package history

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"icinga-webhook-bridge/models"
)

// Logger provides thread-safe JSONL history logging with rotation and filtering.
type Logger struct {
	mu          sync.Mutex
	filePath    string
	maxEntries  int
	appendCount atomic.Int64 // tracks appends since last rotation check
	rotateEvery int64        // check rotation every N appends
	cancelMaint context.CancelFunc
}

// NewLogger creates a new history Logger.
// It ensures the parent directory exists.
func NewLogger(filePath string, maxEntries int) (*Logger, error) {
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("history: create directory %s: %w", dir, err)
	}

	l := &Logger{
		filePath:    filePath,
		maxEntries:  maxEntries,
		rotateEvery: 100, // check rotation every 100 appends
	}

	return l, nil
}

// StartMaintenance starts a background goroutine that periodically checks
// if the history file needs rotation. Call Shutdown to stop it.
func (l *Logger) StartMaintenance(ctx context.Context) {
	mCtx, cancel := context.WithCancel(ctx)
	l.cancelMaint = cancel

	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-mCtx.Done():
				return
			case <-ticker.C:
				l.rotateIfNeeded()
			}
		}
	}()
}

// Shutdown stops the maintenance goroutine.
func (l *Logger) Shutdown() {
	if l.cancelMaint != nil {
		l.cancelMaint()
	}
}

// Append writes a single HistoryEntry to the JSONL file.
// Rotation is handled by the maintenance goroutine, not per-append.
func (l *Logger) Append(entry models.HistoryEntry) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	f, err := os.OpenFile(l.filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("history: open file: %w", err)
	}
	defer f.Close()

	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("history: marshal entry: %w", err)
	}

	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("history: write entry: %w", err)
	}

	// Trigger inline rotation check every N appends (bounded, no goroutine)
	count := l.appendCount.Add(1)
	if count%l.rotateEvery == 0 {
		l.rotateLockedInline()
	}

	return nil
}

// Clear truncates the history file, removing all entries.
func (l *Logger) Clear() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if err := os.Truncate(l.filePath, 0); err != nil {
		return fmt.Errorf("history: truncate file: %w", err)
	}
	l.appendCount.Store(0)
	slog.Info("History cleared", "file", l.filePath)
	return nil
}

// Query reads the history file and returns entries matching the provided filters.
type QueryFilter struct {
	Limit   int
	Service string
	Source  string
	Host    string
	Mode    string
	From    time.Time
	To      time.Time
}

// Query returns history entries matching the filter, ordered newest-first.
func (l *Logger) Query(filter QueryFilter) ([]models.HistoryEntry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	var matched []models.HistoryEntry
	var matchedPos int
	var totalMatched int

	if filter.Limit > 0 {
		matched = make([]models.HistoryEntry, filter.Limit)
	}

	err := l.processAll(func(e models.HistoryEntry) error {
		if filter.Service != "" && e.ServiceName != filter.Service {
			return nil
		}
		if filter.Source != "" && e.SourceKey != filter.Source {
			return nil
		}
		if filter.Host != "" && e.HostName != filter.Host {
			return nil
		}
		if filter.Mode != "" && e.Mode != filter.Mode {
			return nil
		}
		if !filter.From.IsZero() && e.Timestamp.Before(filter.From) {
			return nil
		}
		if !filter.To.IsZero() && e.Timestamp.After(filter.To) {
			return nil
		}

		if filter.Limit > 0 {
			matched[matchedPos] = e
			matchedPos = (matchedPos + 1) % filter.Limit
			totalMatched++
		} else {
			matched = append(matched, e)
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	if filter.Limit > 0 {
		count := totalMatched
		if count > filter.Limit {
			count = filter.Limit
		}
		// Unroll the ring buffer and reverse it simultaneously to get newest first
		result := make([]models.HistoryEntry, count)
		for i := 0; i < count; i++ {
			idx := (matchedPos - 1 - i + filter.Limit) % filter.Limit
			result[i] = matched[idx]
		}
		return result, nil
	}

	// Reverse to get newest first
	for i, j := 0, len(matched)-1; i < j; i, j = i+1, j-1 {
		matched[i], matched[j] = matched[j], matched[i]
	}

	return matched, nil
}

// processAll reads the JSONL file and calls the callback for each entry.
func (l *Logger) processAll(cb func(models.HistoryEntry) error) error {
	f, err := os.Open(l.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("history: open file for reading: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1MB max line size
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var entry models.HistoryEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			continue // skip malformed lines
		}
		if err := cb(entry); err != nil {
			return err
		}
	}

	return scanner.Err()
}

// readAll reads all entries from the JSONL file.
func (l *Logger) readAll() ([]models.HistoryEntry, error) {
	var entries []models.HistoryEntry
	err := l.processAll(func(e models.HistoryEntry) error {
		entries = append(entries, e)
		return nil
	})
	return entries, err
}

// countLines quickly counts the number of newline characters in the history file.
func (l *Logger) countLines() (int, error) {
	f, err := os.Open(l.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("history: open file for line counting: %w", err)
	}
	defer f.Close()

	count := 0
	buf := make([]byte, 32*1024)
	for {
		c, err := f.Read(buf)
		count += bytes.Count(buf[:c], []byte{'\n'})
		if err == io.EOF {
			break
		}
		if err != nil {
			return count, fmt.Errorf("history: read file for line counting: %w", err)
		}
	}
	return count, nil
}

// rotateIfNeeded trims the history file to maxEntries if it exceeds the limit.
// Called by the maintenance goroutine (takes its own lock).
func (l *Logger) rotateIfNeeded() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rotateLockedInline()
}

// rotateLockedInline performs rotation while the lock is already held.
func (l *Logger) rotateLockedInline() {
	// Fast path: avoid expensive processing if the file doesn't need rotation.
	lineCount, err := l.countLines()
	if err != nil || lineCount <= l.maxEntries {
		return
	}

	skip := lineCount - l.maxEntries

	f, err := os.Open(l.filePath)
	if err != nil {
		slog.Error("history: failed to open file for rotation", "error", err)
		return
	}

	tempPath := l.filePath + ".tmp"
	out, err := os.Create(tempPath)
	if err != nil {
		f.Close()
		slog.Error("history: failed to create temp file for rotation", "error", err)
		return
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	writer := bufio.NewWriter(out)

	skipped := 0
	kept := 0
	for scanner.Scan() {
		if skipped < skip {
			skipped++
			continue
		}
		writer.Write(scanner.Bytes())
		writer.WriteByte('\n')
		kept++
	}

	if err := scanner.Err(); err != nil {
		out.Close()
		f.Close()
		os.Remove(tempPath)
		slog.Error("history: scanner error during rotation", "error", err)
		return
	}

	if err := writer.Flush(); err != nil {
		out.Close()
		f.Close()
		os.Remove(tempPath)
		slog.Error("history: failed to flush temp file", "error", err)
		return
	}
	if err := out.Close(); err != nil {
		f.Close()
		os.Remove(tempPath)
		slog.Error("history: failed to close temp file", "error", err)
		return
	}
	f.Close()

	if err := os.Rename(tempPath, l.filePath); err != nil {
		slog.Error("history: failed to rename rotated file", "error", err)
		return
	}

	slog.Info("history: rotated file", "kept", kept, "max", l.maxEntries)
}

// FilePath returns the path to the history JSONL file (used for export).
func (l *Logger) FilePath() string {
	return l.filePath
}

// Stats returns aggregate statistics from the history.
func (l *Logger) Stats() (HistoryStats, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	stats := HistoryStats{
		TotalEntries:       0,
		ByMode:             make(map[string]int),
		ByAction:           make(map[string]int),
		BySeverity:         make(map[string]int),
		BySeverityFiring:   make(map[string]int),
		BySource:           make(map[string]int),
		BySourceIP:         make(map[string]map[string]int),
		BySourceIPLastSeen: make(map[string]map[string]time.Time),
		ErrorCount:         0,
		RecentErrors:       []models.HistoryEntry{},
		RecentEntries:      []models.HistoryEntry{},
	}

	// We want newest recent entries/errors, but we are reading oldest to newest.
	// So we keep ring buffers to avoid O(N) slice shifting, and unroll/reverse at the end.
	recentEntriesBuf := make([]models.HistoryEntry, 100)
	recentEntriesPos := 0
	recentEntriesTotal := 0

	recentErrorsBuf := make([]models.HistoryEntry, 10)
	recentErrorsPos := 0
	recentErrorsTotal := 0

	err := l.processAll(func(e models.HistoryEntry) error {
		stats.TotalEntries++
		stats.ByMode[e.Mode]++
		stats.ByAction[e.Action]++
		if e.Severity != "" {
			stats.BySeverity[e.Severity]++
			if e.Action == "firing" || e.Action == "create" {
				stats.BySeverityFiring[e.Severity]++
			}
		}
		stats.BySource[e.SourceKey]++
		if e.RemoteAddr != "" && e.SourceKey != "" {
			ip := stripPort(e.RemoteAddr)
			if stats.BySourceIP[e.SourceKey] == nil {
				stats.BySourceIP[e.SourceKey] = make(map[string]int)
			}
			stats.BySourceIP[e.SourceKey][ip]++
			if stats.BySourceIPLastSeen[e.SourceKey] == nil {
				stats.BySourceIPLastSeen[e.SourceKey] = make(map[string]time.Time)
			}
			if e.Timestamp.After(stats.BySourceIPLastSeen[e.SourceKey][ip]) {
				stats.BySourceIPLastSeen[e.SourceKey][ip] = e.Timestamp
			}
		}
		if !e.IcingaOK {
			stats.ErrorCount++
		}
		if e.DurationMs > 0 {
			stats.TotalDurationMs += e.DurationMs
		}

		recentEntriesBuf[recentEntriesPos] = e
		recentEntriesPos = (recentEntriesPos + 1) % 20
		recentEntriesTotal++

		if !e.IcingaOK || e.Error != "" {
			recentErrorsBuf[recentErrorsPos] = e
			recentErrorsPos = (recentErrorsPos + 1) % 10
			recentErrorsTotal++
		}

		return nil
	})

	if err != nil {
		return HistoryStats{}, err
	}

	if stats.TotalEntries > 0 {
		stats.AvgDurationMs = stats.TotalDurationMs / int64(stats.TotalEntries)
	}

	// Unroll the ring buffers to get newest first
	recentEntriesCount := recentEntriesTotal
	if recentEntriesCount > 20 {
		recentEntriesCount = 20
	}
	for i := 0; i < recentEntriesCount; i++ {
		idx := (recentEntriesPos - 1 - i + 20) % 20
		stats.RecentEntries = append(stats.RecentEntries, recentEntriesBuf[idx])
	}

	recentErrorsCount := recentErrorsTotal
	if recentErrorsCount > 10 {
		recentErrorsCount = 10
	}
	for i := 0; i < recentErrorsCount; i++ {
		idx := (recentErrorsPos - 1 - i + 10) % 10
		stats.RecentErrors = append(stats.RecentErrors, recentErrorsBuf[idx])
	}

	return stats, nil
}

// HistoryStats holds aggregate statistics about the webhook history.
type HistoryStats struct {
	TotalEntries       int                             `json:"total_entries"`
	ByMode             map[string]int                  `json:"by_mode"`
	ByAction           map[string]int                  `json:"by_action"`
	BySeverity         map[string]int                  `json:"by_severity"`
	BySeverityFiring   map[string]int                  `json:"by_severity_firing"`
	BySource           map[string]int                  `json:"by_source"`
	BySourceIP         map[string]map[string]int       `json:"by_source_ip"`
	BySourceIPLastSeen map[string]map[string]time.Time `json:"-"`
	ErrorCount         int                             `json:"error_count"`
	TotalDurationMs    int64                           `json:"total_duration_ms"`
	AvgDurationMs      int64                           `json:"avg_duration_ms"`
	RecentErrors       []models.HistoryEntry           `json:"recent_errors"`
	RecentEntries      []models.HistoryEntry           `json:"recent_entries"`
}

// stripPort removes the port from a host:port address, returning just the IP.
func stripPort(addr string) string {
	// Fast path for IPv4: avoid expensive net.SplitHostPort and memory allocations inside hot loops.
	// We safely assume it's IPv4 with a port if there is exactly one colon.
	if strings.Count(addr, ":") == 1 {
		if i := strings.LastIndexByte(addr, ':'); i >= 0 {
			return addr[:i]
		}
	}

	// Fallback to strict parsing for IPv6 addresses (which contain multiple colons)
	// and addresses without ports.
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}

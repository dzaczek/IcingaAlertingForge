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

	entries, err := l.readAll()
	if err != nil {
		return nil, err
	}

	// Apply filters
	var filtered []models.HistoryEntry
	for _, e := range entries {
		if filter.Service != "" && e.ServiceName != filter.Service {
			continue
		}
		if filter.Source != "" && e.SourceKey != filter.Source {
			continue
		}
		if filter.Host != "" && e.HostName != filter.Host {
			continue
		}
		if filter.Mode != "" && e.Mode != filter.Mode {
			continue
		}
		if !filter.From.IsZero() && e.Timestamp.Before(filter.From) {
			continue
		}
		if !filter.To.IsZero() && e.Timestamp.After(filter.To) {
			continue
		}
		filtered = append(filtered, e)
	}

	// Reverse to get newest first
	for i, j := 0, len(filtered)-1; i < j; i, j = i+1, j-1 {
		filtered[i], filtered[j] = filtered[j], filtered[i]
	}

	// Apply limit
	if filter.Limit > 0 && len(filtered) > filter.Limit {
		filtered = filtered[:filter.Limit]
	}

	return filtered, nil
}

// readAll reads all entries from the JSONL file.
func (l *Logger) readAll() ([]models.HistoryEntry, error) {
	f, err := os.Open(l.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("history: open file for reading: %w", err)
	}
	defer f.Close()

	var entries []models.HistoryEntry
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
		entries = append(entries, entry)
	}

	return entries, scanner.Err()
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
	// Fast path: avoid expensive readAll (JSON parsing) if the file doesn't need rotation.
	lineCount, err := l.countLines()
	if err != nil || lineCount <= l.maxEntries {
		return
	}

	entries, err := l.readAll()
	if err != nil || len(entries) <= l.maxEntries {
		return
	}

	// Keep only the last maxEntries
	entries = entries[len(entries)-l.maxEntries:]

	f, err := os.Create(l.filePath)
	if err != nil {
		slog.Error("history: failed to rotate file", "error", err)
		return
	}
	defer f.Close()

	writer := bufio.NewWriter(f)
	for _, e := range entries {
		data, err := json.Marshal(e)
		if err != nil {
			continue
		}
		writer.Write(data)
		writer.WriteByte('\n')
	}
	writer.Flush()

	slog.Info("history: rotated file", "kept", len(entries), "max", l.maxEntries)
}

// FilePath returns the path to the history JSONL file (used for export).
func (l *Logger) FilePath() string {
	return l.filePath
}

// Stats returns aggregate statistics from the history.
func (l *Logger) Stats() (HistoryStats, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	entries, err := l.readAll()
	if err != nil {
		return HistoryStats{}, err
	}

	stats := HistoryStats{
		TotalEntries:  len(entries),
		ByMode:        make(map[string]int),
		ByAction:      make(map[string]int),
		BySeverity:       make(map[string]int),
		BySeverityFiring: make(map[string]int),
		BySource:         make(map[string]int),
		BySourceIP:         make(map[string]map[string]int),
		BySourceIPLastSeen: make(map[string]map[string]time.Time),
		ErrorCount:    0,
		RecentErrors:  []models.HistoryEntry{},
		RecentEntries: []models.HistoryEntry{},
	}

	for _, e := range entries {
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
	}

	if stats.TotalEntries > 0 {
		stats.AvgDurationMs = stats.TotalDurationMs / int64(stats.TotalEntries)
	}

	// Collect recent entries (last 20) — newest first
	recentCount := 20
	if len(entries) < recentCount {
		recentCount = len(entries)
	}
	for i := len(entries) - 1; i >= len(entries)-recentCount; i-- {
		stats.RecentEntries = append(stats.RecentEntries, entries[i])
	}

	// Collect recent errors (last 10) — newest first
	for i := len(entries) - 1; i >= 0 && len(stats.RecentErrors) < 10; i-- {
		if !entries[i].IcingaOK || entries[i].Error != "" {
			stats.RecentErrors = append(stats.RecentErrors, entries[i])
		}
	}

	return stats, nil
}

// HistoryStats holds aggregate statistics about the webhook history.
type HistoryStats struct {
	TotalEntries    int                          `json:"total_entries"`
	ByMode          map[string]int               `json:"by_mode"`
	ByAction        map[string]int               `json:"by_action"`
	BySeverity        map[string]int               `json:"by_severity"`
	BySeverityFiring  map[string]int               `json:"by_severity_firing"`
	BySource        map[string]int               `json:"by_source"`
	BySourceIP         map[string]map[string]int       `json:"by_source_ip"`
	BySourceIPLastSeen map[string]map[string]time.Time `json:"-"`
	ErrorCount      int                          `json:"error_count"`
	TotalDurationMs int64                        `json:"total_duration_ms"`
	AvgDurationMs   int64                        `json:"avg_duration_ms"`
	RecentErrors    []models.HistoryEntry        `json:"recent_errors"`
	RecentEntries   []models.HistoryEntry        `json:"recent_entries"`
}

// stripPort removes the port from a host:port address, returning just the IP.
func stripPort(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}

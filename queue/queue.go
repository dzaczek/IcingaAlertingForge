package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// CheckResultSender sends a passive check result to Icinga2.
type CheckResultSender interface {
	SendCheckResult(host, service string, exitStatus int, message string) error
}

// Item represents a single queued check result waiting for retry.
type Item struct {
	ID         string    `json:"id"`
	Host       string    `json:"host"`
	Service    string    `json:"service"`
	ExitStatus int       `json:"exit_status"`
	Message    string    `json:"message"`
	Source     string    `json:"source"`
	RequestID  string    `json:"request_id"`
	EnqueuedAt time.Time `json:"enqueued_at"`
	Attempts   int       `json:"attempts"`
	NextRetry  time.Time `json:"next_retry"`
}

// Stats holds queue statistics for dashboard/API consumption.
type Stats struct {
	Depth        int       `json:"depth"`
	MaxSize      int       `json:"max_size"`
	OldestItem   time.Time `json:"oldest_item,omitempty"`
	TotalRetried int64     `json:"total_retried"`
	TotalDropped int64     `json:"total_dropped"`
	TotalFailed  int64     `json:"total_failed"`
	Processing   bool      `json:"processing"`
}

// Config holds retry queue configuration.
type Config struct {
	Enabled       bool
	MaxSize       int
	FilePath      string
	RetryBase     time.Duration
	RetryMax      time.Duration
	CheckInterval time.Duration
}

// Queue buffers failed Icinga2 check results and retries them with exponential backoff.
type Queue struct {
	mu     sync.Mutex
	items  []Item
	config Config
	sender CheckResultSender

	totalRetried atomic.Int64
	totalDropped atomic.Int64
	totalFailed  atomic.Int64
	processing   atomic.Bool

	cancelFunc context.CancelFunc
}

// New creates a new retry queue. Call Start() to begin processing.
func New(cfg Config, sender CheckResultSender) *Queue {
	q := &Queue{
		items:  make([]Item, 0),
		config: cfg,
		sender: sender,
	}

	if cfg.FilePath != "" {
		q.loadFromDisk()
	}

	return q
}

// Start begins the background retry processor.
func (q *Queue) Start(ctx context.Context) {
	procCtx, cancel := context.WithCancel(ctx)
	q.cancelFunc = cancel

	go q.processor(procCtx)
	slog.Info("Retry queue started",
		"max_size", q.config.MaxSize,
		"retry_base", q.config.RetryBase,
		"retry_max", q.config.RetryMax,
		"check_interval", q.config.CheckInterval,
	)
}

// Enqueue adds a failed check result to the retry queue.
// Returns an error if the queue is full and the item was dropped.
func (q *Queue) Enqueue(item Item) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	if len(q.items) >= q.config.MaxSize {
		q.totalDropped.Add(1)
		slog.Warn("Retry queue full, dropping oldest item",
			"depth", len(q.items), "max_size", q.config.MaxSize,
			"dropped_service", q.items[0].Service)
		q.items = q.items[1:]
	}

	item.Attempts = 0
	item.EnqueuedAt = time.Now()
	item.NextRetry = time.Now().Add(q.config.RetryBase)
	q.items = append(q.items, item)

	slog.Info("Alert queued for retry",
		"host", item.Host, "service", item.Service,
		"exit_status", item.ExitStatus, "queue_depth", len(q.items))

	return nil
}

// Stats returns current queue statistics.
func (q *Queue) Stats() Stats {
	q.mu.Lock()
	defer q.mu.Unlock()

	s := Stats{
		Depth:        len(q.items),
		MaxSize:      q.config.MaxSize,
		TotalRetried: q.totalRetried.Load(),
		TotalDropped: q.totalDropped.Load(),
		TotalFailed:  q.totalFailed.Load(),
		Processing:   q.processing.Load(),
	}

	if len(q.items) > 0 {
		s.OldestItem = q.items[0].EnqueuedAt
	}

	return s
}

// Flush forces immediate retry of all queued items.
func (q *Queue) Flush() int {
	q.mu.Lock()
	items := make([]Item, len(q.items))
	copy(items, q.items)
	q.mu.Unlock()

	processed := 0
	for i := range items {
		items[i].NextRetry = time.Time{} // force immediate
	}

	for _, item := range items {
		if err := q.sender.SendCheckResult(item.Host, item.Service, item.ExitStatus, item.Message); err == nil {
			q.removeByID(item.ID)
			q.totalRetried.Add(1)
			processed++
		}
	}
	return processed
}

// Drain persists all in-memory items to disk for graceful shutdown.
func (q *Queue) Drain() {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.cancelFunc != nil {
		q.cancelFunc()
	}

	if q.config.FilePath != "" && len(q.items) > 0 {
		if err := q.saveToDisk(); err != nil {
			slog.Error("Failed to persist retry queue on shutdown", "error", err, "items", len(q.items))
		} else {
			slog.Info("Retry queue persisted to disk", "items", len(q.items), "path", q.config.FilePath)
		}
	}
}

// Depth returns current number of items in the queue.
func (q *Queue) Depth() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}

func (q *Queue) processor(ctx context.Context) {
	ticker := time.NewTicker(q.config.CheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			q.processReady()
		}
	}
}

func (q *Queue) processReady() {
	q.processing.Store(true)
	defer q.processing.Store(false)

	q.mu.Lock()
	now := time.Now()
	var ready []Item
	for _, item := range q.items {
		if !item.NextRetry.After(now) {
			ready = append(ready, item)
		}
	}
	q.mu.Unlock()

	if len(ready) == 0 {
		return
	}

	slog.Info("Retry queue processing", "ready", len(ready))

	for _, item := range ready {
		err := q.sender.SendCheckResult(item.Host, item.Service, item.ExitStatus, item.Message)
		if err == nil {
			q.removeByID(item.ID)
			q.totalRetried.Add(1)
			slog.Info("Retry succeeded",
				"host", item.Host, "service", item.Service,
				"attempts", item.Attempts+1, "request_id", item.RequestID)
		} else {
			q.incrementAttempt(item.ID)
			q.totalFailed.Add(1)
			slog.Warn("Retry failed",
				"host", item.Host, "service", item.Service,
				"attempts", item.Attempts+1, "error", err)
		}
	}

	// Persist after processing
	if q.config.FilePath != "" {
		q.mu.Lock()
		_ = q.saveToDisk()
		q.mu.Unlock()
	}
}

func (q *Queue) removeByID(id string) {
	q.mu.Lock()
	defer q.mu.Unlock()

	for i, item := range q.items {
		if item.ID == id {
			q.items = append(q.items[:i], q.items[i+1:]...)
			return
		}
	}
}

func (q *Queue) incrementAttempt(id string) {
	q.mu.Lock()
	defer q.mu.Unlock()

	for i, item := range q.items {
		if item.ID == id {
			q.items[i].Attempts++
			q.items[i].NextRetry = time.Now().Add(backoff(q.items[i].Attempts, q.config.RetryBase, q.config.RetryMax))
			return
		}
	}
}

func backoff(attempts int, base, max time.Duration) time.Duration {
	d := time.Duration(float64(base) * math.Pow(2, float64(attempts)))
	if d > max {
		return max
	}
	return d
}

func (q *Queue) saveToDisk() error {
	data, err := json.MarshalIndent(q.items, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal queue: %w", err)
	}
	return os.WriteFile(q.config.FilePath, data, 0o600)
}

func (q *Queue) loadFromDisk() {
	data, err := os.ReadFile(q.config.FilePath)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Error("Failed to load retry queue from disk", "error", err)
		}
		return
	}

	var items []Item
	if err := json.Unmarshal(data, &items); err != nil {
		slog.Error("Failed to parse retry queue file", "error", err)
		return
	}

	q.items = items
	slog.Info("Retry queue restored from disk", "items", len(items), "path", q.config.FilePath)
}

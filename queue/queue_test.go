package queue

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

type mockSender struct {
	failCount atomic.Int32
	calls     atomic.Int32
}

func (m *mockSender) SendCheckResult(host, service string, exitStatus int, message string) error {
	m.calls.Add(1)
	if m.failCount.Load() > 0 {
		m.failCount.Add(-1)
		return fmt.Errorf("icinga2 unreachable")
	}
	return nil
}

func testConfig(filePath string) Config {
	return Config{
		Enabled:       true,
		MaxSize:       10,
		FilePath:      filePath,
		RetryBase:     50 * time.Millisecond,
		RetryMax:      200 * time.Millisecond,
		CheckInterval: 50 * time.Millisecond,
	}
}

func testItem(id, host, service string) Item {
	return Item{
		ID:         id,
		Host:       host,
		Service:    service,
		ExitStatus: 2,
		Message:    "CRITICAL: test",
		Source:     "test",
		RequestID:  "req-" + id,
	}
}

func TestEnqueue(t *testing.T) {
	sender := &mockSender{}
	q := New(testConfig(""), sender)

	if err := q.Enqueue(testItem("1", "host-a", "svc1")); err != nil {
		t.Fatal(err)
	}

	stats := q.Stats()
	if stats.Depth != 1 {
		t.Fatalf("expected depth 1, got %d", stats.Depth)
	}
}

func TestEnqueueOverflow(t *testing.T) {
	sender := &mockSender{}
	cfg := testConfig("")
	cfg.MaxSize = 3
	q := New(cfg, sender)

	for i := 0; i < 5; i++ {
		_ = q.Enqueue(testItem(fmt.Sprintf("%d", i), "host", fmt.Sprintf("svc%d", i)))
	}

	stats := q.Stats()
	if stats.Depth != 3 {
		t.Fatalf("expected depth 3 (max), got %d", stats.Depth)
	}
	if stats.TotalDropped != 2 {
		t.Fatalf("expected 2 dropped, got %d", stats.TotalDropped)
	}
}

func TestProcessorRetries(t *testing.T) {
	sender := &mockSender{}
	q := New(testConfig(""), sender)

	// Icinga down for first 2 attempts
	sender.failCount.Store(2)

	_ = q.Enqueue(testItem("1", "host-a", "svc1"))

	ctx, cancel := context.WithCancel(context.Background())
	q.Start(ctx)

	// Wait for retries
	time.Sleep(500 * time.Millisecond)
	cancel()

	stats := q.Stats()
	if stats.TotalRetried != 1 {
		t.Fatalf("expected 1 retried, got %d", stats.TotalRetried)
	}
	if stats.Depth != 0 {
		t.Fatalf("expected empty queue, got depth %d", stats.Depth)
	}
}

func TestPersistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "queue.json")

	sender := &mockSender{}
	sender.failCount.Store(100) // never succeed

	q := New(testConfig(path), sender)
	_ = q.Enqueue(testItem("1", "host-a", "svc1"))
	_ = q.Enqueue(testItem("2", "host-a", "svc2"))

	// Drain persists to disk
	q.Drain()

	// Verify file exists
	if _, err := os.Stat(path); err != nil {
		t.Fatal("queue file not persisted")
	}

	// New queue should restore items
	q2 := New(testConfig(path), sender)
	stats := q2.Stats()
	if stats.Depth != 2 {
		t.Fatalf("expected 2 restored items, got %d", stats.Depth)
	}
}

func TestFlush(t *testing.T) {
	sender := &mockSender{}
	q := New(testConfig(""), sender)

	_ = q.Enqueue(testItem("1", "host-a", "svc1"))
	_ = q.Enqueue(testItem("2", "host-a", "svc2"))

	processed := q.Flush()
	if processed != 2 {
		t.Fatalf("expected 2 processed, got %d", processed)
	}
	if q.Depth() != 0 {
		t.Fatalf("expected empty queue after flush, got %d", q.Depth())
	}
}

func TestBackoff(t *testing.T) {
	base := 5 * time.Second
	max := 5 * time.Minute

	tests := []struct {
		attempts int
		expected time.Duration
	}{
		{0, 5 * time.Second},
		{1, 10 * time.Second},
		{2, 20 * time.Second},
		{3, 40 * time.Second},
		{4, 80 * time.Second},
		{10, 5 * time.Minute}, // capped
	}

	for _, tt := range tests {
		got := backoff(tt.attempts, base, max)
		if got != tt.expected {
			t.Errorf("backoff(%d) = %v, want %v", tt.attempts, got, tt.expected)
		}
	}
}

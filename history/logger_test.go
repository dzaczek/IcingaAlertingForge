package history

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"icinga-webhook-bridge/models"
)

func newTestLogger(t *testing.T) *Logger {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test-history.jsonl")
	l, err := NewLogger(path, 100)
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}
	return l
}

func sampleEntry(service, mode, action, source string) models.HistoryEntry {
	return models.HistoryEntry{
		Timestamp:   time.Now().UTC(),
		RequestID:   "req-" + service,
		SourceKey:   source,
		HostName:    "test-host",
		Mode:        mode,
		Action:      action,
		ServiceName: service,
		Severity:    "critical",
		ExitStatus:  2,
		Message:     "test message",
		IcingaOK:    true,
		DurationMs:  42,
	}
}

func TestLogger_AppendAndQuery(t *testing.T) {
	l := newTestLogger(t)

	if err := l.Append(sampleEntry("svc-1", "work", "firing", "grafana-prod")); err != nil {
		t.Fatalf("Append failed: %v", err)
	}

	entries, err := l.Query(QueryFilter{Limit: 10})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].ServiceName != "svc-1" {
		t.Errorf("expected service svc-1, got %s", entries[0].ServiceName)
	}
}

func TestLogger_QueryFilterByService(t *testing.T) {
	l := newTestLogger(t)

	l.Append(sampleEntry("alpha", "work", "firing", "grafana-prod"))
	l.Append(sampleEntry("beta", "work", "firing", "grafana-prod"))
	l.Append(sampleEntry("alpha", "work", "resolved", "grafana-prod"))

	entries, err := l.Query(QueryFilter{Limit: 100, Service: "alpha"})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries for alpha, got %d", len(entries))
	}
}

func TestLogger_QueryFilterBySource(t *testing.T) {
	l := newTestLogger(t)

	l.Append(sampleEntry("svc", "work", "firing", "grafana-prod"))
	l.Append(sampleEntry("svc", "work", "firing", "grafana-dev"))

	entries, err := l.Query(QueryFilter{Limit: 100, Source: "grafana-dev"})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry for grafana-dev, got %d", len(entries))
	}
}

func TestLogger_QueryFilterByHost(t *testing.T) {
	l := newTestLogger(t)

	first := sampleEntry("svc", "work", "firing", "grafana-prod")
	first.HostName = "team-a-host"
	second := sampleEntry("svc", "work", "firing", "grafana-prod")
	second.HostName = "team-b-host"
	l.Append(first)
	l.Append(second)

	entries, err := l.Query(QueryFilter{Limit: 100, Host: "team-b-host"})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry for team-b-host, got %d", len(entries))
	}
	if entries[0].HostName != "team-b-host" {
		t.Fatalf("expected host team-b-host, got %s", entries[0].HostName)
	}
}

func TestLogger_QueryFilterByMode(t *testing.T) {
	l := newTestLogger(t)

	l.Append(sampleEntry("svc", "work", "firing", "grafana-prod"))
	l.Append(sampleEntry("svc", "test", "create", "grafana-prod"))

	entries, err := l.Query(QueryFilter{Limit: 100, Mode: "test"})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry for test mode, got %d", len(entries))
	}
}

func TestLogger_QueryLimit(t *testing.T) {
	l := newTestLogger(t)

	for i := 0; i < 20; i++ {
		l.Append(sampleEntry("svc", "work", "firing", "src"))
	}

	entries, err := l.Query(QueryFilter{Limit: 5})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if len(entries) != 5 {
		t.Fatalf("expected 5 entries, got %d", len(entries))
	}
}

func TestLogger_QueryNewestFirst(t *testing.T) {
	l := newTestLogger(t)

	e1 := sampleEntry("first", "work", "firing", "src")
	e1.Timestamp = time.Now().UTC().Add(-time.Hour)
	l.Append(e1)

	e2 := sampleEntry("second", "work", "firing", "src")
	e2.Timestamp = time.Now().UTC()
	l.Append(e2)

	entries, err := l.Query(QueryFilter{Limit: 10})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if entries[0].ServiceName != "second" {
		t.Errorf("expected newest first, got %s", entries[0].ServiceName)
	}
}

func TestLogger_Rotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rotation-test.jsonl")
	l, err := NewLogger(path, 5) // max 5 entries
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}

	// Set low rotateEvery so inline rotation triggers during test
	l.rotateEvery = 3

	for i := 0; i < 10; i++ {
		l.Append(sampleEntry("svc", "work", "firing", "src"))
	}

	// Also trigger explicit rotation to ensure it runs
	l.rotateIfNeeded()

	entries, err := l.Query(QueryFilter{Limit: 100})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if len(entries) > 5 {
		t.Errorf("expected at most 5 entries after rotation, got %d", len(entries))
	}
}

func TestLogger_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.jsonl")
	l, err := NewLogger(path, 100)
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}

	entries, err := l.Query(QueryFilter{Limit: 10})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries from empty file, got %d", len(entries))
	}
}

func TestLogger_Stats(t *testing.T) {
	l := newTestLogger(t)

	l.Append(sampleEntry("svc-a", "work", "firing", "grafana-prod"))
	l.Append(sampleEntry("svc-b", "test", "create", "grafana-dev"))

	errEntry := sampleEntry("svc-c", "work", "firing", "grafana-prod")
	errEntry.IcingaOK = false
	errEntry.Error = "connection refused"
	l.Append(errEntry)

	stats, err := l.Stats()
	if err != nil {
		t.Fatalf("Stats failed: %v", err)
	}
	if stats.TotalEntries != 3 {
		t.Errorf("expected 3 total entries, got %d", stats.TotalEntries)
	}
	if stats.ErrorCount != 1 {
		t.Errorf("expected 1 error, got %d", stats.ErrorCount)
	}
	if stats.ByMode["work"] != 2 {
		t.Errorf("expected 2 work entries, got %d", stats.ByMode["work"])
	}
	if len(stats.RecentErrors) != 1 {
		t.Errorf("expected 1 recent error, got %d", len(stats.RecentErrors))
	}
}

func TestLogger_FilePath(t *testing.T) {
	l := newTestLogger(t)
	if l.FilePath() == "" {
		t.Error("expected non-empty file path")
	}
	if _, err := os.Stat(filepath.Dir(l.FilePath())); err != nil {
		t.Errorf("expected parent directory to exist: %v", err)
	}
}

func TestLogger_StartMaintenanceAndShutdown(t *testing.T) {
	l := newTestLogger(t)

	ctx := t.Context()
	l.StartMaintenance(ctx)
	l.Shutdown()
}

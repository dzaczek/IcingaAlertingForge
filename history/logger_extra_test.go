package history

import (
	"testing"

	"icinga-webhook-bridge/models"
)

func TestLogger_Clear(t *testing.T) {
	l := newTestLogger(t)

	// Append then clear
	l.Append(sampleEntry("svc", "webhook", "forward", "grafana"))

	if err := l.Clear(); err != nil {
		t.Fatalf("Clear failed: %v", err)
	}

	// After clear, Query should return empty
	entries, err := l.Query(QueryFilter{Limit: 100})
	if err != nil {
		t.Fatalf("Query after Clear failed: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries after Clear, got %d", len(entries))
	}
}

func TestLogger_Clear_NonExistentFile(t *testing.T) {
	l := newTestLogger(t)
	// File does not exist yet — Clear should fail with an error
	err := l.Clear()
	if err == nil {
		t.Error("expected error when clearing non-existent file")
	}
}

func TestLogger_Append_Remote(t *testing.T) {
	l := newTestLogger(t)
	entry := models.HistoryEntry{
		ServiceName: "svc-ip",
		RemoteAddr:  "192.0.2.1:12345",
	}
	if err := l.Append(entry); err != nil {
		t.Fatalf("Append failed: %v", err)
	}
}

func TestStripPort(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"192.0.2.1:8080", "192.0.2.1"},
		{"10.0.0.1:443", "10.0.0.1"},
		{"[::1]:9000", "::1"},
		{"plainhost", "plainhost"},
	}

	for _, tt := range tests {
		got := stripPort(tt.input)
		if got != tt.want {
			t.Errorf("stripPort(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestLogger_RotationFileErrors(t *testing.T) {
	l := newTestLogger(t)
	// Set max entries low
	l.maxEntries = 2
	l.rotateEvery = 1

	// Make the file unreadable to trigger error inside rotateLockedInline
	l.Append(sampleEntry("svc1", "webhook", "forward", "test"))
	l.Append(sampleEntry("svc2", "webhook", "forward", "test"))
	l.Append(sampleEntry("svc3", "webhook", "forward", "test"))

	// Try creating a bad file path for os.CreateTemp inside rotateLockedInline
	oldPath := l.filePath
	l.filePath = "/invalid-dir/file.log"
	l.rotateLockedInline() // Should fail on CreateTemp
	l.filePath = oldPath
}

func TestLogger_RotationFileErrorsWrite(t *testing.T) {
	l := newTestLogger(t)
	l.maxEntries = 0 // force rotation

	// We can't easily mock write errors with os.CreateTemp without changing code
	// But we can cover the open file error:
	oldPath := l.filePath
	l.filePath = "/invalid-dir/file.log"
	l.entryCount.Store(10) // make it think it has entries
	l.rotateLockedInline()
	l.filePath = oldPath
}

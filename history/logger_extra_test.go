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

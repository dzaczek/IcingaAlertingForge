package history

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
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

func TestLogger_UpdateConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config-test.jsonl")
	l, err := NewLogger(path, 100)
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}

	for i := 0; i < 10; i++ {
		l.Append(sampleEntry("svc", "work", "firing", "src"))
	}

	newPath := filepath.Join(dir, "new-config-test.jsonl")
	err = l.UpdateConfig(newPath, 5)
	if err != nil {
		t.Fatalf("UpdateConfig failed: %v", err)
	}

	if l.FilePath() != newPath {
		t.Errorf("expected path %s, got %s", newPath, l.FilePath())
	}

	entries, err := l.Query(QueryFilter{Limit: 10})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if len(entries) > 5 {
		t.Errorf("expected at most 5 entries after UpdateConfig, got %d", len(entries))
	}
}

func TestHandler_HandleExport(t *testing.T) {
	l := newTestLogger(t)
	l.Append(sampleEntry("svc-export", "work", "firing", "grafana-prod"))

	h := NewHandler(l)
	req := httptest.NewRequest(http.MethodGet, "/export", nil)
	w := httptest.NewRecorder()
	h.HandleExport(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", resp.StatusCode)
	}
	if resp.Header.Get("Content-Type") != "application/x-ndjson" {
		t.Errorf("unexpected content type: %s", resp.Header.Get("Content-Type"))
	}

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "svc-export") {
		t.Errorf("expected body to contain svc-export, got %s", string(body))
	}
}

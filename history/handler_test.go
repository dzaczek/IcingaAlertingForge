package history

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"icinga-webhook-bridge/models"
)

func testHandler(t *testing.T) *Handler {
	t.Helper()
	logger := newTestLogger(t)
	return NewHandler(logger)
}

func TestHandler_HandleHistory_Basic(t *testing.T) {
	h := testHandler(t)

	// Append a couple of entries
	h.logger.Append(sampleEntry("svc-a", "webhook", "forward", "grafana"))
	h.logger.Append(sampleEntry("svc-b", "webhook", "forward", "alertmanager"))

	req := httptest.NewRequest(http.MethodGet, "/history", nil)
	rr := httptest.NewRecorder()
	h.HandleHistory(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected application/json, got %q", ct)
	}
}

func TestHandler_HandleHistory_MethodNotAllowed(t *testing.T) {
	h := testHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/history", nil)
	rr := httptest.NewRecorder()
	h.HandleHistory(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rr.Code)
	}
}

func TestHandler_HandleHistory_LimitCap(t *testing.T) {
	h := testHandler(t)
	// Limit above maxHistoryLimit should be capped
	req := httptest.NewRequest(http.MethodGet, "/history?limit=99999", nil)
	rr := httptest.NewRecorder()
	h.HandleHistory(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestHandler_HandleHistory_FilterByService(t *testing.T) {
	h := testHandler(t)
	h.logger.Append(sampleEntry("target-svc", "webhook", "forward", "grafana"))
	h.logger.Append(sampleEntry("other-svc", "webhook", "forward", "grafana"))

	req := httptest.NewRequest(http.MethodGet, "/history?service=target-svc", nil)
	rr := httptest.NewRecorder()
	h.HandleHistory(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestHandler_HandleHistory_DateFilter(t *testing.T) {
	h := testHandler(t)
	h.logger.Append(models.HistoryEntry{
		Timestamp:   time.Now().UTC(),
		ServiceName: "dated-svc",
		Mode:        "webhook",
		Action:      "forward",
	})

	today := time.Now().UTC().Format("2006-01-02")
	req := httptest.NewRequest(http.MethodGet, "/history?from="+today+"&to="+today, nil)
	rr := httptest.NewRecorder()
	h.HandleHistory(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestHandler_HandleHistory_RFC3339DateFilter(t *testing.T) {
	h := testHandler(t)
	from := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	to := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	req := httptest.NewRequest(http.MethodGet, "/history?from="+from+"&to="+to, nil)
	rr := httptest.NewRecorder()
	h.HandleHistory(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestHandler_HandleExport_NoFile(t *testing.T) {
	h := testHandler(t)
	// Logger file doesn't exist yet — should return 200 with empty body
	req := httptest.NewRequest(http.MethodGet, "/history/export", nil)
	rr := httptest.NewRecorder()
	h.HandleExport(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/x-ndjson" {
		t.Errorf("expected application/x-ndjson, got %q", ct)
	}
}

func TestHandler_HandleExport_WithData(t *testing.T) {
	h := testHandler(t)
	h.logger.Append(sampleEntry("export-svc", "webhook", "forward", "grafana"))

	req := httptest.NewRequest(http.MethodGet, "/history/export", nil)
	rr := httptest.NewRecorder()
	h.HandleExport(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	if !strings.Contains(rr.Header().Get("Content-Disposition"), "webhook-history.jsonl") {
		t.Error("expected Content-Disposition with filename")
	}
}

func TestHandler_HandleExport_MethodNotAllowed(t *testing.T) {
	h := testHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/history/export", nil)
	rr := httptest.NewRecorder()
	h.HandleExport(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rr.Code)
	}
}

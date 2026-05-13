package handler

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"icinga-webhook-bridge/cache"
	"icinga-webhook-bridge/config"
	"icinga-webhook-bridge/history"
	"icinga-webhook-bridge/metrics"
)

func TestDashboard_ServeHTTP_Basic(t *testing.T) {
	histLogger, _ := history.NewLogger(filepath.Join(t.TempDir(), "hist.jsonl"), 100)
	h := &DashboardHandler{
		Cache:     cache.NewServiceCache(60),
		History:   histLogger,
		Metrics:   metrics.NewCollector(),
		Targets:   map[string]config.TargetConfig{},
		Version:   "test",
		AdminPass: "test",
	}

	t.Run("non-GET method returns 405", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("expected 405, got %d", rr.Code)
		}
	})

	t.Run("GET renders dashboard", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("expected 200, got %d: %s", rr.Code, rr.Body.String())
		}
	})

	t.Run("GET with admin=1 without creds returns 401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/?admin=1", nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", rr.Code)
		}
	})
}

package handler

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
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

	t.Run("GET renders dashboard with logged user", func(t *testing.T) {
		h.AdminUser = "admin"
		h.AdminPass = "test"
		req := httptest.NewRequest(http.MethodGet, "/?admin=1", nil)
		req.SetBasicAuth("admin", "test")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("expected 200, got %d: %s", rr.Code, rr.Body.String())
		}

		body := rr.Body.String()
		expectedStr := "[COMMAND ACCESS - USER: admin]"
		if !strings.Contains(body, expectedStr) {
			t.Errorf("expected body to contain %q, but it did not", expectedStr)
		}
	})

	t.Run("GET with admin=1 without creds redirects to login", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/?admin=1", nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusSeeOther {
			t.Errorf("expected 303 redirect, got %d", rr.Code)
		}
		if loc := rr.Header().Get("Location"); !strings.HasPrefix(loc, "/login") {
			t.Errorf("expected redirect to /login, got %q", loc)
		}
	})
}

func TestDashboard_ServeHTTP_UnauthenticatedAdminRedirectsToLogin(t *testing.T) {
	histLogger, _ := history.NewLogger(filepath.Join(t.TempDir(), "hist.jsonl"), 100)
	h := &DashboardHandler{
		Cache:     cache.NewServiceCache(60),
		History:   histLogger,
		Metrics:   metrics.NewCollector(),
		Targets:   map[string]config.TargetConfig{},
		Version:   "test",
		AdminPass: "test",
	}

	req := httptest.NewRequest(http.MethodGet, "/status/beauty?admin=1", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect, got %d", rr.Code)
	}
	loc := rr.Header().Get("Location")
	if !strings.HasPrefix(loc, "/login?next=") {
		t.Errorf("expected redirect to /login with next param, got %q", loc)
	}
}

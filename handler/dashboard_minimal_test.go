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

	t.Run("GET with admin=1 without creds returns 401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/?admin=1", nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", rr.Code)
		}
	})
}

func TestDashboard_ServeHTTP_AdminLoggedOut(t *testing.T) {
	histLogger, _ := history.NewLogger(filepath.Join(t.TempDir(), "hist.jsonl"), 100)
	h := &DashboardHandler{
		Cache:     cache.NewServiceCache(60),
		History:   histLogger,
		Metrics:   metrics.NewCollector(),
		Targets:   map[string]config.TargetConfig{},
		Version:   "test",
		AdminPass: "test",
	}

	req := httptest.NewRequest(http.MethodGet, "/?admin=1", nil)
	req.AddCookie(&http.Cookie{Name: "_logged_out", Value: "1"})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}

	cookies := rr.Result().Cookies()
	var found bool
	for _, c := range cookies {
		if c.Name == "_logged_out" {
			found = true
			if c.Value != "" {
				t.Errorf("expected empty value for _logged_out cookie, got %q", c.Value)
			}
			if c.MaxAge != -1 {
				t.Errorf("expected MaxAge -1, got %d", c.MaxAge)
			}
			if c.Secure {
				t.Errorf("expected Secure false for non-TLS request, got true")
			}
		}
	}
	if !found {
		t.Errorf("expected _logged_out cookie to be cleared")
	}

	// Test over TLS
	reqTLS := httptest.NewRequest(http.MethodGet, "/?admin=1", nil)
	reqTLS.AddCookie(&http.Cookie{Name: "_logged_out", Value: "1"})
	reqTLS.Header.Set("X-Forwarded-Proto", "https")
	rrTLS := httptest.NewRecorder()
	h.ServeHTTP(rrTLS, reqTLS)

	cookiesTLS := rrTLS.Result().Cookies()
	for _, c := range cookiesTLS {
		if c.Name == "_logged_out" && !c.Secure {
			t.Errorf("expected Secure true for TLS request, got false")
		}
	}
}

func TestDashboard_ServeHTTP_WriteErrors(t *testing.T) {
	histLogger, _ := history.NewLogger(filepath.Join(t.TempDir(), "hist.jsonl"), 100)
	h := &DashboardHandler{
		Cache:     cache.NewServiceCache(60),
		History:   histLogger,
		Metrics:   metrics.NewCollector(),
		Targets:   map[string]config.TargetConfig{},
		Version:   "test",
		AdminPass: "test",
	}

	req := httptest.NewRequest(http.MethodGet, "/?admin=1", nil)
	// Important: add the _logged_out cookie to trigger the early return with the Enter credentials page.
	req.AddCookie(&http.Cookie{Name: "_logged_out", Value: "1"})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}

	body := rr.Body.String()
	expectedStr := "Enter credentials"
	if !strings.Contains(body, expectedStr) {
		t.Errorf("expected body to contain %q, but it did not", expectedStr)
	}
}

package handler

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"icinga-webhook-bridge/cache"
	"icinga-webhook-bridge/config"
	"icinga-webhook-bridge/history"
	"icinga-webhook-bridge/metrics"
	"icinga-webhook-bridge/models"
	"icinga-webhook-bridge/rbac"
)

func TestToDashboardAlert(t *testing.T) {
	base := models.HistoryEntry{
		RequestID:  "req-1",
		SourceKey:  "grafana",
		HostName:   "host-a",
		Mode:       "work",
		Action:     "firing",
		ExitStatus: 2,
		IcingaOK:   true,
		Error:      "",
	}

	t.Run("critical status", func(t *testing.T) {
		da := toDashboardAlert(base)
		if da.StatusLabel != "CRITICAL" {
			t.Errorf("expected CRITICAL, got %s", da.StatusLabel)
		}
		if da.StatusClass != "critical" {
			t.Errorf("expected critical class, got %s", da.StatusClass)
		}
	})

	t.Run("warning status", func(t *testing.T) {
		e := base
		e.ExitStatus = 1
		da := toDashboardAlert(e)
		if da.StatusLabel != "WARNING" {
			t.Errorf("expected WARNING, got %s", da.StatusLabel)
		}
		if da.StatusClass != "warning" {
			t.Errorf("expected warning class, got %s", da.StatusClass)
		}
	})

	t.Run("ok status", func(t *testing.T) {
		e := base
		e.ExitStatus = 0
		da := toDashboardAlert(e)
		if da.StatusLabel != "OK" {
			t.Errorf("expected OK, got %s", da.StatusLabel)
		}
		if da.StatusClass != "ok" {
			t.Errorf("expected ok class, got %s", da.StatusClass)
		}
	})

	t.Run("test mode override", func(t *testing.T) {
		e := base
		e.Mode = "test"
		da := toDashboardAlert(e)
		if da.StatusLabel != "TEST" {
			t.Errorf("expected TEST, got %s", da.StatusLabel)
		}
		if da.StatusClass != "test" {
			t.Errorf("expected test class, got %s", da.StatusClass)
		}
	})

	t.Run("manual mode suffix", func(t *testing.T) {
		e := base
		e.Mode = "manual"
		da := toDashboardAlert(e)
		if da.StatusLabel != "CRITICAL [MANUAL]" {
			t.Errorf("expected 'CRITICAL [MANUAL]', got %s", da.StatusLabel)
		}
	})

	t.Run("icinga error overrides class", func(t *testing.T) {
		e := base
		e.IcingaOK = false
		da := toDashboardAlert(e)
		if da.StatusClass != "error" {
			t.Errorf("expected error class, got %s", da.StatusClass)
		}
	})

	t.Run("error string overrides class", func(t *testing.T) {
		e := base
		e.Error = "something went wrong"
		da := toDashboardAlert(e)
		if da.StatusClass != "error" {
			t.Errorf("expected error class, got %s", da.StatusClass)
		}
	})
}

func TestIsAdmin(t *testing.T) {
	t.Run("no admin pass returns false", func(t *testing.T) {
		h := &DashboardHandler{AdminPass: ""}
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if h.isAdmin(req) {
			t.Error("expected false when admin pass not set")
		}
	})

	t.Run("no basic auth returns false", func(t *testing.T) {
		h := &DashboardHandler{AdminUser: "admin", AdminPass: "secret"}
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if h.isAdmin(req) {
			t.Error("expected false without basic auth")
		}
	})

	t.Run("correct credentials returns true", func(t *testing.T) {
		h := &DashboardHandler{AdminUser: "admin", AdminPass: "secret"}
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.SetBasicAuth("admin", "secret")
		if !h.isAdmin(req) {
			t.Error("expected true with correct credentials")
		}
	})

	t.Run("wrong password returns false", func(t *testing.T) {
		h := &DashboardHandler{AdminUser: "admin", AdminPass: "secret"}
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.SetBasicAuth("admin", "wrong")
		if h.isAdmin(req) {
			t.Error("expected false with wrong password")
		}
	})

	t.Run("RBAC user returns true", func(t *testing.T) {
		rbacMgr := rbac.New(nil)
		rbacMgr.AddUser(rbac.User{Username: "user", Password: "pass", Role: rbac.RoleViewer})
		h := &DashboardHandler{AdminUser: "admin", AdminPass: "secret", RBAC: rbacMgr}
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.SetBasicAuth("user", "pass")
		if !h.isAdmin(req) {
			t.Error("expected true for authenticated RBAC user")
		}
	})
}

func TestBuildSourceIPLists(t *testing.T) {
	stats := history.HistoryStats{
		BySourceIP: map[string]map[string]int{
			"grafana": {
				"10.0.0.1": 5,
				"10.0.0.2": 3,
				"10.0.0.3": 1,
			},
			"alertmanager": {
				"192.168.1.1": 2,
			},
		},
		BySourceIPLastSeen: map[string]map[string]time.Time{
			"grafana": {
				"10.0.0.1": time.Now(),
			},
		},
	}

	topIPs, lastIPs := buildSourceIPLists(stats)

	if len(topIPs) != 2 {
		t.Errorf("expected 2 sources in topIPs, got %d", len(topIPs))
	}
	if len(topIPs["grafana"]) != 3 {
		t.Errorf("expected 3 entries for grafana, got %d", len(topIPs["grafana"]))
	}
	// First entry should be the highest count
	if topIPs["grafana"][0].Count != 5 {
		t.Errorf("expected highest count first, got %d", topIPs["grafana"][0].Count)
	}
	if len(lastIPs) != 2 {
		t.Errorf("expected 2 sources in lastIPs, got %d", len(lastIPs))
	}
}

func TestHandleStats(t *testing.T) {
	h := &DashboardHandler{
		History: nil,
	}

	t.Run("non-GET method returns 405", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/status/beauty/stats", nil)
		rr := httptest.NewRecorder()
		h.HandleStats(rr, req)
		if rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("expected 405, got %d", rr.Code)
		}
	})

	t.Run("nil history returns 500", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/status/beauty/stats", nil)
		rr := httptest.NewRecorder()
		h.HandleStats(rr, req)
		if rr.Code != http.StatusInternalServerError {
			t.Errorf("expected 500, got %d", rr.Code)
		}
	})

	t.Run("returns valid stats", func(t *testing.T) {
		histLogger, _ := history.NewLogger(filepath.Join(t.TempDir(), "hist.jsonl"), 100)
		histLogger.Append(models.HistoryEntry{
			RequestID: "1",
			Mode:      "work",
			Action:    "firing",
		})
		h.History = histLogger
		h.StartedAt = time.Now()

		req := httptest.NewRequest(http.MethodGet, "/status/beauty/stats", nil)
		rr := httptest.NewRecorder()
		h.HandleStats(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", rr.Code)
		}
	})
}

func TestDashboard_ServeHTTP_StatsEndpoint(t *testing.T) {
	h := &DashboardHandler{
		History: nil,
	}

	req := httptest.NewRequest(http.MethodGet, "/status/beauty/stats", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	// should route to HandleStats -> returns 500 because history is nil
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", rr.Code)
	}
}

func TestDashboard_ServeHTTP_Errors(t *testing.T) {
	h := &DashboardHandler{
		History: nil,
	}

	t.Run("GET without history fails", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusInternalServerError {
			t.Errorf("expected 500 without history, got %d", rr.Code)
		}
	})

	t.Run("wrong auth logs metric", func(t *testing.T) {
		histLogger, _ := history.NewLogger(filepath.Join(t.TempDir(), "hist.jsonl"), 100)
		metric := metrics.NewCollector()
		h2 := &DashboardHandler{
			History:   histLogger,
			Metrics:   metric,
			AdminUser: "admin",
			AdminPass: "secret",
		}
		req := httptest.NewRequest(http.MethodGet, "/?admin=1", nil)
		req.SetBasicAuth("wrong", "password")
		rr := httptest.NewRecorder()
		h2.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", rr.Code)
		}
		stats := metric.Snapshot()
		if stats.FailedAuthTotal == 0 {
			t.Errorf("expected failed auth metric to increase")
		}
	})
}

func TestDashboard_ServeHTTP_LogoutAndAdmin(t *testing.T) {
	histLogger, _ := history.NewLogger(filepath.Join(t.TempDir(), "hist.jsonl"), 100)
	h := &DashboardHandler{
		History:   histLogger,
		AdminUser: "admin",
		AdminPass: "secret",
		Version:   "1.0",
		Cache:     cache.NewServiceCache(60),
		Targets:   map[string]config.TargetConfig{},
	}

	t.Run("logout logic sets 401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/?admin=1", nil)
		req.AddCookie(&http.Cookie{Name: "_logged_out", Value: "1"})
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("expected 401 for logout, got %d", rr.Code)
		}
	})

	t.Run("admin with valid auth", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/?admin=1", nil)
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", rr.Code)
		}
	})
}

func TestDashboard_ServeHTTP_AdminRendering(t *testing.T) {
	histLogger, _ := history.NewLogger(filepath.Join(t.TempDir(), "hist.jsonl"), 100)
	h := &DashboardHandler{
		History:   histLogger,
		AdminUser: "admin",
		AdminPass: "secret",
		Version:   "1.0",
		Targets:   map[string]config.TargetConfig{},
		Cache:     cache.NewServiceCache(60),
	}

	t.Run("admin with valid auth and rendering", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/?admin=1", nil)
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", rr.Code)
		}
	})

	t.Run("admin missing history causes 500", func(t *testing.T) {
		hNoHist := &DashboardHandler{
			History:   nil,
			AdminUser: "admin",
			AdminPass: "secret",
		}
		req := httptest.NewRequest(http.MethodGet, "/?admin=1", nil)
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		hNoHist.ServeHTTP(rr, req)
		if rr.Code != http.StatusInternalServerError {
			t.Errorf("expected 500, got %d", rr.Code)
		}
	})

	t.Run("viewer role returns 200 but not admin panel", func(t *testing.T) {
		rbacMgr := rbac.New(nil)
		rbacMgr.AddUser(rbac.User{Username: "user", Password: "pass", Role: rbac.RoleViewer})
		h.RBAC = rbacMgr

		req := httptest.NewRequest(http.MethodGet, "/?admin=1", nil)
		req.SetBasicAuth("user", "pass")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", rr.Code)
		}
	})
}

func TestDashboard_ServeHTTP_FullAdmin(t *testing.T) {
	histLogger, _ := history.NewLogger(filepath.Join(t.TempDir(), "hist.jsonl"), 100)

	c := cache.NewServiceCache(60)
	c.Register("host-b", "service-b")
	c.Freeze("host-b", "service-b", nil)

	metric := metrics.NewCollector()

	h := &DashboardHandler{
		History:   histLogger,
		AdminUser: "admin",
		AdminPass: "secret",
		Version:   "1.0",
		Targets:   map[string]config.TargetConfig{},
		Cache:     c,
		Metrics:   metric,
	}

	t.Run("admin with valid auth and rendering with cache and metrics", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/?admin=1", nil)
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", rr.Code)
		}
	})
}

package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"icinga-webhook-bridge/history"
	"icinga-webhook-bridge/models"
	"icinga-webhook-bridge/rbac"
)

func TestToDashboardAlert(t *testing.T) {
	base := models.HistoryEntry{
		RequestID: "req-1",
		SourceKey: "grafana",
		HostName:  "host-a",
		Mode:      "work",
		Action:    "firing",
		ExitStatus: 2,
		IcingaOK:  true,
		Error:     "",
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

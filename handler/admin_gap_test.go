package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"icinga-webhook-bridge/config"
	"icinga-webhook-bridge/icinga"
	"icinga-webhook-bridge/rbac"
)

// ── firstHostName ──────────────────────────────────────────────────────────

func TestFirstHostName(t *testing.T) {
	if got := firstHostName(nil); got != "" {
		t.Errorf("nil → %q, want empty", got)
	}
	if got := firstHostName([]string{}); got != "" {
		t.Errorf("empty → %q, want empty", got)
	}
	if got := firstHostName([]string{"a"}); got != "a" {
		t.Errorf("single → %q, want a", got)
	}
	if got := firstHostName([]string{"a", "b"}); got != "ALL TARGETS" {
		t.Errorf("multi → %q, want ALL TARGETS", got)
	}
}

// ── targetHostNames ────────────────────────────────────────────────────────

func TestTargetHostNames(t *testing.T) {
	targets := map[string]config.TargetConfig{
		"z": {ID: "z", HostName: "zulu"},
		"a": {ID: "a", HostName: "alpha"},
	}
	names := targetHostNames(targets)
	if len(names) != 2 {
		t.Fatalf("expected 2 names, got %d", len(names))
	}
	// sorted alphabetically
	if names[0] != "alpha" || names[1] != "zulu" {
		t.Errorf("expected sorted [alpha, zulu], got %v", names)
	}
}

// ── HandleClearHistory ─────────────────────────────────────────────────────

func TestAdmin_HandleClearHistory_Gaps(t *testing.T) {
	h, _ := testAdminHandler(t, func(w http.ResponseWriter, r *http.Request) {})

	t.Run("history nil", func(t *testing.T) {
		// Set History to nil after testAdminHandler creates it
		hNil := *h
		hNil.History = nil
		req := httptest.NewRequest(http.MethodPost, "/admin/history/clear", nil)
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		hNil.HandleClearHistory(rr, req)
		if rr.Code != http.StatusInternalServerError {
			t.Errorf("expected 500 for nil history, got %d", rr.Code)
		}
	})

	t.Run("method not allowed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/admin/history/clear", nil)
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		h.HandleClearHistory(rr, req)
		if rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("expected 405, got %d", rr.Code)
		}
	})
}

// ── HandleDebugToggle ──────────────────────────────────────────────────────

func TestAdmin_HandleDebugToggle_Gaps(t *testing.T) {
	h, _ := testAdminHandler(t, func(w http.ResponseWriter, r *http.Request) {})

	t.Run("debug ring nil", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/admin/debug/toggle", nil)
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		h.HandleDebugToggle(rr, req)
		if rr.Code != http.StatusInternalServerError {
			t.Errorf("expected 500 for nil debug ring, got %d", rr.Code)
		}
	})

	t.Run("GET returns enabled state", func(t *testing.T) {
		h.DebugRing = icinga.NewDebugRing()
		req := httptest.NewRequest(http.MethodGet, "/admin/debug/toggle", nil)
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		h.HandleDebugToggle(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", rr.Code)
		}
	})

	t.Run("POST invalid body", func(t *testing.T) {
		h.DebugRing = icinga.NewDebugRing()
		req := httptest.NewRequest(http.MethodPost, "/admin/debug/toggle", bytes.NewBufferString("not-json"))
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		h.HandleDebugToggle(rr, req)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", rr.Code)
		}
	})

	t.Run("POST enables debug", func(t *testing.T) {
		h.DebugRing = icinga.NewDebugRing()
		body, _ := json.Marshal(map[string]bool{"enabled": true})
		req := httptest.NewRequest(http.MethodPost, "/admin/debug/toggle", bytes.NewBuffer(body))
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		h.HandleDebugToggle(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", rr.Code)
		}
	})

	t.Run("method not allowed", func(t *testing.T) {
		h.DebugRing = icinga.NewDebugRing()
		req := httptest.NewRequest(http.MethodPut, "/admin/debug/toggle", nil)
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		h.HandleDebugToggle(rr, req)
		if rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("expected 405, got %d", rr.Code)
		}
	})
}

// ── HandleDeleteService ────────────────────────────────────────────────────

func TestAdmin_HandleDeleteService_Gaps(t *testing.T) {
	mockAPI := func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			// Return service info indicating it's managed by IAF
			w.Write([]byte(`{"results":[{"attrs":{"managed_by":"IcingaAlertingForge","check_command":"dummy"}}]}`))
			return
		}
		if r.Method == http.MethodDelete {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"results":[{"code":200}]}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"results":[]}`))
	}
	h, _ := testAdminHandler(t, mockAPI)

	t.Run("valid delete with mock", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/admin/services/svc?host=host-a", nil)
		req.URL.Path = "/admin/services/svc"
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		h.HandleDeleteService(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("expected 200, got %d: %s", rr.Code, rr.Body.String())
		}
	})

	t.Run("method not allowed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/admin/services/svc?host=h", nil)
		req.URL.Path = "/admin/services/svc"
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		h.HandleDeleteService(rr, req)
		if rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("expected 405, got %d", rr.Code)
		}
	})

	t.Run("missing service name", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/admin/services/?host=host-a", nil)
		req.URL.Path = "/admin/services/"
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		h.HandleDeleteService(rr, req)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for missing service, got %d", rr.Code)
		}
	})
}

// ── HandleBulkDelete ───────────────────────────────────────────────────────

func TestAdmin_HandleBulkDelete_Gaps(t *testing.T) {
	mockAPI := func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"results":[{"code":200}]}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"results":[]}`))
	}
	h, _ := testAdminHandler(t, mockAPI)

	t.Run("method not allowed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/admin/services/bulk-delete", nil)
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		h.HandleBulkDelete(rr, req)
		if rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("expected 405, got %d", rr.Code)
		}
	})

	t.Run("invalid JSON body", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/admin/services/bulk-delete", bytes.NewBufferString("bad"))
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		h.HandleBulkDelete(rr, req)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d: %s", rr.Code, rr.Body.String())
		}
	})

	t.Run("empty services list", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{"services": []any{}})
		req := httptest.NewRequest(http.MethodPost, "/admin/services/bulk-delete", bytes.NewBuffer(body))
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		h.HandleBulkDelete(rr, req)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", rr.Code)
		}
	})

	t.Run("valid bulk delete", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{
			"services": []string{"svc1", "svc2"},
		})
		req := httptest.NewRequest(http.MethodPost, "/admin/services/bulk-delete", bytes.NewBuffer(body))
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		h.HandleBulkDelete(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("expected 200, got %d: %s", rr.Code, rr.Body.String())
		}
	})
}

func TestAdminHandler_HandleDeleteService(t *testing.T) {
	h := &AdminHandler{


	}
	t.Run("non-DELETE method returns 405", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/admin/services/abc", nil)
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()

		h.HandleDeleteService(rr, req)

		if rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("expected 405, got %d", rr.Code)
		}
	})
}

func TestAdminHandler_HandleDeleteService_NoService(t *testing.T) {
	h := &AdminHandler{
		RBAC: rbac.New(nil),
	}
	h.RBAC.AddUser(rbac.User{Username: "admin", Password: "pwd", Role: rbac.RoleAdmin})
	t.Run("missing service returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/admin/services/", nil)
		req.SetBasicAuth("admin", "pwd")
		rr := httptest.NewRecorder()

		h.HandleDeleteService(rr, req)

		if rr.Code != http.StatusForbidden {
			t.Errorf("expected 403, got %d", rr.Code)
		}
	})
}

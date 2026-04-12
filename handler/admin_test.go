package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"icinga-webhook-bridge/cache"
	"icinga-webhook-bridge/config"
	"icinga-webhook-bridge/history"
	"icinga-webhook-bridge/icinga"
	"icinga-webhook-bridge/models"
	"icinga-webhook-bridge/rbac"
)

func testAdminHandler(t *testing.T, icingaHandler http.HandlerFunc) (*AdminHandler, *rbac.Manager) {
	t.Helper()
	icingaServer := httptest.NewTLSServer(icingaHandler)
	t.Cleanup(icingaServer.Close)

	tmpDir := t.TempDir()
	historyPath := filepath.Join(tmpDir, "history.jsonl")
	histLogger, _ := history.NewLogger(historyPath, 100)

	// RBAC Manager requires []rbac.User
	rbacMgr := rbac.New(nil)

	h := &AdminHandler{
		User:    "admin",
		Pass:    "secret",
		RBAC:    rbacMgr,
		History: histLogger,
		Cache:   cache.NewServiceCache(60),
		API: &icinga.APIClient{
			BaseURL:        icingaServer.URL,
			User:           "test",
			Pass:           "test",
			HTTPClient:     icingaServer.Client(),
			ConflictPolicy: icinga.ConflictPolicyWarn,
		},
		Targets: map[string]config.TargetConfig{
			"team-a": {ID: "team-a", HostName: "host-a"},
		},
	}
	return h, rbacMgr
}

func TestAdmin_Auth(t *testing.T) {
	h, rbacMgr := testAdminHandler(t, func(w http.ResponseWriter, r *http.Request) {})

	t.Run("unauthenticated", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/admin/services", nil)
		rr := httptest.NewRecorder()
		h.HandleListServices(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", rr.Code)
		}
	})

	t.Run("primary admin success", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/admin/services", nil)
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		h.HandleListServices(rr, req)
		// Should be 200 (or something other than 401/403)
		if rr.Code == http.StatusUnauthorized || rr.Code == http.StatusForbidden {
			t.Errorf("expected success, got %d", rr.Code)
		}
	})

	t.Run("RBAC user insufficient permissions", func(t *testing.T) {
		rbacMgr.AddUser(rbac.User{Username: "viewer", Password: "password", Role: rbac.RoleViewer})
		req := httptest.NewRequest(http.MethodPost, "/admin/history/clear", nil)
		req.SetBasicAuth("viewer", "password")
		rr := httptest.NewRecorder()
		h.HandleClearHistory(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Errorf("expected 403, got %d", rr.Code)
		}
	})

	t.Run("RBAC operator success", func(t *testing.T) {
		rbacMgr.AddUser(rbac.User{Username: "operator", Password: "password", Role: rbac.RoleOperator})
		// ClearHistory requires PermClearHistory, which Operator has.
		// Need to ensure file exists for Clear()
		h.History.Append(models.HistoryEntry{})

		req := httptest.NewRequest(http.MethodPost, "/admin/history/clear", nil)
		req.SetBasicAuth("operator", "password")
		rr := httptest.NewRecorder()
		h.HandleClearHistory(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", rr.Code)
		}
	})

	t.Run("RBAC operator forbidden for delete", func(t *testing.T) {
		// operator does NOT have PermDeleteService
		req := httptest.NewRequest(http.MethodDelete, "/admin/services/svc-1", nil)
		req.SetBasicAuth("operator", "password")
		rr := httptest.NewRecorder()
		h.HandleDeleteService(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Errorf("expected 403, got %d", rr.Code)
		}
	})
}

func TestAdmin_HandleDeleteService(t *testing.T) {
	deleteCalled := false
	h, _ := testAdminHandler(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1/objects/services/host-a!svc-1" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"results":[{"attrs":{"vars":{"managed_by":"IcingaAlertingForge"},"check_command":"dummy"}}]}`))
			return
		}
		if r.Method == http.MethodDelete && r.URL.Path == "/v1/objects/services/host-a!svc-1" {
			deleteCalled = true
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"results":[{"code":200}]}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})

	t.Run("valid service", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/admin/services/svc-1?host=host-a", nil)
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		h.HandleDeleteService(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", rr.Code)
		}
		if !deleteCalled {
			t.Error("API delete was not called")
		}
	})

	t.Run("invalid host", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/admin/services/svc-1?host=wrong-host", nil)
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		h.HandleDeleteService(rr, req)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", rr.Code)
		}
	})
}

func TestAdmin_HandleBulkDelete(t *testing.T) {
	deleteCount := 0
	h, _ := testAdminHandler(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"results":[{"attrs":{"vars":{"managed_by":"IcingaAlertingForge"},"check_command":"dummy"}}]}`))
			return
		}
		if r.Method == http.MethodDelete {
			deleteCount++
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"results":[{"code":200}]}`))
		}
	})

	payload := map[string]any{
		"services": []any{
			"svc-1",
			map[string]string{"host": "host-a", "service": "svc-2"},
		},
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/admin/services/bulk-delete", bytes.NewBuffer(body))
	req.SetBasicAuth("admin", "secret")
	rr := httptest.NewRecorder()

	h.HandleBulkDelete(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	if deleteCount != 2 {
		t.Errorf("expected 2 deletes, got %d", deleteCount)
	}
}

func TestAdmin_HandleClearHistory(t *testing.T) {
	h, _ := testAdminHandler(t, nil)

	t.Run("success", func(t *testing.T) {
		// history.Logger.Clear() requires the file to exist because it calls os.Truncate.
		// Append something to ensure file creation.
		h.History.Append(models.HistoryEntry{})

		req := httptest.NewRequest(http.MethodPost, "/admin/history/clear", nil)
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		h.HandleClearHistory(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("expected 200, got %d: %s", rr.Code, rr.Body.String())
		}
	})

	t.Run("history nil", func(t *testing.T) {
		h.History = nil
		req := httptest.NewRequest(http.MethodPost, "/admin/history/clear", nil)
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		h.HandleClearHistory(rr, req)
		if rr.Code != http.StatusInternalServerError {
			t.Errorf("expected 500, got %d", rr.Code)
		}
	})
}

func TestAdmin_HandleDebugToggle(t *testing.T) {
	h, _ := testAdminHandler(t, nil)
	h.DebugRing = icinga.NewDebugRing()

	t.Run("GET status", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/admin/debug/toggle", nil)
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		h.HandleDebugToggle(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", rr.Code)
		}
		var resp map[string]bool
		json.NewDecoder(rr.Body).Decode(&resp)
		if resp["enabled"] {
			t.Error("expected debug to be disabled initially")
		}
	})

	t.Run("POST enable", func(t *testing.T) {
		body, _ := json.Marshal(map[string]bool{"enabled": true})
		req := httptest.NewRequest(http.MethodPost, "/admin/debug/toggle", bytes.NewBuffer(body))
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		h.HandleDebugToggle(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", rr.Code)
		}
		if !h.DebugRing.Enabled() {
			t.Error("debug ring should be enabled")
		}
	})
}

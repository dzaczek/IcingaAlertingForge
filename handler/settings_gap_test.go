package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"icinga-webhook-bridge/configstore"
	"icinga-webhook-bridge/metrics"
	"icinga-webhook-bridge/rbac"
)

func TestSettings_checkAuth_Gaps(t *testing.T) {
	store, _ := configstore.New(t.TempDir()+"/cfg.json", "test-key")
	store.Update(configstore.StoredConfig{})

	t.Run("wrong password for primary admin", func(t *testing.T) {
		h := &SettingsHandler{User: "admin", Pass: "secret", Store: store}
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.SetBasicAuth("admin", "wrong")
		rr := httptest.NewRecorder()
		if h.checkAuth(rr, req) {
			t.Error("expected auth failure with wrong password")
		}
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", rr.Code)
		}
	})

	t.Run("RBAC user with manage.config permission", func(t *testing.T) {
		rbacMgr := rbac.New(nil)
		rbacMgr.AddUser(rbac.User{Username: "rbacadmin", Password: "admin-pass", Role: rbac.RoleAdmin})
		h := &SettingsHandler{User: "primary", Pass: "primarypass", Store: store, RBAC: rbacMgr}
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.SetBasicAuth("rbacadmin", "admin-pass")
		rr := httptest.NewRecorder()
		if !h.checkAuth(rr, req) {
			t.Error("expected auth success for rbacadmin with manage.config")
		}
	})

	t.Run("RBAC user without manage.config permission", func(t *testing.T) {
		rbacMgr := rbac.New(nil)
		rbacMgr.AddUser(rbac.User{Username: "viewer", Password: "view-pass", Role: rbac.RoleViewer})
		h := &SettingsHandler{User: "admin", Pass: "secret", Store: store, RBAC: rbacMgr}
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.SetBasicAuth("viewer", "view-pass")
		rr := httptest.NewRecorder()
		if h.checkAuth(rr, req) {
			t.Error("expected auth failure for viewer without manage.config")
		}
		if rr.Code != http.StatusForbidden {
			t.Errorf("expected 403, got %d", rr.Code)
		}
	})

	t.Run("RBAC user with wrong password", func(t *testing.T) {
		rbacMgr := rbac.New(nil)
		rbacMgr.AddUser(rbac.User{Username: "viewer", Password: "correct", Role: rbac.RoleViewer})
		h := &SettingsHandler{User: "admin", Pass: "secret", Store: store, RBAC: rbacMgr}
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.SetBasicAuth("viewer", "wrong")
		rr := httptest.NewRecorder()
		if h.checkAuth(rr, req) {
			t.Error("expected auth failure with wrong RBAC password")
		}
	})

	t.Run("RBAC nil, wrong primary and no fallback", func(t *testing.T) {
		h := &SettingsHandler{User: "admin", Pass: "secret", Store: store, RBAC: nil}
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.SetBasicAuth("admin", "wrong")
		rr := httptest.NewRecorder()
		if h.checkAuth(rr, req) {
			t.Error("expected auth failure")
		}
	})

	t.Run("with metrics collector", func(t *testing.T) {
		mc := metrics.NewCollector()
		h := &SettingsHandler{User: "admin", Pass: "secret", Store: store, Metrics: mc}
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.SetBasicAuth("admin", "wrong")
		rr := httptest.NewRecorder()
		if h.checkAuth(rr, req) {
			t.Error("expected auth failure with metrics")
		}
		// auth failure should be recorded
	})
}

func TestSettings_HandleGetSettings_Gaps(t *testing.T) {
	t.Run("no auth header returns 401", func(t *testing.T) {
		h := testSettingsHandler(t)
		req := httptest.NewRequest(http.MethodGet, "/admin/settings", nil)
		rr := httptest.NewRecorder()
		h.HandleGetSettings(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", rr.Code)
		}
	})

	t.Run("method not allowed", func(t *testing.T) {
		h := testSettingsHandler(t)
		req := httptest.NewRequest(http.MethodPost, "/admin/settings", nil)
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		h.HandleGetSettings(rr, req)
		if rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("expected 405, got %d", rr.Code)
		}
	})
}

func TestSettings_HandlePatchSettings_Gaps(t *testing.T) {
	h := testSettingsHandler(t)

	t.Run("method not allowed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/admin/settings", nil)
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		h.HandlePatchSettings(rr, req)
		if rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("expected 405, got %d", rr.Code)
		}
	})

	t.Run("no auth header", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPatch, "/admin/settings", nil)
		rr := httptest.NewRecorder()
		h.HandlePatchSettings(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", rr.Code)
		}
	})
}

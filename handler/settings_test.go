package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"icinga-webhook-bridge/configstore"
)

func testSettingsHandler(t *testing.T) *SettingsHandler {
	t.Helper()
	tmpDir := t.TempDir()
	store, _ := configstore.New(filepath.Join(tmpDir, "config.json"), "test-key")
	store.Update(configstore.StoredConfig{
		Icinga2Pass: "secret",
		Targets: []configstore.TargetStore{
			{ID: "t1", HostName: "host1", APIKeys: []string{"key1"}},
		},
	})

	return &SettingsHandler{
		Store: store,
		User:  "admin",
		Pass:  "secret",
	}
}

func TestSettings_HandleGetSettings(t *testing.T) {
	h := testSettingsHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/admin/settings", nil)
	req.SetBasicAuth("admin", "secret")
	rr := httptest.NewRecorder()

	h.HandleGetSettings(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}

	var sc configstore.StoredConfig
	json.NewDecoder(rr.Body).Decode(&sc)
	if sc.Icinga2Pass != "***" {
		t.Errorf("expected masked password, got %s", sc.Icinga2Pass)
	}
	if sc.Targets[0].APIKeys[0] != "***" {
		t.Error("expected masked API key")
	}
}

func TestSettings_HandlePatchSettings(t *testing.T) {
	h := testSettingsHandler(t)
	patch := map[string]any{
		"icinga2_host": "http://new-icinga",
		"icinga2_pass": "new-secret",
	}
	body, _ := json.Marshal(patch)
	req := httptest.NewRequest(http.MethodPatch, "/admin/settings", bytes.NewBuffer(body))
	req.SetBasicAuth("admin", "secret")
	rr := httptest.NewRecorder()

	h.HandlePatchSettings(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}

	updated := h.Store.Get()
	if updated.Icinga2Host != "http://new-icinga" {
		t.Errorf("expected host update, got %s", updated.Icinga2Host)
	}
	if updated.Icinga2Pass != "new-secret" {
		t.Errorf("expected pass update, got %s", updated.Icinga2Pass)
	}
}

func TestSettings_HandleAddTarget(t *testing.T) {
	h := testSettingsHandler(t)

	t.Run("success", func(t *testing.T) {
		target := map[string]any{
			"id":        "t2",
			"host_name": "host2",
		}
		body, _ := json.Marshal(target)
		req := httptest.NewRequest(http.MethodPost, "/admin/settings/targets", bytes.NewBuffer(body))
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()

		h.HandleAddTarget(rr, req)

		if rr.Code != http.StatusCreated {
			t.Errorf("expected 201, got %d", rr.Code)
		}
		if len(h.Store.Get().Targets) != 2 {
			t.Error("target was not added to store")
		}
	})

	t.Run("duplicate ID", func(t *testing.T) {
		target := map[string]any{
			"id":        "t1",
			"host_name": "host1-dup",
		}
		body, _ := json.Marshal(target)
		req := httptest.NewRequest(http.MethodPost, "/admin/settings/targets", bytes.NewBuffer(body))
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()

		h.HandleAddTarget(rr, req)

		if rr.Code != http.StatusConflict {
			t.Errorf("expected 409, got %d", rr.Code)
		}
	})
}

func TestSettings_HandleDeleteTarget(t *testing.T) {
	h := testSettingsHandler(t)

	t.Run("success", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/admin/settings/targets/t1", nil)
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()

		h.HandleDeleteTarget(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", rr.Code)
		}
		if len(h.Store.Get().Targets) != 0 {
			t.Error("target was not deleted")
		}
	})

	t.Run("non-existent", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/admin/settings/targets/missing", nil)
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()

		h.HandleDeleteTarget(rr, req)

		if rr.Code != http.StatusNotFound {
			t.Errorf("expected 404, got %d", rr.Code)
		}
	})
}

func TestSettings_HandleImportConfig(t *testing.T) {
	h := testSettingsHandler(t)
	importData := map[string]any{
		"meta": map[string]int{"version": 1},
		"config": map[string]any{
			"icinga2_pass": "***",
			"targets": []any{
				map[string]any{"id": "new-t", "host_name": "new-h", "api_keys": []string{"***"}},
			},
		},
	}
	body, _ := json.Marshal(importData)
	req := httptest.NewRequest(http.MethodPost, "/admin/settings/import", bytes.NewBuffer(body))
	req.SetBasicAuth("admin", "secret")
	rr := httptest.NewRecorder()

	h.HandleImportConfig(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	updated := h.Store.Get()
	if updated.Icinga2Pass != "secret" {
		t.Error("secret was not preserved during import")
	}
}

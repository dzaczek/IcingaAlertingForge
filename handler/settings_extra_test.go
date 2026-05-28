package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"icinga-webhook-bridge/configstore"
)

func TestSettings_HandleGenerateKey(t *testing.T) {
	h := testSettingsHandler(t)

	t.Run("generates key for existing target", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/admin/settings/targets/t1/generate-key", nil)
		req.URL.Path = "/admin/settings/targets/t1/generate-key"
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		h.HandleGenerateKey(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("expected 200, got %d: %s", rr.Code, rr.Body.String())
		}
		var resp map[string]string
		json.NewDecoder(rr.Body).Decode(&resp)
		if resp["api_key"] == "" {
			t.Error("expected non-empty api_key in response")
		}
	})

	t.Run("target not found", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/admin/settings/targets/nonexistent/generate-key", nil)
		req.URL.Path = "/admin/settings/targets/nonexistent/generate-key"
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		h.HandleGenerateKey(rr, req)
		if rr.Code != http.StatusNotFound {
			t.Errorf("expected 404, got %d", rr.Code)
		}
	})

	t.Run("method not allowed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/admin/settings/targets/t1/generate-key", nil)
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		h.HandleGenerateKey(rr, req)
		if rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("expected 405, got %d", rr.Code)
		}
	})
}

func TestSettings_HandleRevealKeys(t *testing.T) {
	h := testSettingsHandler(t)

	t.Run("reveals keys for existing target", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/admin/settings/targets/t1/reveal-keys", nil)
		req.URL.Path = "/admin/settings/targets/t1/reveal-keys"
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		h.HandleRevealKeys(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("expected 200, got %d: %s", rr.Code, rr.Body.String())
		}
		var resp map[string]any
		json.NewDecoder(rr.Body).Decode(&resp)
		keys, ok := resp["api_keys"].([]any)
		if !ok || len(keys) == 0 {
			t.Error("expected api_keys array in response")
		}
	})

	t.Run("target not found", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/admin/settings/targets/nope/reveal-keys", nil)
		req.URL.Path = "/admin/settings/targets/nope/reveal-keys"
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		h.HandleRevealKeys(rr, req)
		if rr.Code != http.StatusNotFound {
			t.Errorf("expected 404, got %d", rr.Code)
		}
	})

	t.Run("method not allowed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/admin/settings/targets/t1/reveal-keys", nil)
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		h.HandleRevealKeys(rr, req)
		if rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("expected 405, got %d", rr.Code)
		}
	})
}

func TestSettings_HandleExportConfig(t *testing.T) {
	h := testSettingsHandler(t)

	t.Run("exports config as JSON", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/admin/settings/export", nil)
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		h.HandleExportConfig(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("expected 200, got %d: %s", rr.Code, rr.Body.String())
		}
		if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
			t.Errorf("expected application/json, got %q", ct)
		}
		if cd := rr.Header().Get("Content-Disposition"); cd == "" {
			t.Error("expected Content-Disposition header")
		}
		// Verify valid JSON
		var exported map[string]json.RawMessage
		if err := json.NewDecoder(rr.Body).Decode(&exported); err != nil {
			t.Fatalf("export produced invalid JSON: %v", err)
		}
	})

	t.Run("method not allowed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/admin/settings/export", nil)
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		h.HandleExportConfig(rr, req)
		if rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("expected 405, got %d", rr.Code)
		}
	})
}

func TestSettings_HandleTestIcinga_NoHost(t *testing.T) {
	h := testSettingsHandler(t)
	// store has no icinga2_host
	h.Store.Update(configstore.StoredConfig{})

	req := httptest.NewRequest(http.MethodPost, "/admin/settings/test-icinga", nil)
	req.SetBasicAuth("admin", "secret")
	rr := httptest.NewRecorder()
	h.HandleTestIcinga(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

func TestSettings_HandleTestIcinga_ConnectionError(t *testing.T) {
	h := testSettingsHandler(t)
	h.Store.Update(configstore.StoredConfig{
		Icinga2Host: "https://127.0.0.1:19999", // nothing listening
		Icinga2User: "root",
		Icinga2Pass: "secret",
	})

	req := httptest.NewRequest(http.MethodPost, "/admin/settings/test-icinga", nil)
	req.SetBasicAuth("admin", "secret")
	rr := httptest.NewRecorder()
	h.HandleTestIcinga(rr, req)
	// Should return 200 with status=error (soft-fail for connectivity issues)
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 (soft-fail), got %d", rr.Code)
	}
	var resp map[string]string
	json.NewDecoder(rr.Body).Decode(&resp)
	if resp["status"] != "error" {
		t.Errorf("expected status=error, got %q", resp["status"])
	}
}

func TestSettings_HandleDeleteKey(t *testing.T) {
	h := testSettingsHandler(t)

	// ensure t1 has an API key to delete
	h.Store.Update(configstore.StoredConfig{
		Targets: []configstore.TargetStore{
			{ID: "t1", HostName: "host1", APIKeys: []string{"key1"}},
		},
	})

	t.Run("deletes key for existing target", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/admin/settings/targets/t1/keys/0", nil)
		req.URL.Path = "/admin/settings/targets/t1/keys/0"
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		h.HandleDeleteKey(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("expected 200, got %d: %s", rr.Code, rr.Body.String())
		}

		cfg := h.Store.Get()
		if len(cfg.Targets[0].APIKeys) != 0 {
			t.Errorf("expected 0 api keys, got %d", len(cfg.Targets[0].APIKeys))
		}
	})

	t.Run("target not found", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/admin/settings/targets/nonexistent/keys/0", nil)
		req.URL.Path = "/admin/settings/targets/nonexistent/keys/0"
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		h.HandleDeleteKey(rr, req)
		if rr.Code != http.StatusNotFound {
			t.Errorf("expected 404, got %d", rr.Code)
		}
	})

	t.Run("invalid index", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/admin/settings/targets/t1/keys/99", nil)
		req.URL.Path = "/admin/settings/targets/t1/keys/99"
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		h.HandleDeleteKey(rr, req)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", rr.Code)
		}
	})

	t.Run("invalid path", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/admin/settings/targets/t1/keys/invalid", nil)
		req.URL.Path = "/admin/settings/targets/t1/keys/invalid"
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		h.HandleDeleteKey(rr, req)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", rr.Code)
		}
	})

	t.Run("method not allowed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/admin/settings/targets/t1/keys/0", nil)
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		h.HandleDeleteKey(rr, req)
		if rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("expected 405, got %d", rr.Code)
		}
	})
}

package handler

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"icinga-webhook-bridge/config"
	"icinga-webhook-bridge/configstore"
)

func TestSettings_reload(t *testing.T) {
	t.Run("nil OnReload returns immediately", func(t *testing.T) {
		h := testSettingsHandler(t)
		// OnReload is nil by default, reload should not panic
		h.reload()
	})

	t.Run("OnReload is called with config", func(t *testing.T) {
		tmpDir := t.TempDir()
		store, _ := configstore.New(filepath.Join(tmpDir, "config.json"), "test-key")
		store.Update(configstore.StoredConfig{
			Icinga2Host: "http://icinga.example.com",
			Targets: []configstore.TargetStore{
				{ID: "t1", HostName: "host1", APIKeys: []string{"key1"}},
			},
		})

		called := false
		h := &SettingsHandler{
			Store: store,
			User:  "admin",
			Pass:  "secret",
			OnReload: func(cfg *config.Config) {
				called = true
				if cfg.Icinga2Host != "http://icinga.example.com" {
					t.Errorf("unexpected host: %s", cfg.Icinga2Host)
				}
				if len(cfg.Targets) != 1 {
					t.Errorf("expected 1 target, got %d", len(cfg.Targets))
				}
			},
		}

		h.reload()
		if !called {
			t.Error("expected OnReload to be called")
		}
	})

	t.Run("check auth with reload configured", func(t *testing.T) {
		tmpDir := t.TempDir()
		store, _ := configstore.New(filepath.Join(tmpDir, "config.json"), "test-key")
		store.Update(configstore.StoredConfig{})

		h := &SettingsHandler{
			Store: store,
			User:  "admin",
			Pass:  "secret",
			OnReload: func(cfg *config.Config) {
				// no-op
			},
		}

		req := httptest.NewRequest(http.MethodGet, "/admin/settings", nil)
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		h.HandleGetSettings(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", rr.Code)
		}
	})
}

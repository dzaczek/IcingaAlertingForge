package configstore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"icinga-webhook-bridge/config"
)

func TestExists(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	s, _ := New(configPath, "key")

	if s.Exists() {
		t.Error("Exists should return false before Save")
	}

	s.Update(StoredConfig{Version: 1})
	if err := s.Save(); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	if !s.Exists() {
		t.Error("Exists should return true after Save")
	}
}

func TestSetUsersGetUsers(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	s, _ := New(configPath, "key")
	s.Update(StoredConfig{Version: 1})

	users := []StoredUser{
		{Username: "alice", Password: "secret", Role: "admin"},
		{Username: "bob", Password: "pass", Role: "viewer"},
	}

	if err := s.SetUsers(users); err != nil {
		t.Fatalf("SetUsers failed: %v", err)
	}

	got := s.GetUsers()
	if len(got) != 2 {
		t.Fatalf("expected 2 users, got %d", len(got))
	}
	if got[0].Username != "alice" || got[1].Username != "bob" {
		t.Errorf("unexpected users: %+v", got)
	}
}

func TestGetUsers_NilConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	s, _ := New(configPath, "key")
	// current is nil — no Load/Update called
	got := s.GetUsers()
	if got != nil {
		t.Errorf("expected nil users for empty store, got %v", got)
	}
}

func TestMigrateFromEnv(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	s, _ := New(configPath, "key")

	cfg := &config.Config{
		Icinga2Host:       "icinga.example.com",
		Icinga2User:       "root",
		Icinga2Pass:       "mysecret",
		HistoryFile:       "/tmp/history.jsonl",
		HistoryMaxEntries: 100,
		CacheTTLMinutes:   30,
		LogLevel:          "info",
		Targets: map[string]config.TargetConfig{
			"t1": {ID: "t1", HostName: "host-1", Source: "grafana"},
		},
		WebhookRoutes: map[string]config.WebhookRoute{
			"api-key-1": {Source: "grafana", TargetID: "t1"},
		},
	}

	if err := s.MigrateFromEnv(cfg); err != nil {
		t.Fatalf("MigrateFromEnv failed: %v", err)
	}

	got := s.Get()
	if got.Icinga2Host != "icinga.example.com" {
		t.Errorf("unexpected icinga2_host: %s", got.Icinga2Host)
	}
	if len(got.Targets) != 1 {
		t.Errorf("expected 1 target, got %d", len(got.Targets))
	}
	if len(got.Targets[0].APIKeys) != 1 || got.Targets[0].APIKeys[0] != "api-key-1" {
		t.Errorf("unexpected api keys: %v", got.Targets[0].APIKeys)
	}
}

func TestToConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	s, _ := New(configPath, "key")

	s.Update(StoredConfig{
		Version:     1,
		Icinga2Host: "icinga.local",
		Icinga2User: "root",
		Icinga2Pass: "secret",
		Targets: []TargetStore{
			{ID: "t1", HostName: "host-1", Source: "grafana", APIKeys: []string{"key1"}},
		},
	})

	cfg := s.ToConfig("8080", "0.0.0.0")
	if cfg == nil {
		t.Fatal("ToConfig returned nil")
	}
	if cfg.Icinga2Host != "icinga.local" {
		t.Errorf("unexpected icinga2_host: %s", cfg.Icinga2Host)
	}
	if len(cfg.Targets) != 1 {
		t.Errorf("expected 1 target, got %d", len(cfg.Targets))
	}
	if _, ok := cfg.WebhookRoutes["key1"]; !ok {
		t.Error("expected webhook route for key1")
	}
}

func TestToConfig_NilConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	s, _ := New(configPath, "key")
	// no Update — current is nil
	cfg := s.ToConfig("8080", "0.0.0.0")
	if cfg != nil {
		t.Error("expected nil config when store is empty")
	}
}

func TestExport(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	s, _ := New(configPath, "key")
	s.Update(StoredConfig{Version: 1, Icinga2Host: "icinga.local"})

	data, err := s.Export()
	if err != nil {
		t.Fatalf("Export failed: %v", err)
	}

	var exported map[string]json.RawMessage
	if err := json.Unmarshal(data, &exported); err != nil {
		t.Fatalf("Export produced invalid JSON: %v", err)
	}
	if _, ok := exported["meta"]; !ok {
		t.Error("expected meta key in export")
	}
	if _, ok := exported["config"]; !ok {
		t.Error("expected config key in export")
	}
}

func TestLoadOrCreateKey_ExistingKeyFile(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")

	// First creation generates the key file
	s1, err := New(configPath, "")
	if err != nil {
		t.Fatalf("first New failed: %v", err)
	}
	key1 := s1.encKey

	// Second creation should reuse the same key
	s2, err := New(configPath, "")
	if err != nil {
		t.Fatalf("second New failed: %v", err)
	}

	for i, b := range key1 {
		if s2.encKey[i] != b {
			t.Error("second store should load the same key from file")
			break
		}
	}
}

func TestLoadOrCreateKey_CorruptKeyFile(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")

	// Create a key file with invalid content
	keyPath := filepath.Join(tmpDir, ".config.key")
	os.WriteFile(keyPath, []byte("not-valid-hex!!!"), 0600)

	// Should generate a new key, not fail
	s, err := New(configPath, "")
	if err != nil {
		t.Fatalf("New should succeed even with corrupt key file: %v", err)
	}
	if len(s.encKey) != 32 {
		t.Errorf("expected 32-byte key, got %d", len(s.encKey))
	}
}

func TestStore_Save_CreateTempFailure(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	s, err := New(configPath, "test-key")
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	// Must initialize s.current so Save() doesn't panic
	s.Update(StoredConfig{Version: 1})

	// Use an invalid path to force os.CreateTemp to fail.
	// Using a null byte or an incredibly long path usually triggers a failure.
	s.filePath = string([]byte{0}) + "invalid-path/config.json"

	err = s.Save()
	if err == nil {
		t.Error("expected error when saving to invalid directory")
	}
}

func TestStore_Exists(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	s, err := New(configPath, "test-key")
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	if s.Exists() {
		t.Error("expected Exists() to be false before saving")
	}

	s.Update(StoredConfig{Version: 1})
	if err := s.Save(); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	if !s.Exists() {
		t.Error("expected Exists() to be true after saving")
	}
}

func TestStore_SetUsersGetUsers(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	s, err := New(configPath, "test-key")
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	s.Update(StoredConfig{Version: 1})

	users := []StoredUser{{Username: "testuser", Password: "testpass", Role: "admin"}}
	if err := s.SetUsers(users); err != nil {
		t.Fatalf("SetUsers failed: %v", err)
	}

	retrieved := s.GetUsers()
	if len(retrieved) != 1 {
		t.Fatalf("expected 1 user, got %d", len(retrieved))
	}
	if retrieved[0].Username != "testuser" {
		t.Errorf("expected testuser, got %s", retrieved[0].Username)
	}
}

func TestStore_Export(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	s, err := New(configPath, "test-key")
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	s.Update(StoredConfig{Icinga2Pass: "secret", Targets: []TargetStore{{ID: "t1"}}})
	if err := s.Save(); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	exported, err := s.Export()
	if err != nil {
		t.Fatalf("Export failed: %v", err)
	}
	if string(exported) == "" {
		t.Error("expected non-empty export")
	}
}

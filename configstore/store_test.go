package configstore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestNew(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")

	t.Run("with empty encryption key", func(t *testing.T) {
		s, err := New(configPath, "")
		if err != nil {
			t.Fatalf("New failed: %v", err)
		}
		if len(s.encKey) != 32 {
			t.Errorf("expected 32 byte key, got %d", len(s.encKey))
		}
		// Check key file was created
		if _, err := os.Stat(s.keyPath); os.IsNotExist(err) {
			t.Error("key file was not created")
		}
	})

	t.Run("with provided encryption key", func(t *testing.T) {
		key := "super-secret-key"
		s, err := New(configPath, key)
		if err != nil {
			t.Fatalf("New failed: %v", err)
		}
		if len(s.encKey) != 32 {
			t.Errorf("expected 32 byte key (hashed), got %d", len(s.encKey))
		}
	})
}

func TestSaveLoad(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	s, _ := New(configPath, "test-key")

	original := StoredConfig{
		Icinga2Pass: "secret-password",
		Targets: []TargetStore{
			{
				ID:      "t1",
				APIKeys: []string{"api-key-1"},
			},
		},
		Users: []StoredUser{
			{Username: "admin", Password: "admin-password"},
		},
	}

	s.Update(original)
	if err := s.Save(); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Read file directly to verify encryption
	data, _ := os.ReadFile(configPath)
	var raw map[string]interface{}
	json.Unmarshal(data, &raw)
	if raw["icinga2_pass"] == "secret-password" {
		t.Error("icinga2_pass was not encrypted on disk")
	}

	// Create new store instance to test loading
	s2, _ := New(configPath, "test-key")
	if err := s2.Load(); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	loaded := s2.Get()
	if loaded.Icinga2Pass != original.Icinga2Pass {
		t.Errorf("expected icinga2_pass %s, got %s", original.Icinga2Pass, loaded.Icinga2Pass)
	}
	if loaded.Targets[0].APIKeys[0] != original.Targets[0].APIKeys[0] {
		t.Error("API key decryption failed")
	}
	if loaded.Users[0].Password != original.Users[0].Password {
		t.Error("User password decryption failed")
	}
}

func TestLoadError(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")

	t.Run("missing file", func(t *testing.T) {
		s, _ := New(configPath, "key")
		err := s.Load()
		if err == nil {
			t.Error("expected error for missing file, got nil")
		}
	})

	t.Run("corrupted JSON", func(t *testing.T) {
		s, _ := New(configPath, "key")
		os.WriteFile(configPath, []byte("{invalid json}"), 0600)
		err := s.Load()
		if err == nil {
			t.Error("expected error for corrupted JSON, got nil")
		}
	})

	t.Run("tampered ciphertext", func(t *testing.T) {
		s, _ := New(configPath, "key")
		sc := StoredConfig{Icinga2Pass: "secret"}
		s.Update(sc)
		s.Save()

		// Read, tamper, and write back
		data, _ := os.ReadFile(configPath)
		var raw map[string]interface{}
		json.Unmarshal(data, &raw)
		raw["icinga2_pass"] = raw["icinga2_pass"].(string) + "tampered"
		newData, _ := json.Marshal(raw)
		os.WriteFile(configPath, newData, 0600)

		err := s.Load()
		if err == nil {
			t.Error("expected error for tampered ciphertext, got nil")
		}
	})
}

func TestConcurrency(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	s, _ := New(configPath, "key")
	s.Update(StoredConfig{Version: 1})

	const iterations = 100
	var wg sync.WaitGroup
	wg.Add(3)

	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			s.Save()
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			s.Get()
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			sc := s.Get()
			sc.Icinga2Host = fmt.Sprintf("host-%d", i)
			s.Update(sc)
		}
	}()

	wg.Wait()
}

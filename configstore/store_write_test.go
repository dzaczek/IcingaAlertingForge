package configstore

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveCreateTempError(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	s, _ := New(configPath, "test-key")

	s.Update(StoredConfig{Version: 1})

	// Make dir read-only to force CreateTemp error
	os.Chmod(tmpDir, 0500)

	err := s.Save()
	if err == nil {
		t.Fatal("expected error from Save due to read-only dir, got nil")
	}

	// Restore permissions
	os.Chmod(tmpDir, 0700)
}

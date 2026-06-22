package configstore

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSave_TempFileErrors(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	s, _ := New(configPath, "test-key")

	s.Update(StoredConfig{Version: 1})

	// Test 1: os.CreateTemp failure by providing a nonexistent directory
	badPath := filepath.Join(tmpDir, "nonexistent_dir", "config.json")
	s.filePath = badPath
	err := s.Save()
	if err == nil {
		t.Error("expected error when CreateTemp fails (bad directory), got nil")
	} else if !strings.Contains(err.Error(), "create temp file") {
		t.Errorf("expected 'create temp file' error, got %v", err)
	}
}

package history

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLogger_RotationErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rot-err.jsonl")
	l, err := NewLogger(path, 2)
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}

	// Trigger open error
	l.rotateEvery = 1
	os.Remove(path) // simulate file deleted
    l.entryCount.Store(3)
	l.rotateLockedInline() // Should hit open err

    // Trigger temp file create err
    path2 := filepath.Join(dir, "rot-err2.jsonl")
	l2, _ := NewLogger(path2, 2)
    l2.rotateEvery = 1
    os.WriteFile(path2, []byte("line1\nline2\nline3\n"), 0600)
    l2.entryCount.Store(3)

    // mess up dir permissions
    os.Chmod(dir, 0500)
    l2.rotateLockedInline() // should hit os.CreateTemp err
    os.Chmod(dir, 0700)
}

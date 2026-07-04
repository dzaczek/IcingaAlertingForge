package history

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestLoggerRotateErrorHandling(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "history.jsonl")
	l, err := NewLogger(path, 2)
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}

	for i := 0; i < 5; i++ {
		l.Append(sampleEntry("svc", "work", "firing", fmt.Sprintf("src-%d", i)))
	}

	// Make the target directory read-only to force an error in CreateTemp
	os.Chmod(tmpDir, 0500)

	// Test the rotation logic which will fail to create temp file
	l.rotateLockedInline()

	// Restore permissions to allow cleanup
	os.Chmod(tmpDir, 0700)
}

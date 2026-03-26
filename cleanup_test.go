package smtp

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestCleanupTempFiles(t *testing.T) {
	tmpDir := t.TempDir()
	log, _ := zap.NewDevelopment()

	p := &Plugin{
		cfg: &Config{
			AttachmentStorage: AttachmentConfig{
				Mode:         "tempfile",
				TempDir:      tmpDir,
				CleanupAfter: 50 * time.Millisecond,
			},
		},
		log: log,
	}

	// Create old files (matching pattern)
	oldFile := filepath.Join(tmpDir, "smtp-att-old-file.txt")
	os.WriteFile(oldFile, []byte("old"), 0644)
	// Backdate the file
	past := time.Now().Add(-1 * time.Hour)
	os.Chtimes(oldFile, past, past)

	// Create recent file (matching pattern)
	recentFile := filepath.Join(tmpDir, "smtp-att-recent-file.txt")
	os.WriteFile(recentFile, []byte("recent"), 0644)

	// Create non-matching file
	otherFile := filepath.Join(tmpDir, "other-file.txt")
	os.WriteFile(otherFile, []byte("other"), 0644)
	os.Chtimes(otherFile, past, past)

	p.cleanupTempFiles()

	// Old matching file should be removed
	if _, err := os.Stat(oldFile); !os.IsNotExist(err) {
		t.Error("old smtp-att file should have been removed")
	}

	// Recent matching file should remain
	if _, err := os.Stat(recentFile); err != nil {
		t.Error("recent smtp-att file should NOT have been removed")
	}

	// Non-matching file should remain
	if _, err := os.Stat(otherFile); err != nil {
		t.Error("non-matching file should NOT have been removed")
	}
}

func TestCleanupTempFiles_NonexistentDir(t *testing.T) {
	log, _ := zap.NewDevelopment()
	p := &Plugin{
		cfg: &Config{
			AttachmentStorage: AttachmentConfig{
				Mode:         "tempfile",
				TempDir:      "/nonexistent/path",
				CleanupAfter: time.Hour,
			},
		},
		log: log,
	}

	// Should not panic
	p.cleanupTempFiles()
}

func TestStartCleanupRoutine_SkipsMemoryMode(t *testing.T) {
	log, _ := zap.NewDevelopment()
	p := &Plugin{
		cfg: &Config{
			AttachmentStorage: AttachmentConfig{
				Mode: "memory",
			},
		},
		log: log,
	}

	// Should not start goroutine or panic
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.startCleanupRoutine(ctx)
}

func TestStartCleanupRoutine_StopsOnCancel(t *testing.T) {
	tmpDir := t.TempDir()
	log, _ := zap.NewDevelopment()

	p := &Plugin{
		cfg: &Config{
			AttachmentStorage: AttachmentConfig{
				Mode:         "tempfile",
				TempDir:      tmpDir,
				CleanupAfter: 10 * time.Millisecond,
			},
		},
		log: log,
	}

	ctx, cancel := context.WithCancel(context.Background())
	p.startCleanupRoutine(ctx)

	// Let it tick at least once
	time.Sleep(30 * time.Millisecond)

	// Cancel should stop the goroutine
	cancel()

	// Give goroutine time to exit
	time.Sleep(20 * time.Millisecond)

	// Create a file after cancel - it should NOT be cleaned up
	testFile := filepath.Join(tmpDir, "smtp-att-after-cancel.txt")
	os.WriteFile(testFile, []byte("test"), 0644)
	past := time.Now().Add(-1 * time.Hour)
	os.Chtimes(testFile, past, past)

	// Wait more than the ticker interval
	time.Sleep(30 * time.Millisecond)

	// File should still exist since cleanup routine was cancelled
	if _, err := os.Stat(testFile); os.IsNotExist(err) {
		t.Error("file should still exist after cleanup routine was cancelled")
	}
}

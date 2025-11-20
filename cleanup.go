package smtp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"
)

// startCleanupRoutine starts background cleanup of temp files
func (p *Plugin) startCleanupRoutine(ctx context.Context) {
	if p.cfg.AttachmentStorage.Mode != "tempfile" {
		return
	}

	ticker := time.NewTicker(p.cfg.AttachmentStorage.CleanupAfter)

	go func() {
		for {
			select {
			case <-ctx.Done():
				ticker.Stop()
				return
			case <-ticker.C:
				p.cleanupTempFiles()
			}
		}
	}()
}

// cleanupTempFiles removes old temp files
func (p *Plugin) cleanupTempFiles() {
	dir := p.cfg.AttachmentStorage.TempDir
	cutoff := time.Now().Add(-p.cfg.AttachmentStorage.CleanupAfter)

	entries, err := os.ReadDir(dir)
	if err != nil {
		// Directory might not exist yet, which is fine
		if !os.IsNotExist(err) {
			p.log.Error("cleanup readdir error", zap.Error(err))
		}
		return
	}

	removed := 0
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "smtp-att-") {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		if info.ModTime().Before(cutoff) {
			path := filepath.Join(dir, entry.Name())
			if err := os.Remove(path); err != nil {
				p.log.Warn("failed to remove temp file",
					zap.String("path", path),
					zap.Error(err),
				)
			} else {
				removed++
			}
		}
	}

	if removed > 0 {
		p.log.Debug("temp file cleanup completed", zap.Int("removed", removed))
	}
}

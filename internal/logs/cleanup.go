// Package logs provides log rotation and cleanup helpers.
package logs

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/EstebanForge/construct-cli/internal/config"
)

const cleanupMarkerFile = ".logs_cleanup_last_run"

// RunCleanupIfDue removes old logs based on maintenance settings.
func RunCleanupIfDue(cfg *config.Config) {
	if cfg == nil || !cfg.Maintenance.CleanupEnabled {
		return
	}

	interval := time.Duration(cfg.Maintenance.CleanupIntervalSeconds) * time.Second
	if interval <= 0 {
		interval = 24 * time.Hour
	}

	now := time.Now()
	configDir := config.GetConfigDir()
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return
	}

	markerPath := filepath.Join(configDir, cleanupMarkerFile)
	shouldRun := true

	if data, err := os.ReadFile(markerPath); err == nil {
		if lastRun, err := parseMarkerTimestamp(string(data)); err == nil {
			if now.Sub(lastRun) < interval {
				shouldRun = false
			}
		}
	}

	if !shouldRun {
		return
	}

	cleanupLogs(cfg.Maintenance.LogRetentionDays, now)
	if err := os.WriteFile(markerPath, []byte(strconv.FormatInt(now.Unix(), 10)), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to write cleanup marker: %v\n", err)
	}
}

func parseMarkerTimestamp(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, fmt.Errorf("empty marker")
	}
	seconds, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(seconds, 0), nil
}

func cleanupLogs(retentionDays int, now time.Time) {
	if retentionDays <= 0 {
		return
	}

	logsDir := config.GetLogsDir()
	entries, err := os.ReadDir(logsDir)
	if err != nil {
		return
	}

	cutoff := now.Add(-time.Duration(retentionDays) * 24 * time.Hour)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			if err := os.Remove(filepath.Join(logsDir, entry.Name())); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to remove log file %s: %v\n", entry.Name(), err)
			}
		}
	}
}

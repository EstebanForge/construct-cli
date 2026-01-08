package config

import (
	"os"
	"path/filepath"
	"strings"
)

const rebuildRequiredFile = ".rebuild_required"

// GetRebuildRequired returns the reason and whether a rebuild is required.
func GetRebuildRequired() (string, bool) {
	path := filepath.Join(GetConfigDir(), rebuildRequiredFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(data)), true
}

// SetRebuildRequired records a rebuild-required marker with a reason.
func SetRebuildRequired(reason string) error {
	path := filepath.Join(GetConfigDir(), rebuildRequiredFile)
	if strings.TrimSpace(reason) == "" {
		reason = "rebuild required"
	}
	return os.WriteFile(path, []byte(reason+"\n"), 0644)
}

// ClearRebuildRequired removes the rebuild-required marker.
func ClearRebuildRequired() error {
	path := filepath.Join(GetConfigDir(), rebuildRequiredFile)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

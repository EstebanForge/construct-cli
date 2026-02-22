package update

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/EstebanForge/construct-cli/internal/config"
	"github.com/EstebanForge/construct-cli/internal/constants"
)

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		v1       string
		v2       string
		expected int
	}{
		// v1 > v2
		{"0.10.1", "0.10.0", 1},
		{"1.0.0", "0.9.9", 1},
		{"0.3.1", "0.3.0", 1},

		// v1 < v2
		{"0.10.0", "0.10.1", -1},
		{"0.9.9", "1.0.0", -1},

		// v1 == v2
		{"0.10.1", "0.10.1", 0},
		{"1.0.0", "1.0.0", 0},

		// Handle potential non-numeric segments gracefully (should default to 0)
		{"0.10.a", "0.10.0", 0},
		{"0.10", "0.10.0", 0},
	}

	for _, tt := range tests {
		result := compareVersions(tt.v1, tt.v2)
		if result != tt.expected {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", tt.v1, tt.v2, result, tt.expected)
		}
	}
}

func TestRecordUpdateCheckCreatesTimestampFileWhenMissing(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	checkFile := filepath.Join(config.GetConfigDir(), constants.UpdateCheckFile)
	if _, err := os.Stat(checkFile); !os.IsNotExist(err) {
		t.Fatalf("expected update check file to be missing before test, got err=%v", err)
	}

	RecordUpdateCheck()

	info, err := os.Stat(checkFile)
	if err != nil {
		t.Fatalf("expected update check file to exist after RecordUpdateCheck, got %v", err)
	}
	if info.IsDir() {
		t.Fatal("expected update check path to be a file, got directory")
	}
}

func TestRecordUpdateCheckRefreshesTimestamp(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	checkFile := filepath.Join(config.GetConfigDir(), constants.UpdateCheckFile)
	if err := os.MkdirAll(filepath.Dir(checkFile), 0755); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}
	if err := os.WriteFile(checkFile, []byte{}, 0644); err != nil {
		t.Fatalf("failed to create update check file: %v", err)
	}

	stale := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(checkFile, stale, stale); err != nil {
		t.Fatalf("failed to set stale timestamp: %v", err)
	}

	RecordUpdateCheck()

	info, err := os.Stat(checkFile)
	if err != nil {
		t.Fatalf("failed to stat update check file: %v", err)
	}
	if !info.ModTime().After(stale) {
		t.Fatalf("expected mod time %v to be after stale time %v", info.ModTime(), stale)
	}
}

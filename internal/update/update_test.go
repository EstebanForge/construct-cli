package update

import (
	"os"
	"path/filepath"
	"strings"
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

		// Semver prerelease handling
		{"1.3.9-beta.1", "1.3.9-beta.0", 1},
		{"1.3.9", "1.3.9-beta.9", 1},
		{"1.3.9-beta.1", "1.3.9", -1},
	}

	for _, tt := range tests {
		result := compareVersions(tt.v1, tt.v2)
		if result != tt.expected {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", tt.v1, tt.v2, result, tt.expected)
		}
	}
}

func TestResolveUpdateChannel(t *testing.T) {
	tests := []struct {
		name     string
		cfg      *config.Config
		expected string
	}{
		{name: "nil config defaults stable", cfg: nil, expected: "stable"},
		{name: "empty channel defaults stable", cfg: &config.Config{}, expected: "stable"},
		{name: "explicit stable", cfg: &config.Config{Runtime: config.RuntimeConfig{UpdateChannel: "stable"}}, expected: "stable"},
		{name: "case-insensitive beta", cfg: &config.Config{Runtime: config.RuntimeConfig{UpdateChannel: "BeTa"}}, expected: "beta"},
		{name: "invalid channel falls back", cfg: &config.Config{Runtime: config.RuntimeConfig{UpdateChannel: "nightly"}}, expected: "stable"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := resolveUpdateChannel(tt.cfg)
			if result != tt.expected {
				t.Fatalf("resolveUpdateChannel() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestVersionURLForChannel(t *testing.T) {
	if got := versionURLForChannel("stable"); got != constants.GithubRawURL {
		t.Fatalf("stable channel URL mismatch: got %q want %q", got, constants.GithubRawURL)
	}
	if got := versionURLForChannel("beta"); got != constants.GithubRawBetaURL {
		t.Fatalf("beta channel URL mismatch: got %q want %q", got, constants.GithubRawBetaURL)
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

func TestResolveInstallTarget(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	tests := []struct {
		name         string
		execPath     string
		wantOverride bool
		wantTarget   string
	}{
		{
			name:         "brew cellar path uses user-local override",
			execPath:     "/opt/homebrew/Cellar/construct-cli/1.4.0/bin/construct",
			wantOverride: true,
			wantTarget:   filepath.Join(tempHome, ".local", "bin", "construct"),
		},
		{
			name:         "non-brew path keeps executable path",
			execPath:     "/usr/local/bin/construct",
			wantOverride: false,
			wantTarget:   "/usr/local/bin/construct",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotTarget, gotOverride, err := resolveInstallTarget(tt.execPath)
			if err != nil {
				t.Fatalf("resolveInstallTarget() unexpected error: %v", err)
			}
			if gotTarget != tt.wantTarget {
				t.Fatalf("resolveInstallTarget() target = %q, want %q", gotTarget, tt.wantTarget)
			}
			if gotOverride != tt.wantOverride {
				t.Fatalf("resolveInstallTarget() override = %v, want %v", gotOverride, tt.wantOverride)
			}
		})
	}
}

func TestIsPermissionError(t *testing.T) {
	if !isPermissionError(os.ErrPermission) {
		t.Fatal("expected os.ErrPermission to be detected as permission error")
	}
	if isPermissionError(nil) {
		t.Fatal("expected nil error to not be detected as permission error")
	}
	if isPermissionError(os.ErrNotExist) {
		t.Fatal("expected os.ErrNotExist to not be detected as permission error")
	}
}

func TestDisplayPath(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	inHome := filepath.Join(tempHome, ".local", "bin", "construct")
	got := displayPath(inHome)
	if !strings.HasPrefix(got, "~/") {
		t.Fatalf("expected displayPath to replace home prefix, got %q", got)
	}

	outside := "/usr/local/bin/construct"
	if gotOutside := displayPath(outside); gotOutside != outside {
		t.Fatalf("expected outside path to remain unchanged, got %q", gotOutside)
	}
}

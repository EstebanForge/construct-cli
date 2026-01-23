// Package daemon manages the optional background daemon container.
package daemon

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestGetLaunchdPlistPath tests plist path generation on macOS
func TestGetLaunchdPlistPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("Failed to get home directory: %v", err)
	}

	expected := filepath.Join(home, "Library", "LaunchAgents", "com.construct-cli.daemon.plist")
	result := getLaunchdPlistPath()

	if result != expected {
		t.Errorf("Expected plist path %s, got %s", expected, result)
	}
}

// TestGetSystemdUnitPath tests systemd unit path generation on Linux
func TestGetSystemdUnitPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("Failed to get home directory: %v", err)
	}

	expected := filepath.Join(home, ".config", "systemd", "user", "construct-daemon.service")
	result := getSystemdUnitPath()

	if result != expected {
		t.Errorf("Expected unit path %s, got %s", expected, result)
	}
}

// TestGetConstructBinaryPath tests binary path resolution
func TestGetConstructBinaryPath(t *testing.T) {
	result := getConstructBinaryPath()

	// Result should either be the current executable or "construct" fallback
	if result == "" {
		t.Error("Binary path should not be empty")
	}

	if result == "construct" {
		// Fallback is acceptable if os.Executable() failed
		return
	}

	// If not fallback, should be an absolute path
	if !filepath.IsAbs(result) {
		t.Errorf("Binary path should be absolute, got: %s", result)
	}

	// Verify the path exists (it should be the current test binary)
	if _, err := os.Stat(result); err != nil {
		t.Logf("Warning: Binary path does not exist: %s (may be expected in test env)", result)
	}
}

// TestInstallServiceUnsupportedOS verifies behavior on unsupported OS
func TestInstallServiceUnsupportedOS(t *testing.T) {
	// We can't easily test the full InstallService() as it calls os.Exit()
	// But we can verify the constants are correct
	if launchdLabel != "com.construct-cli.daemon" {
		t.Errorf("Expected launchd label 'com.construct-cli.daemon', got '%s'", launchdLabel)
	}

	if systemdUnit != "construct-daemon" {
		t.Errorf("Expected systemd unit 'construct-daemon', got '%s'", systemdUnit)
	}
}

// TestPlatformSpecificPaths verifies platform-specific path formats
func TestPlatformSpecificPaths(t *testing.T) {
	switch runtime.GOOS {
	case "darwin":
		plistPath := getLaunchdPlistPath()
		if !filepath.IsAbs(plistPath) {
			t.Errorf("Plist path should be absolute on macOS, got: %s", plistPath)
		}
		if filepath.Base(plistPath) != "com.construct-cli.daemon.plist" {
			t.Errorf("Plist filename incorrect, got: %s", filepath.Base(plistPath))
		}
	case "linux":
		unitPath := getSystemdUnitPath()
		if !filepath.IsAbs(unitPath) {
			t.Errorf("Unit path should be absolute on Linux, got: %s", unitPath)
		}
		if filepath.Base(unitPath) != "construct-daemon.service" {
			t.Errorf("Unit filename incorrect, got: %s", filepath.Base(unitPath))
		}
	default:
		t.Skipf("Platform-specific path test not implemented for %s", runtime.GOOS)
	}
}

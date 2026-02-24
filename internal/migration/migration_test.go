package migration

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EstebanForge/construct-cli/internal/config"
	"github.com/EstebanForge/construct-cli/internal/templates"
)

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		v1       string
		v2       string
		expected int
	}{
		// v1 > v2
		{"0.4.0", "0.3.0", 1},
		{"1.0.0", "0.9.9", 1},
		{"0.3.1", "0.3.0", 1},
		{"0.4.0", "0.3.9", 1},

		// v1 < v2
		{"0.3.0", "0.4.0", -1},
		{"0.9.9", "1.0.0", -1},
		{"0.3.0", "0.3.1", -1},

		// v1 == v2
		{"0.3.0", "0.3.0", 0},
		{"1.0.0", "1.0.0", 0},
		{"0.4.0", "0.4.0", 0},

		// With 'v' prefix
		{"v0.4.0", "v0.3.0", 1},
		{"v0.3.0", "v0.4.0", -1},
		{"v0.3.0", "v0.3.0", 0},

		// Mixed prefix
		{"0.4.0", "v0.3.0", 1},
		{"v0.3.0", "0.4.0", -1},

		// Prerelease handling
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

func TestIsPermissionWriteError(t *testing.T) {
	if !isPermissionWriteError(os.ErrPermission) {
		t.Fatal("expected os.ErrPermission to be detected")
	}
	if !isPermissionWriteError(fmt.Errorf("open file: permission denied")) {
		t.Fatal("expected permission denied string to be detected")
	}
	if isPermissionWriteError(fmt.Errorf("some other error")) {
		t.Fatal("unexpected permission detection for non-permission error")
	}
}

func TestAttemptMigrationPermissionRecoveryForOS(t *testing.T) {
	original := runOwnershipFixNonInteractiveFn
	originalAttempted := attemptedOwnershipFix
	t.Cleanup(func() {
		runOwnershipFixNonInteractiveFn = original
		attemptedOwnershipFix = originalAttempted
	})

	t.Run("linux permission error recovers", func(t *testing.T) {
		attemptedOwnershipFix = false
		called := false
		runOwnershipFixNonInteractiveFn = func(configPath string) error {
			called = true
			if configPath != "/tmp/test-config" {
				t.Fatalf("unexpected config path: %s", configPath)
			}
			return nil
		}

		recovered, err := attemptMigrationPermissionRecoveryForOS("linux", os.ErrPermission, "/tmp/test-config")
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
		if !recovered {
			t.Fatal("expected recovery to be attempted")
		}
		if !called {
			t.Fatal("expected ownership fix command to be called")
		}
	})

	t.Run("linux permission error with failing fix", func(t *testing.T) {
		attemptedOwnershipFix = false
		runOwnershipFixNonInteractiveFn = func(_ string) error {
			return fmt.Errorf("sudo failed")
		}

		recovered, err := attemptMigrationPermissionRecoveryForOS("linux", os.ErrPermission, "/tmp/test-config")
		if recovered {
			t.Fatal("expected recovery=false when fix fails")
		}
		if err == nil {
			t.Fatal("expected error when fix fails")
		}
	})

	t.Run("non-linux skips recovery", func(t *testing.T) {
		attemptedOwnershipFix = false
		runOwnershipFixNonInteractiveFn = func(_ string) error {
			t.Fatal("should not call ownership fix on non-linux")
			return nil
		}

		recovered, err := attemptMigrationPermissionRecoveryForOS("darwin", os.ErrPermission, "/tmp/test-config")
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
		if recovered {
			t.Fatal("expected recovered=false on non-linux")
		}
	})

	t.Run("non-permission error skips recovery", func(t *testing.T) {
		attemptedOwnershipFix = false
		runOwnershipFixNonInteractiveFn = func(_ string) error {
			t.Fatal("should not call ownership fix for non-permission error")
			return nil
		}

		recovered, err := attemptMigrationPermissionRecoveryForOS("linux", fmt.Errorf("not writable for another reason"), "/tmp/test-config")
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
		if recovered {
			t.Fatal("expected recovered=false for non-permission error")
		}
	})
}

func TestMigrationPermissionErrorIncludesManualFix(t *testing.T) {
	err := migrationPermissionError("write entrypoint-hash.sh", os.ErrPermission, "/tmp/cfg", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !isPermissionWriteError(err) {
		t.Fatal("expected error to be treated as permission error")
	}
	if !strings.Contains(msg, "sudo chown -R") {
		t.Fatalf("expected manual fix hint in error, got: %s", msg)
	}
}

func TestUpdateContainerTemplatesReplacesDirectoryCollision(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	containerDir := filepath.Join(config.GetConfigDir(), "container")
	blockingPath := filepath.Join(containerDir, "entrypoint-hash.sh")
	if err := os.MkdirAll(blockingPath, 0755); err != nil {
		t.Fatalf("failed to create blocking directory: %v", err)
	}

	if err := updateContainerTemplates(); err != nil {
		t.Fatalf("updateContainerTemplates failed: %v", err)
	}

	info, err := os.Stat(blockingPath)
	if err != nil {
		t.Fatalf("expected entrypoint-hash.sh to exist: %v", err)
	}
	if info.IsDir() {
		t.Fatal("expected entrypoint-hash.sh to be a file, got directory")
	}

	written, err := os.ReadFile(blockingPath)
	if err != nil {
		t.Fatalf("failed to read entrypoint-hash.sh: %v", err)
	}
	if string(written) != templates.EntrypointHash {
		t.Fatal("entrypoint-hash.sh content mismatch after collision recovery")
	}
}

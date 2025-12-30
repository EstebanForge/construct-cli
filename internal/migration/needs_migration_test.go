package migration

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EstebanForge/construct-cli/internal/config"
	"github.com/EstebanForge/construct-cli/internal/constants"
)

func TestNeedsMigration(t *testing.T) {
	// Setup temp home dir
	tempHome, err := os.MkdirTemp("", "construct-test-migration")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempHome)

	// Set HOME env var
	t.Setenv("HOME", tempHome)

	// Ensure config dir exists (simulating what Init does, but simpler)
	// Actually we can just call config.Init()!
	// But first we need to make sure config.Init() uses the new HOME.
	// Yes, GetConfigDir uses os.UserHomeDir which respects HOME.

	// 1. Fresh Install State
	if err := config.Init(); err != nil {
		t.Fatalf("config.Init() failed: %v", err)
	}

	// Verify initial state
	if NeedsMigration() {
		t.Error("Fresh install should not need migration")
	}

	// 2. Version Mismatch
	versionPath := filepath.Join(config.GetConfigDir(), ".version")
	if err := os.WriteFile(versionPath, []byte("0.0.1\n"), 0644); err != nil {
		t.Fatalf("Failed to write version file: %v", err)
	}
	if !NeedsMigration() {
		t.Error("Old version should trigger migration")
	}

	// Restore version
	if err := os.WriteFile(versionPath, []byte(constants.Version+"\n"), 0644); err != nil {
		t.Fatalf("Failed to restore version file: %v", err)
	}
	if NeedsMigration() {
		t.Error("Restored version should not need migration")
	}

	// 3. Entrypoint Hash Mismatch
	hashPath := filepath.Join(config.GetConfigDir(), ".entrypoint_template_hash")
	if err := os.WriteFile(hashPath, []byte("badhash\n"), 0644); err != nil {
		t.Fatalf("Failed to write bad hash: %v", err)
	}
	if !NeedsMigration() {
		t.Error("Mismatched entrypoint hash should trigger migration")
	}

	// 4. Entrypoint Hash Missing
	if err := os.Remove(hashPath); err != nil {
		t.Fatalf("Failed to remove hash file: %v", err)
	}
	// entrypoint.sh still exists from Init
	if !NeedsMigration() {
		t.Error("Missing entrypoint hash (with existing file) should trigger migration")
	}

	// 5. Entrypoint File Missing (and hash missing)
	containerDir := filepath.Join(config.GetConfigDir(), "container")
	entrypointPath := filepath.Join(containerDir, "entrypoint.sh")
	if err := os.Remove(entrypointPath); err != nil {
		t.Fatalf("Failed to remove entrypoint file: %v", err)
	}
	if NeedsMigration() {
		t.Error("Missing entrypoint file AND hash should NOT trigger migration (config.Init handles this)")
	}
}

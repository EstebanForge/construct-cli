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
	originalFix := runOwnershipFixFn
	originalConfirm := confirmOwnershipFixFn
	originalAttempted := attemptedOwnershipFix
	t.Cleanup(func() {
		runOwnershipFixFn = originalFix
		confirmOwnershipFixFn = originalConfirm
		attemptedOwnershipFix = originalAttempted
	})

	t.Run("linux permission error recovers", func(t *testing.T) {
		attemptedOwnershipFix = false
		called := false
		confirmOwnershipFixFn = func(_ string) bool { return true }
		runOwnershipFixFn = func(configPath string) error {
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
		confirmOwnershipFixFn = func(_ string) bool { return true }
		runOwnershipFixFn = func(_ string) error {
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

	t.Run("linux permission error declined by user", func(t *testing.T) {
		attemptedOwnershipFix = false
		confirmOwnershipFixFn = func(_ string) bool { return false }
		runOwnershipFixFn = func(_ string) error {
			t.Fatal("should not call ownership fix when user declines")
			return nil
		}

		recovered, err := attemptMigrationPermissionRecoveryForOS("linux", os.ErrPermission, "/tmp/test-config")
		if recovered {
			t.Fatal("expected recovered=false when user declines")
		}
		if err == nil {
			t.Fatal("expected error when user declines fix")
		}
		if !strings.Contains(err.Error(), "Run one of:") {
			t.Fatalf("expected manual fix instructions in decline error, got: %v", err)
		}
	})

	t.Run("non-linux skips recovery", func(t *testing.T) {
		attemptedOwnershipFix = false
		confirmOwnershipFixFn = func(_ string) bool {
			t.Fatal("should not prompt on non-linux")
			return false
		}
		runOwnershipFixFn = func(_ string) error {
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
		confirmOwnershipFixFn = func(_ string) bool {
			t.Fatal("should not prompt on non-permission error")
			return false
		}
		runOwnershipFixFn = func(_ string) error {
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

func TestHashTemplateDeterministic(t *testing.T) {
	allTemplates := make(map[string]string)
	for k, v := range imageTierTemplates {
		allTemplates[k] = v
	}
	for k, v := range softTierTemplates {
		allTemplates[k] = v
	}
	for name, content := range allTemplates {
		h1 := hashTemplate(content)
		h2 := hashTemplate(content)
		if h1 != h2 {
			t.Errorf("hashTemplate(%q) not deterministic: %q != %q", name, h1, h2)
		}
	}
}

func TestComputeTemplateHashesComplete(t *testing.T) {
	hashes := computeTemplateHashes()

	for name := range imageTierTemplates {
		if _, ok := hashes[name]; !ok {
			t.Errorf("image-tier template %q missing from computeTemplateHashes", name)
		}
	}
	for name := range softTierTemplates {
		if _, ok := hashes[name]; !ok {
			t.Errorf("soft-tier template %q missing from computeTemplateHashes", name)
		}
	}
	expected := len(imageTierTemplates) + len(softTierTemplates)
	if len(hashes) != expected {
		t.Errorf("computeTemplateHashes returned %d entries, expected %d", len(hashes), expected)
	}
}

func TestDiffTemplatesNoChange(t *testing.T) {
	stored := computeTemplateHashes()
	diff := diffTemplates(stored)
	if diff.ImageChanged {
		t.Error("expected ImageChanged=false when hashes match")
	}
	if diff.SoftChanged {
		t.Error("expected SoftChanged=false when hashes match")
	}
	if diff.EntrypointChanged {
		t.Error("expected EntrypointChanged=false when hashes match")
	}
	if len(diff.ChangedNames) != 0 {
		t.Errorf("expected no changed names, got %v", diff.ChangedNames)
	}
}

func TestDiffTemplatesImageTierChange(t *testing.T) {
	stored := computeTemplateHashes()
	stored["entrypoint.sh"] = "badhash"
	diff := diffTemplates(stored)
	if !diff.ImageChanged {
		t.Error("expected ImageChanged=true when entrypoint.sh differs")
	}
	if !diff.EntrypointChanged {
		t.Error("expected EntrypointChanged=true when entrypoint.sh differs")
	}
	if diff.SoftChanged {
		t.Error("expected SoftChanged=false for image-tier-only change")
	}
	found := false
	for _, name := range diff.ChangedNames {
		if name == "entrypoint.sh" {
			found = true
		}
	}
	if !found {
		t.Error("expected 'entrypoint.sh' in ChangedNames")
	}
}

func TestDiffTemplatesSoftTierOnly(t *testing.T) {
	stored := computeTemplateHashes()
	stored["docker-compose.yml"] = "badhash"
	diff := diffTemplates(stored)
	if diff.ImageChanged {
		t.Error("expected ImageChanged=false for soft-tier-only change")
	}
	if !diff.SoftChanged {
		t.Error("expected SoftChanged=true when docker-compose.yml differs")
	}
	if diff.EntrypointChanged {
		t.Error("expected EntrypointChanged=false for soft-tier change")
	}
}

func TestDiffTemplatesMissingKey(t *testing.T) {
	stored := computeTemplateHashes()
	delete(stored, "clipper")
	diff := diffTemplates(stored)
	if !diff.ImageChanged {
		t.Error("expected ImageChanged=true when template key is missing")
	}
}

func TestSaveLoadTemplateHashes(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	if err := config.Init(); err != nil {
		t.Fatalf("config.Init() failed: %v", err)
	}

	original := computeTemplateHashes()
	if err := saveTemplateHashes(original); err != nil {
		t.Fatalf("saveTemplateHashes failed: %v", err)
	}

	loaded := loadTemplateHashes()
	if loaded == nil {
		t.Fatal("loadTemplateHashes returned nil after save")
	}

	if len(loaded) != len(original) {
		t.Fatalf("loaded %d entries, expected %d", len(loaded), len(original))
	}
	for k, v := range original {
		if loaded[k] != v {
			t.Errorf("hash mismatch for %q: loaded %q, expected %q", k, loaded[k], v)
		}
	}
}

func TestLoadTemplateHashesMissing(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	// Create config dir but do NOT call config.Init() (which writes hashes)
	configDir := filepath.Join(homeDir, ".config", "construct-cli")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}

	loaded := loadTemplateHashes()
	if loaded != nil {
		t.Error("expected nil when hash file does not exist")
	}
}

func TestSaveTemplateHashesDeterministic(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	if err := config.Init(); err != nil {
		t.Fatalf("config.Init() failed: %v", err)
	}

	hashes := computeTemplateHashes()
	if err := saveTemplateHashes(hashes); err != nil {
		t.Fatalf("first save failed: %v", err)
	}
	first, err := os.ReadFile(filepath.Join(config.GetConfigDir(), templateHashesFile))
	if err != nil {
		t.Fatalf("read first: %v", err)
	}

	if err := saveTemplateHashes(hashes); err != nil {
		t.Fatalf("second save failed: %v", err)
	}
	second, err := os.ReadFile(filepath.Join(config.GetConfigDir(), templateHashesFile))
	if err != nil {
		t.Fatalf("read second: %v", err)
	}

	if string(first) != string(second) {
		t.Errorf("saved hash file not deterministic:\nfirst:  %s\nsecond: %s", first, second)
	}
}

func TestSaveTemplateHashesSortedOutput(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	if err := config.Init(); err != nil {
		t.Fatalf("config.Init() failed: %v", err)
	}

	if err := saveTemplateHashes(computeTemplateHashes()); err != nil {
		t.Fatalf("saveTemplateHashes failed: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(config.GetConfigDir(), templateHashesFile))
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}

	// Extract keys in order from the JSON output
	lines := strings.Split(string(content), "\n")
	var extractedKeys []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, `"`) {
			// Extract key between first pair of quotes
			end := strings.Index(line[1:], `"`)
			if end >= 0 {
				extractedKeys = append(extractedKeys, line[1:1+end])
			}
		}
	}

	// Verify keys are sorted
	for i := 1; i < len(extractedKeys); i++ {
		if extractedKeys[i] < extractedKeys[i-1] {
			t.Errorf("keys not sorted: %q appears before %q\n%s", extractedKeys[i-1], extractedKeys[i], content)
		}
	}
}

func TestCleanupLegacyHashFiles(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	if err := config.Init(); err != nil {
		t.Fatalf("config.Init() failed: %v", err)
	}

	// Create legacy file
	legacyPath := filepath.Join(config.GetConfigDir(), ".entrypoint_template_hash")
	if err := os.WriteFile(legacyPath, []byte("old\n"), 0644); err != nil {
		t.Fatalf("Failed to create legacy file: %v", err)
	}

	cleanupLegacyHashFiles()

	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Error("legacy hash file should have been removed")
	}
}

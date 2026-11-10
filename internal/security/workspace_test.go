package security

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/EstebanForge/construct-cli/internal/config"
)

func TestCreateAPFSCloneWithBrokenSymlink(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("APFS clone tests are macOS-specific")
	}

	tempDir := t.TempDir()
	projectRoot := filepath.Join(tempDir, "project")
	if err := os.MkdirAll(filepath.Join(projectRoot, ".antigravitycli"), 0755); err != nil {
		t.Fatalf("failed to create project dirs: %v", err)
	}

	// Create regular file
	regularFile := filepath.Join(projectRoot, "main.go")
	if err := os.WriteFile(regularFile, []byte("package main"), 0644); err != nil {
		t.Fatalf("failed to write regular file: %v", err)
	}

	// Create a broken symlink pointing to nonexistent guest path (exactly like the reported bug)
	brokenSymlink := filepath.Join(projectRoot, ".antigravitycli", "0eb101d6-70ff-4ef4-8dad-0af17500c61c.json")
	if err := os.Symlink("/home/construct/.gemini/config/projects/nonexistent.json", brokenSymlink); err != nil {
		t.Fatalf("failed to create broken symlink: %v", err)
	}

	// Create valid symlink
	validSymlink := filepath.Join(projectRoot, "symlink_main.go")
	if err := os.Symlink("main.go", validSymlink); err != nil {
		t.Fatalf("failed to create valid symlink: %v", err)
	}

	configDir := filepath.Join(tempDir, "config")
	wm := NewWorkspaceManager(configDir)

	sessionID := SessionID("test-symlink-session")
	ws, err := wm.CreateSessionWorkspace(sessionID, projectRoot, "fixed")
	if err != nil {
		t.Fatalf("CreateSessionWorkspace failed on broken symlink: %v", err)
	}
	defer ws.Cleanup()

	// Verify cloned files exist in ws.MergedDir
	clonedRegular := filepath.Join(ws.MergedDir, "main.go")
	if _, err := os.Stat(clonedRegular); err != nil {
		t.Errorf("expected cloned regular file to exist: %v", err)
	}

	clonedBroken := filepath.Join(ws.MergedDir, ".antigravitycli", "0eb101d6-70ff-4ef4-8dad-0af17500c61c.json")
	target, err := os.Readlink(clonedBroken)
	if err != nil {
		t.Errorf("expected cloned broken symlink to be readable as symlink: %v", err)
	}
	if target != "/home/construct/.gemini/config/projects/nonexistent.json" {
		t.Errorf("cloned symlink target mismatch: got %q, want %q", target, "/home/construct/.gemini/config/projects/nonexistent.json")
	}

	clonedValid := filepath.Join(ws.MergedDir, "symlink_main.go")
	targetValid, err := os.Readlink(clonedValid)
	if err != nil {
		t.Errorf("expected cloned valid symlink to be readable as symlink: %v", err)
	}
	if targetValid != "main.go" {
		t.Errorf("cloned valid symlink target mismatch: got %q, want %q", targetValid, "main.go")
	}
}

func TestAPFSCloneRedactsSecretsInMergedDir(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("APFS clone tests are macOS-specific")
	}

	tempDir := t.TempDir()
	projectRoot := filepath.Join(tempDir, "project")
	if err := os.MkdirAll(projectRoot, 0755); err != nil {
		t.Fatalf("failed to create project dir: %v", err)
	}

	envFile := filepath.Join(projectRoot, ".env")
	if err := os.WriteFile(envFile, []byte("API_KEY=super-secret-12345\nPORT=8080\n"), 0644); err != nil {
		t.Fatalf("failed to write .env: %v", err)
	}

	configDir := filepath.Join(tempDir, "config")
	cfg := &config.Config{
		Security: config.SecurityConfig{
			HideSecrets:          true,
			HideSecretsMaskStyle: "fixed",
			HideGitDir:           true,
		},
	}

	sess, err := Open(cfg, configDir, projectRoot)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer sess.Close()

	mergedEnv := filepath.Join(sess.ProjectRoot(), ".env")
	content, err := os.ReadFile(mergedEnv)
	if err != nil {
		t.Fatalf("failed to read merged .env: %v", err)
	}

	if strings.Contains(string(content), "super-secret-12345") {
		t.Fatal("merged .env contains unredacted secrets in APFS clone mode")
	}
}

func TestAPFSCloneSymlinkSecretDoesNotOverwriteHost(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("APFS clone tests are macOS-specific")
	}

	tempDir := t.TempDir()
	externalSecretDir := filepath.Join(tempDir, "external")
	if err := os.MkdirAll(externalSecretDir, 0755); err != nil {
		t.Fatalf("failed to create external dir: %v", err)
	}

	hostSecretFile := filepath.Join(externalSecretDir, "real_secret.env")
	hostSecretContent := "API_KEY=real-host-secret-99999\n"
	if err := os.WriteFile(hostSecretFile, []byte(hostSecretContent), 0600); err != nil {
		t.Fatalf("failed to write host secret file: %v", err)
	}

	projectRoot := filepath.Join(tempDir, "project")
	if err := os.MkdirAll(projectRoot, 0755); err != nil {
		t.Fatalf("failed to create project dir: %v", err)
	}

	// Symlink in project pointing to external host file
	symlinkPath := filepath.Join(projectRoot, ".env")
	if err := os.Symlink(hostSecretFile, symlinkPath); err != nil {
		t.Fatalf("failed to create symlink: %v", err)
	}

	configDir := filepath.Join(tempDir, "config")
	cfg := &config.Config{
		Security: config.SecurityConfig{
			HideSecrets:          true,
			HideSecretsMaskStyle: "fixed",
		},
	}

	sess, err := Open(cfg, configDir, projectRoot)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer sess.Close()

	// 1. Merged workspace must NOT expose the secret
	mergedEnv := filepath.Join(sess.ProjectRoot(), ".env")
	mergedContent, err := os.ReadFile(mergedEnv)
	if err != nil {
		t.Fatalf("failed to read merged .env: %v", err)
	}
	if strings.Contains(string(mergedContent), "real-host-secret-99999") {
		t.Fatal("merged .env still contains secret")
	}

	// 2. The host external secret file must NOT have been overwritten
	currentHostContent, err := os.ReadFile(hostSecretFile)
	if err != nil {
		t.Fatalf("failed to read host secret file: %v", err)
	}
	if string(currentHostContent) != hostSecretContent {
		t.Fatalf("host secret file was overwritten! got %q, want %q", string(currentHostContent), hostSecretContent)
	}
}

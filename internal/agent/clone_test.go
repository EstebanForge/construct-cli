package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/EstebanForge/construct-cli/internal/config"
	"github.com/EstebanForge/construct-cli/internal/hostgit"
)

func TestEngineCloneModeLifecycle(t *testing.T) {
	ctx := context.Background()
	repoDir := t.TempDir()

	// Initialize git repo
	if err := hostgit.Command(ctx, "init", repoDir).Run(); err != nil {
		t.Fatalf("failed to init git repo: %v", err)
	}

	testFile := filepath.Join(repoDir, "hello.txt")
	if err := os.WriteFile(testFile, []byte("original\n"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := hostgit.Command(ctx, "-C", repoDir, "-c", "user.name=Test", "-c", "user.email=test@local", "add", "hello.txt").Run(); err != nil {
		t.Fatal(err)
	}
	if err := hostgit.Command(ctx, "-C", repoDir, "-c", "user.name=Test", "-c", "user.email=test@local", "commit", "-m", "init").Run(); err != nil {
		t.Fatal(err)
	}

	// Change working directory to repoDir
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(repoDir); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Chdir(origWd)
	}()

	cfg := &config.Config{}
	engine := NewRuntimeEngine(cfg, []string{"claude"}, "docker", t.TempDir(), nil)
	engine.SetCloneMode(true)

	if err := engine.Prepare(); err != nil {
		t.Fatalf("engine.Prepare() failed: %v", err)
	}

	// Verify that e.cwd is pointing to the ephemeral clone dir, NOT the host repo
	if engine.cwd == repoDir {
		t.Errorf("expected engine.cwd to point to clone, but got repoDir %s", engine.cwd)
	}
	if engine.cloneSession == nil {
		t.Fatalf("expected cloneSession to be set")
	}

	// Write work in clone
	clonedFile := filepath.Join(engine.cwd, "agent-output.txt")
	if err := os.WriteFile(clonedFile, []byte("agent commit\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Teardown engine
	engine.Teardown()

	// Verify that bundle was exported to host repoDir
	entries, err := os.ReadDir(repoDir)
	if err != nil {
		t.Fatal(err)
	}

	var bundleFound string
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".bundle" {
			bundleFound = filepath.Join(repoDir, entry.Name())
			break
		}
	}

	if bundleFound == "" {
		t.Fatalf("expected git bundle file in host repo, found none")
	}
	defer func() {
		_ = os.Remove(bundleFound)
	}()

	// Verify the bundle
	if err := hostgit.Command(ctx, "-C", repoDir, "bundle", "verify", bundleFound).Run(); err != nil {
		t.Errorf("bundle verify failed: %v", err)
	}
}

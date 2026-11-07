package gitclone

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/EstebanForge/construct-cli/internal/hostgit"
)

func TestCloneSessionLifecycle(t *testing.T) {
	ctx := context.Background()
	repoDir := t.TempDir()

	// Initialize test git repo
	if err := hostgit.Command(ctx, "init", repoDir).Run(); err != nil {
		t.Fatalf("failed to init git repo: %v", err)
	}

	readmeFile := filepath.Join(repoDir, "README.md")
	if err := os.WriteFile(readmeFile, []byte("# Initial Repo\n"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := hostgit.Command(ctx, "-C", repoDir, "-c", "user.name=Test", "-c", "user.email=test@local", "add", "README.md").Run(); err != nil {
		t.Fatal(err)
	}
	if err := hostgit.Command(ctx, "-C", repoDir, "-c", "user.name=Test", "-c", "user.email=test@local", "commit", "-m", "init").Run(); err != nil {
		t.Fatal(err)
	}

	// Make an uncommitted change
	if err := os.WriteFile(readmeFile, []byte("# Modified Repo\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create clone session
	session, err := CreateSession(ctx, repoDir)
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	// Verify uncommitted change carried over
	clonedReadme := filepath.Join(session.CloneDir, "README.md")
	data, err := os.ReadFile(clonedReadme)
	if err != nil {
		t.Fatalf("failed to read cloned README: %v", err)
	}
	if string(data) != "# Modified Repo\n" {
		t.Errorf("expected '# Modified Repo\\n', got %q", string(data))
	}

	// Simulate guest agent work: add a new file
	guestFile := filepath.Join(session.CloneDir, "feature.txt")
	if err := os.WriteFile(guestFile, []byte("guest work\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Teardown session
	bundlePath, hasChanges, err := session.Teardown(ctx)
	if err != nil {
		t.Fatalf("Teardown failed: %v", err)
	}
	if !hasChanges {
		t.Fatalf("expected hasChanges to be true")
	}
	if bundlePath == "" {
		t.Fatalf("expected non-empty bundlePath")
	}
	defer func() {
		_ = os.Remove(bundlePath)
	}()

	// Verify bundle file was created
	if _, err := os.Stat(bundlePath); err != nil {
		t.Errorf("bundle file does not exist: %v", err)
	}

	// Verify clone dir was removed
	if _, err := os.Stat(session.CloneDir); !os.IsNotExist(err) {
		t.Errorf("expected clone directory to be deleted, err=%v", err)
	}

	// Verify bundle validity with git
	if out, err := hostgit.Command(ctx, "-C", repoDir, "bundle", "verify", bundlePath).CombinedOutput(); err != nil {
		t.Errorf("git bundle verify failed: %v, out: %s", err, string(out))
	}
}

func TestCloneSessionNoChanges(t *testing.T) {
	ctx := context.Background()
	repoDir := t.TempDir()

	if err := hostgit.Command(ctx, "init", repoDir).Run(); err != nil {
		t.Fatalf("failed to init git repo: %v", err)
	}
	readmeFile := filepath.Join(repoDir, "README.md")
	if err := os.WriteFile(readmeFile, []byte("# Initial Repo\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := hostgit.Command(ctx, "-C", repoDir, "-c", "user.name=Test", "-c", "user.email=test@local", "add", "README.md").Run(); err != nil {
		t.Fatal(err)
	}
	if err := hostgit.Command(ctx, "-C", repoDir, "-c", "user.name=Test", "-c", "user.email=test@local", "commit", "-m", "init").Run(); err != nil {
		t.Fatal(err)
	}

	session, err := CreateSession(ctx, repoDir)
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	bundlePath, hasChanges, err := session.Teardown(ctx)
	if err != nil {
		t.Fatalf("Teardown failed: %v", err)
	}
	if hasChanges {
		t.Errorf("expected hasChanges to be false")
	}
	if bundlePath != "" {
		t.Errorf("expected empty bundlePath, got %q", bundlePath)
	}
}

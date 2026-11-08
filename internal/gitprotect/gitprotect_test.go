package gitprotect

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureEmptyHooksDir(t *testing.T) {
	tempDir := t.TempDir()
	hooksDir, err := EnsureEmptyHooksDir(tempDir)
	if err != nil {
		t.Fatalf("unexpected error creating empty hooks dir: %v", err)
	}

	info, err := os.Stat(hooksDir)
	if err != nil {
		t.Fatalf("hooks dir does not exist: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("expected directory, got file")
	}

	entries, err := os.ReadDir(hooksDir)
	if err != nil {
		t.Fatalf("failed to read dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected empty directory, found %d entries", len(entries))
	}
}

func TestLockUnlockNoopOnNonDarwin(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	gitDir := filepath.Join(tempDir, ".git")
	if err := os.MkdirAll(filepath.Join(gitDir, "hooks"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "config"), []byte("[core]\n"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := LockRepository(ctx, tempDir); err != nil {
		t.Errorf("LockRepository failed: %v", err)
	}
	if err := UnlockRepository(ctx, tempDir); err != nil {
		t.Errorf("UnlockRepository failed: %v", err)
	}
}

func TestLockUnlockWorktreePointer(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	actualGitDir := filepath.Join(tempDir, "main-repo", ".git", "worktrees", "wt")
	if err := os.MkdirAll(filepath.Join(actualGitDir, "hooks"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(actualGitDir, "config"), []byte("[core]\n"), 0644); err != nil {
		t.Fatal(err)
	}

	wtDir := filepath.Join(tempDir, "wt")
	if err := os.MkdirAll(wtDir, 0755); err != nil {
		t.Fatal(err)
	}
	dotGitFile := filepath.Join(wtDir, ".git")
	if err := os.WriteFile(dotGitFile, []byte("gitdir: "+actualGitDir+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := LockRepository(ctx, wtDir); err != nil {
		t.Errorf("LockRepository worktree failed: %v", err)
	}
	if err := UnlockRepository(ctx, wtDir); err != nil {
		t.Errorf("UnlockRepository worktree failed: %v", err)
	}
}

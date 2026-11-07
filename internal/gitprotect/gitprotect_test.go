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

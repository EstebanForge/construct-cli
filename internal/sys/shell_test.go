package sys

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveBinaryPathPreservesSymlinkPath(t *testing.T) {
	tmpDir := t.TempDir()
	realBin := filepath.Join(tmpDir, "real-bin")
	linkBin := filepath.Join(tmpDir, "shim-bin")

	if err := os.WriteFile(realBin, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatalf("failed to create real binary: %v", err)
	}
	if err := os.Symlink(realBin, linkBin); err != nil {
		t.Fatalf("failed to create symlink: %v", err)
	}

	got := resolveBinaryPath(linkBin)
	want, err := filepath.Abs(linkBin)
	if err != nil {
		t.Fatalf("failed to get abs symlink path: %v", err)
	}
	if got != want {
		t.Fatalf("expected symlink path %q, got %q", want, got)
	}
}

func TestResolveBinaryPathNormalizesRelativePath(t *testing.T) {
	tmpDir := t.TempDir()
	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get cwd: %v", err)
	}
	defer os.Chdir(originalWD)

	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("failed to chdir: %v", err)
	}
	currentWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get cwd after chdir: %v", err)
	}

	got := resolveBinaryPath("./tool")
	want := filepath.Join(currentWD, "tool")
	if got != want {
		t.Fatalf("expected absolute path %q, got %q", want, got)
	}
}

func TestResolveAliasConstructCommandPrefersLocalCt(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	localBin := filepath.Join(tempHome, ".local", "bin")
	if err := os.MkdirAll(localBin, 0755); err != nil {
		t.Fatalf("failed to create local bin: %v", err)
	}
	localCt := filepath.Join(localBin, "ct")
	if err := os.WriteFile(localCt, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatalf("failed to create ct shim: %v", err)
	}

	got, err := resolveAliasConstructCommand()
	if err != nil {
		t.Fatalf("resolveAliasConstructCommand returned error: %v", err)
	}
	if got != localCt {
		t.Fatalf("expected local ct path %q, got %q", localCt, got)
	}
}

func TestResolveAliasConstructCommandFallsBackToExecutable(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	got, err := resolveAliasConstructCommand()
	if err != nil {
		t.Fatalf("resolveAliasConstructCommand returned error: %v", err)
	}
	if got == "" {
		t.Fatal("expected non-empty command path")
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("expected absolute path, got %q", got)
	}
}

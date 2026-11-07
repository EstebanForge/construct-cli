// Package gitprotect safeguards repository git configuration and hooks.
package gitprotect

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// HasImmutableFlag reports whether the host OS supports user immutable file flags (macOS).
func HasImmutableFlag() bool {
	return runtime.GOOS == "darwin"
}

// LockRepository applies immutable flags on macOS to protect critical git paths.
func LockRepository(ctx context.Context, repoDir string) error {
	if !HasImmutableFlag() {
		return nil
	}

	gitDir := filepath.Join(repoDir, ".git")
	if info, err := os.Stat(gitDir); err != nil || !info.IsDir() {
		return nil
	}

	configPath := filepath.Join(gitDir, "config")
	if _, err := os.Stat(configPath); err == nil {
		if err := exec.CommandContext(ctx, "chflags", "uchg", configPath).Run(); err != nil {
			return fmt.Errorf("lock git config: %w", err)
		}
	}

	hooksPath := filepath.Join(gitDir, "hooks")
	if _, err := os.Stat(hooksPath); err == nil {
		if err := exec.CommandContext(ctx, "chflags", "-R", "uchg", hooksPath).Run(); err != nil {
			return fmt.Errorf("lock git hooks: %w", err)
		}
	}

	return nil
}

// UnlockRepository removes immutable flags on macOS.
func UnlockRepository(ctx context.Context, repoDir string) error {
	if !HasImmutableFlag() {
		return nil
	}

	gitDir := filepath.Join(repoDir, ".git")
	if info, err := os.Stat(gitDir); err != nil || !info.IsDir() {
		return nil
	}

	configPath := filepath.Join(gitDir, "config")
	if _, err := os.Stat(configPath); err == nil {
		if err := exec.CommandContext(ctx, "chflags", "nouchg", configPath).Run(); err != nil {
			return fmt.Errorf("unlock git config: %w", err)
		}
	}

	hooksPath := filepath.Join(gitDir, "hooks")
	if _, err := os.Stat(hooksPath); err == nil {
		if err := exec.CommandContext(ctx, "chflags", "-R", "nouchg", hooksPath).Run(); err != nil {
			return fmt.Errorf("unlock git hooks: %w", err)
		}
	}

	return nil
}

// EnsureEmptyHooksDir ensures an empty directory exists on the host to mount over guest hooks.
func EnsureEmptyHooksDir(cacheDir string) (string, error) {
	dir := filepath.Join(cacheDir, "empty-hooks")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	return dir, nil
}

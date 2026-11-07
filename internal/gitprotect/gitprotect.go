// Package gitprotect safeguards repository git configuration and hooks.
package gitprotect

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// HasImmutableFlag reports whether the host OS supports immutable file flags (macOS chflags or Linux chattr).
func HasImmutableFlag() bool {
	return runtime.GOOS == "darwin" || runtime.GOOS == "linux"
}

// LockRepository applies immutable flags to protect critical git paths.
func LockRepository(ctx context.Context, repoDir string) error {
	if !HasImmutableFlag() {
		return nil
	}

	gitDir := filepath.Join(repoDir, ".git")
	if info, err := os.Stat(gitDir); err != nil || !info.IsDir() {
		return nil
	}

	switch runtime.GOOS {
	case "darwin":
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
	case "linux":
		if _, err := exec.LookPath("chattr"); err != nil {
			return nil
		}
		configPath := filepath.Join(gitDir, "config")
		if _, err := os.Stat(configPath); err == nil {
			cmd := exec.CommandContext(ctx, "chattr", "+i", configPath)
			if out, err := cmd.CombinedOutput(); err != nil {
				if os.Geteuid() != 0 || strings.Contains(string(out), "Operation not permitted") || strings.Contains(string(out), "not supported") {
					return nil
				}
				return fmt.Errorf("lock git config (chattr +i): %w: %s", err, string(out))
			}
		}
		hooksPath := filepath.Join(gitDir, "hooks")
		if _, err := os.Stat(hooksPath); err == nil {
			cmd := exec.CommandContext(ctx, "chattr", "-R", "+i", hooksPath)
			if out, err := cmd.CombinedOutput(); err != nil {
				if os.Geteuid() != 0 || strings.Contains(string(out), "Operation not permitted") || strings.Contains(string(out), "not supported") {
					return nil
				}
				return fmt.Errorf("lock git hooks (chattr +i): %w: %s", err, string(out))
			}
		}
	}

	return nil
}

// UnlockRepository removes immutable flags.
func UnlockRepository(ctx context.Context, repoDir string) error {
	if !HasImmutableFlag() {
		return nil
	}

	gitDir := filepath.Join(repoDir, ".git")
	if info, err := os.Stat(gitDir); err != nil || !info.IsDir() {
		return nil
	}

	switch runtime.GOOS {
	case "darwin":
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
	case "linux":
		if _, err := exec.LookPath("chattr"); err != nil {
			return nil
		}
		configPath := filepath.Join(gitDir, "config")
		if _, err := os.Stat(configPath); err == nil {
			cmd := exec.CommandContext(ctx, "chattr", "-i", configPath)
			if out, err := cmd.CombinedOutput(); err != nil {
				if os.Geteuid() != 0 || strings.Contains(string(out), "Operation not permitted") || strings.Contains(string(out), "not supported") {
					return nil
				}
				return fmt.Errorf("unlock git config (chattr -i): %w: %s", err, string(out))
			}
		}
		hooksPath := filepath.Join(gitDir, "hooks")
		if _, err := os.Stat(hooksPath); err == nil {
			cmd := exec.CommandContext(ctx, "chattr", "-R", "-i", hooksPath)
			if out, err := cmd.CombinedOutput(); err != nil {
				if os.Geteuid() != 0 || strings.Contains(string(out), "Operation not permitted") || strings.Contains(string(out), "not supported") {
					return nil
				}
				return fmt.Errorf("unlock git hooks (chattr -i): %w: %s", err, string(out))
			}
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

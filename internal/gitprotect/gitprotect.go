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

func resolveGitDirs(repoDir string) (gitDir string, commonDir string, pointerFile string, err error) {
	dotGit := filepath.Join(repoDir, ".git")
	info, err := os.Stat(dotGit)
	if err != nil {
		return "", "", "", err
	}
	if info.IsDir() {
		return dotGit, dotGit, "", nil
	}
	// Worktree file pointer (.git file containing "gitdir: <path>")
	data, err := os.ReadFile(dotGit)
	if err != nil {
		return "", "", "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "gitdir:") {
			target := strings.TrimSpace(strings.TrimPrefix(line, "gitdir:"))
			if !filepath.IsAbs(target) {
				target = filepath.Join(repoDir, target)
			}
			common := target
			if cData, cErr := os.ReadFile(filepath.Join(target, "commondir")); cErr == nil {
				cRel := strings.TrimSpace(string(cData))
				if filepath.IsAbs(cRel) {
					common = cRel
				} else {
					common = filepath.Clean(filepath.Join(target, cRel))
				}
			}
			return target, common, dotGit, nil
		}
	}
	return "", "", "", fmt.Errorf("invalid .git worktree pointer")
}

func lockHooksChmod(hooksPath string) {
	_ = os.Chmod(hooksPath, 0555)                                                       //nolint:errcheck
	_ = filepath.Walk(hooksPath, func(path string, info os.FileInfo, err error) error { //nolint:errcheck
		if err == nil && !info.IsDir() {
			_ = os.Chmod(path, 0444) //nolint:errcheck
		}
		return nil
	})
}

func unlockHooksChmod(hooksPath string) {
	_ = os.Chmod(hooksPath, 0755)                                                       //nolint:errcheck
	_ = filepath.Walk(hooksPath, func(path string, info os.FileInfo, err error) error { //nolint:errcheck
		if err == nil && !info.IsDir() {
			_ = os.Chmod(path, 0755) //nolint:errcheck
		}
		return nil
	})
}

// LockRepository applies immutable flags to protect critical git paths.
func LockRepository(ctx context.Context, repoDir string) error {
	if !HasImmutableFlag() {
		return nil
	}

	gitDir, commonDir, pointerFile, err := resolveGitDirs(repoDir)
	if err != nil || gitDir == "" {
		return nil
	}

	dirsToLock := []string{gitDir}
	if commonDir != "" && commonDir != gitDir {
		dirsToLock = append(dirsToLock, commonDir)
	}

	switch runtime.GOOS {
	case "darwin":
		if pointerFile != "" {
			_ = exec.CommandContext(ctx, "chflags", "uchg", pointerFile).Run() //nolint:errcheck
		}
		for _, d := range dirsToLock {
			configPath := filepath.Join(d, "config")
			if _, err := os.Stat(configPath); err == nil {
				if err := exec.CommandContext(ctx, "chflags", "uchg", configPath).Run(); err != nil {
					return fmt.Errorf("lock git config: %w", err)
				}
			}
			hooksPath := filepath.Join(d, "hooks")
			if _, err := os.Stat(hooksPath); err == nil {
				if err := exec.CommandContext(ctx, "chflags", "-R", "uchg", hooksPath).Run(); err != nil {
					return fmt.Errorf("lock git hooks: %w", err)
				}
			}
		}
	case "linux":
		if pointerFile != "" {
			cmd := exec.CommandContext(ctx, "chattr", "+i", pointerFile)
			if out, err := cmd.CombinedOutput(); err != nil {
				_ = os.Chmod(pointerFile, 0444) //nolint:errcheck
				_ = out
			}
		}
		for _, d := range dirsToLock {
			configPath := filepath.Join(d, "config")
			if _, err := os.Stat(configPath); err == nil {
				cmd := exec.CommandContext(ctx, "chattr", "+i", configPath)
				if out, err := cmd.CombinedOutput(); err != nil {
					// Fallback to unprivileged readonly mode if CAP_LINUX_IMMUTABLE is unavailable
					_ = os.Chmod(configPath, 0444) //nolint:errcheck
					if os.Geteuid() != 0 || strings.Contains(string(out), "Operation not permitted") || strings.Contains(string(out), "not supported") {
						continue
					}
					return fmt.Errorf("lock git config (chattr +i): %w: %s", err, string(out))
				}
			}
			hooksPath := filepath.Join(d, "hooks")
			if _, err := os.Stat(hooksPath); err == nil {
				cmd := exec.CommandContext(ctx, "chattr", "-R", "+i", hooksPath)
				if out, err := cmd.CombinedOutput(); err != nil {
					lockHooksChmod(hooksPath)
					if os.Geteuid() != 0 || strings.Contains(string(out), "Operation not permitted") || strings.Contains(string(out), "not supported") {
						continue
					}
					return fmt.Errorf("lock git hooks (chattr +i): %w: %s", err, string(out))
				}
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

	gitDir, commonDir, pointerFile, err := resolveGitDirs(repoDir)
	if err != nil || gitDir == "" {
		return nil
	}

	dirsToUnlock := []string{gitDir}
	if commonDir != "" && commonDir != gitDir {
		dirsToUnlock = append(dirsToUnlock, commonDir)
	}

	switch runtime.GOOS {
	case "darwin":
		if pointerFile != "" {
			_ = exec.CommandContext(ctx, "chflags", "nouchg", pointerFile).Run() //nolint:errcheck
		}
		for _, d := range dirsToUnlock {
			configPath := filepath.Join(d, "config")
			if _, err := os.Stat(configPath); err == nil {
				if err := exec.CommandContext(ctx, "chflags", "nouchg", configPath).Run(); err != nil {
					return fmt.Errorf("unlock git config: %w", err)
				}
			}
			hooksPath := filepath.Join(d, "hooks")
			if _, err := os.Stat(hooksPath); err == nil {
				if err := exec.CommandContext(ctx, "chflags", "-R", "nouchg", hooksPath).Run(); err != nil {
					return fmt.Errorf("unlock git hooks: %w", err)
				}
			}
		}
	case "linux":
		if pointerFile != "" {
			_ = exec.CommandContext(ctx, "chattr", "-i", pointerFile).Run() //nolint:errcheck
			_ = os.Chmod(pointerFile, 0644)                                 //nolint:errcheck
		}
		for _, d := range dirsToUnlock {
			configPath := filepath.Join(d, "config")
			if _, err := os.Stat(configPath); err == nil {
				cmd := exec.CommandContext(ctx, "chattr", "-i", configPath)
				if out, err := cmd.CombinedOutput(); err != nil {
					_ = os.Chmod(configPath, 0644) //nolint:errcheck
					if os.Geteuid() != 0 || strings.Contains(string(out), "Operation not permitted") || strings.Contains(string(out), "not supported") {
						continue
					}
					return fmt.Errorf("unlock git config (chattr -i): %w: %s", err, string(out))
				}
				_ = os.Chmod(configPath, 0644) //nolint:errcheck
			}
			hooksPath := filepath.Join(d, "hooks")
			if _, err := os.Stat(hooksPath); err == nil {
				cmd := exec.CommandContext(ctx, "chattr", "-R", "-i", hooksPath)
				if out, err := cmd.CombinedOutput(); err != nil {
					unlockHooksChmod(hooksPath)
					if os.Geteuid() != 0 || strings.Contains(string(out), "Operation not permitted") || strings.Contains(string(out), "not supported") {
						continue
					}
					return fmt.Errorf("unlock git hooks (chattr -i): %w: %s", err, string(out))
				}
				unlockHooksChmod(hooksPath)
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

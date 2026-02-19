//revive:disable:var-naming // Package name intentionally matches folder name used across imports.
package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"github.com/EstebanForge/construct-cli/internal/config"
)

// DaemonMountsLabelKey stores the hash for multi-path daemon mounts.
const DaemonMountsLabelKey = "construct.daemon.mounts_hash"

// DaemonMount defines a host-to-container mount mapping.
type DaemonMount struct {
	HostPath      string
	ContainerPath string
}

// DaemonMounts holds validated multi-path daemon mount configuration.
type DaemonMounts struct {
	Enabled  bool
	Paths    []string
	Mounts   []DaemonMount
	Hash     string
	Warnings []string
}

// ResolveDaemonMounts validates and normalizes multi-path daemon mount settings.
// It returns warnings that callers can surface to users.
func ResolveDaemonMounts(cfg *config.Config) DaemonMounts {
	if cfg == nil || !cfg.Daemon.MultiPathsEnabled {
		return DaemonMounts{}
	}

	raw := cfg.Daemon.MountPaths
	result := DaemonMounts{}
	if len(raw) == 0 {
		result.Warnings = append(result.Warnings, "daemon.multi_paths_enabled is true but daemon.mount_paths is empty; falling back to single-root daemon mounts")
		return result
	}

	if len(raw) > 32 {
		result.Warnings = append(result.Warnings, fmt.Sprintf("daemon.mount_paths has %d entries; large lists may slow startup", len(raw)))
	}
	if len(raw) > 64 {
		result.Warnings = append(result.Warnings, "daemon.mount_paths exceeds 64 entries; ignoring entries beyond the first 64")
		raw = raw[:64]
	}

	paths := make([]string, 0, len(raw))
	seen := make(map[string]struct{})
	for _, entry := range raw {
		normalized, err := normalizeMountPath(entry)
		if err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("daemon.mount_paths entry skipped (%s): %v", strings.TrimSpace(entry), err))
			continue
		}
		if _, exists := seen[normalized]; exists {
			result.Warnings = append(result.Warnings, fmt.Sprintf("daemon.mount_paths entry duplicated and ignored: %s", normalized))
			continue
		}
		seen[normalized] = struct{}{}
		paths = append(paths, normalized)
	}

	if len(paths) == 0 {
		result.Warnings = append(result.Warnings, "daemon.mount_paths had no valid entries; falling back to single-root daemon mounts")
		return result
	}

	for i := 0; i < len(paths); i++ {
		for j := i + 1; j < len(paths); j++ {
			if containsPath(paths[i], paths[j]) {
				result.Warnings = append(result.Warnings, fmt.Sprintf("daemon.mount_paths overlap: %s contains %s (first match wins)", paths[i], paths[j]))
			} else if containsPath(paths[j], paths[i]) {
				result.Warnings = append(result.Warnings, fmt.Sprintf("daemon.mount_paths overlap: %s contains %s (first match wins)", paths[j], paths[i]))
			}
		}
	}

	result.Enabled = true
	result.Paths = paths
	result.Mounts = buildDaemonMounts(paths)
	result.Hash = hashDaemonMountPaths(paths)
	return result
}

// MapDaemonWorkdirFromMounts maps the host cwd into a daemon workdir using multi-path mounts.
func MapDaemonWorkdirFromMounts(cwd string, mounts []DaemonMount) (string, bool) {
	if cwd == "" || len(mounts) == 0 {
		return "", false
	}

	cwd = filepath.Clean(cwd)
	for _, mount := range mounts {
		rel, err := filepath.Rel(filepath.Clean(mount.HostPath), cwd)
		if err != nil {
			continue
		}
		if rel == "." {
			return mount.ContainerPath, true
		}
		if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			continue
		}
		return path.Join(mount.ContainerPath, filepath.ToSlash(rel)), true
	}

	return "", false
}

// GetContainerLabel returns the value of a container label.
func GetContainerLabel(containerRuntime, containerName, labelKey string) (string, error) {
	var cmd *exec.Cmd
	format := fmt.Sprintf("{{index .Config.Labels %q}}", labelKey)

	switch containerRuntime {
	case "docker", "container":
		cmd = exec.Command("docker", "inspect", "--format", format, containerName)
	case "podman":
		cmd = exec.Command("podman", "inspect", "--format", format, containerName)
	default:
		return "", fmt.Errorf("unsupported runtime: %s", containerRuntime)
	}

	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to inspect container labels: %w", err)
	}

	return strings.TrimSpace(string(output)), nil
}

func buildDaemonMounts(paths []string) []DaemonMount {
	mounts := make([]DaemonMount, 0, len(paths))
	for _, hostPath := range paths {
		mounts = append(mounts, DaemonMount{
			HostPath:      hostPath,
			ContainerPath: daemonMountDest(hostPath),
		})
	}
	return mounts
}

func hashDaemonMountPaths(paths []string) string {
	h := sha256.New()
	for _, entry := range paths {
		fmt.Fprintf(h, "%s\n", entry) //nolint:errcheck // sha256.Write never fails
	}
	return hex.EncodeToString(h.Sum(nil))
}

func daemonMountDest(hostPath string) string {
	sum := sha256.Sum256([]byte(hostPath))
	return path.Join("/workspaces", hex.EncodeToString(sum[:8]))
}

func normalizeMountPath(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("empty path")
	}

	expanded := os.ExpandEnv(trimmed)
	if strings.HasPrefix(expanded, "~") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("unable to expand home dir: %w", err)
		}
		switch {
		case expanded == "~":
			expanded = homeDir
		case strings.HasPrefix(expanded, "~/"):
			expanded = filepath.Join(homeDir, expanded[2:])
		default:
			return "", fmt.Errorf("unsupported ~ expansion")
		}
	}

	abs, err := filepath.Abs(expanded)
	if err != nil {
		return "", fmt.Errorf("unable to resolve absolute path: %w", err)
	}

	abs = filepath.Clean(abs)
	if _, err := os.Stat(abs); err != nil {
		return "", fmt.Errorf("path not found")
	}

	return abs, nil
}

func containsPath(parent, child string) bool {
	rel, err := filepath.Rel(filepath.Clean(parent), filepath.Clean(child))
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

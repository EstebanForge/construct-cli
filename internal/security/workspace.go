package security

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// WorkspaceManager manages isolated workspaces using OverlayFS (Linux) or APFS clones (macOS).
type WorkspaceManager struct {
	securityDir string
}

// NewWorkspaceManager creates a new workspace manager.
func NewWorkspaceManager(configDir string) *WorkspaceManager {
	return &WorkspaceManager{
		securityDir: filepath.Join(configDir, "security"),
	}
}

// WorkspaceType identifies the workspace isolation strategy.
type WorkspaceType string

const (
	// WorkspaceTypeOverlayFS uses Linux overlay mounts.
	WorkspaceTypeOverlayFS WorkspaceType = "overlayfs"
	// WorkspaceTypeAPFSClone uses macOS APFS clones.
	WorkspaceTypeAPFSClone WorkspaceType = "apfsclone"
)

// DetectWorkspaceType returns the appropriate workspace type for the current platform.
func DetectWorkspaceType() WorkspaceType {
	switch runtime.GOOS {
	case "linux":
		return WorkspaceTypeOverlayFS
	case "darwin":
		return WorkspaceTypeAPFSClone
	default:
		// Fallback: try overlayfs first, then clone
		return WorkspaceTypeOverlayFS
	}
}

// CreateSessionWorkspace creates an isolated workspace for a session.
func (wm *WorkspaceManager) CreateSessionWorkspace(sessionID SessionID, projectRoot string, maskStyle string) (*SessionWorkspace, error) {
	wsType := DetectWorkspaceType()

	sessionDir := filepath.Join(wm.securityDir, "sessions", string(sessionID))
	upperDir := filepath.Join(sessionDir, "upper")
	workDir := filepath.Join(sessionDir, "work")
	mergedDir := filepath.Join(sessionDir, "merged")

	// Create directory structure
	for _, dir := range []string{upperDir, workDir, mergedDir} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, fmt.Errorf("failed to create workspace directory %s: %w", dir, err)
		}
	}

	var mergedPath string
	var err error

	switch wsType {
	case WorkspaceTypeOverlayFS:
		mergedPath, err = wm.createOverlayFS(projectRoot, upperDir, workDir, mergedDir)
	case WorkspaceTypeAPFSClone:
		mergedPath, err = wm.createAPFSClone(projectRoot, upperDir, mergedDir)
	default:
		return nil, fmt.Errorf("unsupported workspace type: %s", wsType)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to create workspace: %w", err)
	}

	return &SessionWorkspace{
		SessionID:   sessionID,
		Type:        wsType,
		ProjectRoot: projectRoot,
		UpperDir:    upperDir,
		WorkDir:     workDir,
		MergedDir:   mergedPath,
		MaskStyle:   maskStyle,
	}, nil
}

// SessionWorkspace represents an isolated session workspace.
type SessionWorkspace struct {
	SessionID   SessionID
	Type        WorkspaceType
	ProjectRoot string
	UpperDir    string
	WorkDir     string
	MergedDir   string
	MaskStyle   string
}

// createOverlayFS creates an overlay mount on Linux.
func (wm *WorkspaceManager) createOverlayFS(lower, upper, work, merged string) (string, error) {
	// Check if running as root or has necessary capabilities
	if os.Geteuid() != 0 {
		// Try using user namespace mounts (requires kernel 4.18+)
		// For now, we'll require root or setuid binary
		return "", fmt.Errorf("overlayfs mounts require root privileges or unprivileged user namespace support")
	}

	// Ensure lower is read-only
	lowerRO := lower + "-ro"
	if err := os.MkdirAll(lowerRO, 0755); err != nil {
		return "", fmt.Errorf("failed to create lower ro directory: %w", err)
	}

	// Use mount command for overlayfs
	// mount -t overlay overlay -olowerdir=<lower>:<upper>,upperdir=<upper>,workdir=<work> <merged>
	lowerPath := fmt.Sprintf("%s:%s", lower, upper)
	options := fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s", lowerPath, upper, work)

	cmd := exec.Command("mount", "-t", "overlay", "overlay", "-o", options, merged)
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("overlayfs mount failed: %w, output: %s", err, string(output))
	}

	return merged, nil
}

// createAPFSClone creates an APFS clone-based workspace on macOS.
func (wm *WorkspaceManager) createAPFSClone(projectRoot, _ /* upper */, merged string) (string, error) {
	// On macOS, we'll use cp -c for copy-on-write clones
	// This creates a session directory with cloned files

	// Walk the project directory and clone files
	err := filepath.Walk(projectRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Compute relative path
		relPath, err := filepath.Rel(projectRoot, path)
		if err != nil {
			return err
		}

		targetPath := filepath.Join(merged, relPath)

		if info.IsDir() {
			// Create directory
			return os.MkdirAll(targetPath, info.Mode().Perm())
		}

		// Clone file using cp -c (APFS clone)
		// If cp -c fails (non-APFS volume), fall back to regular copy
		cmd := exec.Command("cp", "-c", path, targetPath)
		if err := cmd.Run(); err != nil {
			// Fall back to regular copy
			return copyFile(path, targetPath, info.Mode().Perm())
		}

		return nil
	})

	if err != nil {
		return "", fmt.Errorf("failed to clone project: %w", err)
	}

	return merged, nil
}

// Cleanup removes the workspace and unmounts if necessary.
func (ws *SessionWorkspace) Cleanup() error {
	switch ws.Type {
	case WorkspaceTypeOverlayFS:
		// Unmount overlay
		cmd := exec.Command("umount", ws.MergedDir)
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("failed to unmount overlay: %w, output: %s", err, string(output))
		}
	case WorkspaceTypeAPFSClone:
		// No unmount needed, just remove directory
	}

	// Remove session directory
	sessionDir := filepath.Dir(ws.UpperDir)
	if err := os.RemoveAll(sessionDir); err != nil {
		return fmt.Errorf("failed to remove session directory: %w", err)
	}

	return nil
}

// HasChanges returns true if files were modified in the workspace.
func (ws *SessionWorkspace) HasChanges() (bool, error) {
	// Check if upper layer has any files
	entries, err := os.ReadDir(ws.UpperDir)
	if err != nil {
		return false, fmt.Errorf("failed to read upper directory: %w", err)
	}

	return len(entries) > 0, nil
}

// GetChanges returns a list of changed files in the workspace.
func (ws *SessionWorkspace) GetChanges() ([]string, error) {
	var changes []string

	err := filepath.Walk(ws.UpperDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		relPath, err := filepath.Rel(ws.UpperDir, path)
		if err != nil {
			return err
		}

		changes = append(changes, relPath)
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to walk upper directory: %w", err)
	}

	return changes, nil
}

// copyFile copies a file with mode preservation.
func copyFile(src, dst string, mode os.FileMode) error {
	// Read source
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}

	// Write destination
	if err := os.WriteFile(dst, data, mode); err != nil {
		return err
	}

	return nil
}

// IsOverlayFSSupported checks if overlayfs is available.
func IsOverlayFSSupported() bool {
	if runtime.GOOS != "linux" {
		return false
	}

	// Check if overlay filesystem is available
	_, err := os.Stat("/proc/filesystems")
	if err != nil {
		return false
	}

	data, err := os.ReadFile("/proc/filesystems")
	if err != nil {
		return false
	}

	return strings.Contains(string(data), "overlay")
}

// IsAPFSCloneSupported checks if APFS clones are available.
func IsAPFSCloneSupported() bool {
	if runtime.GOOS != "darwin" {
		return false
	}

	// Try a test clone to see if it works
	tmpDir, err := os.MkdirTemp("", "apfs-test")
	if err != nil {
		return false
	}
	//nolint:errcheck // Cleanup errors are acceptable in test
	defer os.RemoveAll(tmpDir)

	srcFile := filepath.Join(tmpDir, "src")
	dstFile := filepath.Join(tmpDir, "dst")

	if err := os.WriteFile(srcFile, []byte("test"), 0644); err != nil {
		return false
	}

	cmd := exec.Command("cp", "-c", srcFile, dstFile)
	if err := cmd.Run(); err != nil {
		// cp -c failed, fall back to regular copy
		return false
	}

	return true
}

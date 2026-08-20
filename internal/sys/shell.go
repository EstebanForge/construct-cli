package sys

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/EstebanForge/construct-cli/internal/ui"
	"github.com/EstebanForge/construct-cli/internal/update"
)

// EnsureCtSymlink silently creates ~/.local/bin/ct symlink if needed.
func EnsureCtSymlink() {
	target, err := buildCtTarget()
	if err != nil {
		return
	}

	// Check if ct command already exists in PATH
	ctPath, err := exec.LookPath("ct")
	if err == nil {
		// ct exists - check if it's pointing to our binary
		ctPathRaw := ctPath
		resolvedPath, err := filepath.EvalSymlinks(ctPath)
		if err != nil {
			// Failed to resolve, skip silently
			return
		}
		ctPath = resolvedPath
		if ctPath != target.resolved {
			if shouldReplaceBrewCt(ctPathRaw, ctPath) {
				replaceSymlink(ctPathRaw, target.path)
			}
			// ct exists but points to something else - silently skip
			// (user already has a ct command, don't interfere)
			return
		}
		// ct already points to our binary, all good
		return
	}

	// Try to create symlink in ~/.local/bin silently
	createSymlinkInLocalBin(target.path)
}

func createSymlinkInLocalBin(exePath string) bool {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return false
	}

	localBin := filepath.Join(homeDir, ".local", "bin")
	ctSymlink := filepath.Join(localBin, "ct")

	// Create ~/.local/bin if it doesn't exist
	if err := os.MkdirAll(localBin, 0755); err != nil {
		return false
	}

	// Check if symlink already exists
	if fileExists(ctSymlink) {
		// Check if it points to our binary
		targetResolved, err := filepath.EvalSymlinks(ctSymlink)
		exeResolved := exePath
		if resolved, resolveErr := filepath.EvalSymlinks(exePath); resolveErr == nil {
			exeResolved = resolved
		}
		if err == nil && targetResolved == exeResolved {
			return false // Already pointing to us
		}
		// Exists but points elsewhere - don't overwrite
		return false
	}

	// Create the symlink
	if err := os.Symlink(exePath, ctSymlink); err != nil {
		return false
	}

	// Ensure ~/.local/bin is in PATH (add to shell configs if needed)
	ensureLocalBinInPath(localBin)

	return true
}

type ctTarget struct {
	path     string
	resolved string
}

func buildCtTarget() (ctTarget, error) {
	// Try to find construct in PATH first (prefer installed version over local dev build)
	var exePath string
	pathCmd, err := exec.LookPath("construct")
	if err != nil {
		// Fall back to current executable if construct not in PATH
		pathCmd = ""
		exePath, err = os.Executable()
		if err != nil {
			return ctTarget{}, err
		}
		// Resolve symlinks to get real path for local builds
		exePath, err = filepath.EvalSymlinks(exePath)
		if err != nil {
			return ctTarget{}, err
		}
	} else {
		exePath = pathCmd
	}

	exeResolved := exePath
	if resolved, resolveErr := filepath.EvalSymlinks(exePath); resolveErr == nil {
		exeResolved = resolved
	}
	if update.IsBrewInstalled() {
		if preferred := preferredBrewConstructPath(pathCmd, exeResolved); preferred != "" {
			exePath = preferred
			if resolved, resolveErr := filepath.EvalSymlinks(preferred); resolveErr == nil {
				exeResolved = resolved
			} else {
				exeResolved = preferred
			}
		}
	}

	return ctTarget{path: exePath, resolved: exeResolved}, nil
}

// FixCtSymlink ensures ~/.local/bin/ct points to the current Construct binary.
func FixCtSymlink() (bool, string, error) {
	target, err := buildCtTarget()
	if err != nil {
		return false, "", err
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return false, "", err
	}

	localBin := filepath.Join(homeDir, ".local", "bin")
	ctSymlink := filepath.Join(localBin, "ct")

	if err := os.MkdirAll(localBin, 0755); err != nil {
		return false, "", err
	}

	if info, statErr := os.Lstat(ctSymlink); statErr == nil {
		if info.Mode()&os.ModeSymlink == 0 {
			return false, fmt.Sprintf("ct exists but is not a symlink: %s", ctSymlink), nil
		}

		targetResolved, err := filepath.EvalSymlinks(ctSymlink)
		if err == nil && targetResolved == target.resolved {
			return false, fmt.Sprintf("ct already points to %s", target.path), nil
		}

		if err := os.Remove(ctSymlink); err != nil {
			return false, "", err
		}
	}

	if err := os.Symlink(target.path, ctSymlink); err != nil {
		return false, "", err
	}

	ensureLocalBinInPath(localBin)
	return true, fmt.Sprintf("ct now points to %s", target.path), nil
}

func preferredBrewConstructPath(pathCmd, exeResolved string) string {
	if pathCmd != "" {
		switch pathCmd {
		case "/opt/homebrew/bin/construct", "/usr/local/bin/construct", "/home/linuxbrew/.linuxbrew/bin/construct":
			return pathCmd
		}
	}

	switch {
	case strings.Contains(exeResolved, "/opt/homebrew/Cellar/construct-cli/"):
		return "/opt/homebrew/bin/construct"
	case strings.Contains(exeResolved, "/usr/local/Cellar/construct-cli/"):
		return "/usr/local/bin/construct"
	case strings.Contains(exeResolved, "/home/linuxbrew/.linuxbrew/Cellar/construct-cli/"):
		return "/home/linuxbrew/.linuxbrew/bin/construct"
	default:
		return ""
	}
}

func shouldReplaceBrewCt(ctPath, ctResolved string) bool {
	if !update.IsBrewInstalled() {
		return false
	}
	if !isBrewCellarPath(ctResolved) {
		return false
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	localCt := filepath.Join(homeDir, ".local", "bin", "ct")
	return ctPath == localCt
}

func isBrewCellarPath(path string) bool {
	return strings.Contains(path, "/opt/homebrew/Cellar/construct-cli/") ||
		strings.Contains(path, "/usr/local/Cellar/construct-cli/") ||
		strings.Contains(path, "/home/linuxbrew/.linuxbrew/Cellar/construct-cli/")
}

func replaceSymlink(targetPath, exePath string) {
	if err := os.Remove(targetPath); err != nil {
		return
	}
	if err := os.Symlink(exePath, targetPath); err != nil {
		return
	}
}

func ensureLocalBinInPath(localBin string) {
	// Check if ~/.local/bin is already in PATH
	pathEnv := os.Getenv("PATH")
	if strings.Contains(pathEnv, localBin) {
		return // Already in PATH
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return
	}

	// Detect user's shell
	shell := os.Getenv("SHELL")
	if shell == "" {
		return
	}

	var configFile string
	var pathLine string

	// Determine config file and PATH export line based on shell
	if strings.Contains(shell, "zsh") {
		configFile = filepath.Join(homeDir, ".zshrc")
		pathLine = "\n# Add ~/.local/bin to PATH\nexport PATH=\"$HOME/.local/bin:$PATH\"\n"
	} else if strings.Contains(shell, "bash") {
		configFile = filepath.Join(homeDir, ".bashrc")
		if _, statErr := os.Stat(configFile); os.IsNotExist(statErr) {
			configFile = filepath.Join(homeDir, ".bash_profile")
		}
		pathLine = "\n# Add ~/.local/bin to PATH\nexport PATH=\"$HOME/.local/bin:$PATH\"\n"
	} else if strings.Contains(shell, "fish") {
		configFile = filepath.Join(homeDir, ".config/fish/config.fish")
		pathLine = "\n# Add ~/.local/bin to PATH\nset -gx PATH $HOME/.local/bin $PATH\n"
	} else {
		return
	}

	// Check if PATH line already exists
	if fileExists(configFile) {
		content, readErr := os.ReadFile(configFile)
		if readErr == nil && strings.Contains(string(content), ".local/bin") {
			return // Already added
		}
	}

	// Append PATH export to config file silently (ignore errors)
	f, err := os.OpenFile(configFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	//nolint:errcheck // Intentionally ignoring errors for silent operation
	defer f.Close()

	//nolint:errcheck // Intentionally ignoring errors for silent operation
	f.WriteString(pathLine)
}

func resolveAliasConstructCommand() (string, error) {
	// Best effort: keep ct symlink current before writing aliases.
	if _, _, err := FixCtSymlink(); err != nil {
		ui.LogDebug("Failed to refresh ct symlink before alias install: %v", err)
	}

	homeDir, err := os.UserHomeDir()
	if err == nil {
		localCt := filepath.Join(homeDir, ".local", "bin", "ct")
		if info, statErr := os.Stat(localCt); statErr == nil && !info.IsDir() {
			return localCt, nil
		}
	}

	exePath, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, resolveErr := filepath.EvalSymlinks(exePath); resolveErr == nil {
		exePath = resolved
	}
	return exePath, nil
}

func resolveBinaryPath(agentPath string) string {
	// Keep symlink-based shim paths (for example /opt/homebrew/bin/*) instead of
	// resolving to versioned Cellar/Caskroom locations. This keeps ns-* aliases
	// stable across package updates.
	resolvedPath := agentPath
	if absPath, err := filepath.Abs(agentPath); err == nil {
		resolvedPath = absPath
	}
	return resolvedPath
}

type shellInfo struct {
	configFile string
	shellType  string
}

func getShellInfo() (shellInfo, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return shellInfo{}, err
	}

	shell := os.Getenv("SHELL")
	if shell == "" {
		return shellInfo{}, fmt.Errorf("no shell detected")
	}

	if strings.Contains(shell, "zsh") {
		return shellInfo{configFile: filepath.Join(homeDir, ".zshrc"), shellType: "zsh"}, nil
	} else if strings.Contains(shell, "bash") {
		configFile := filepath.Join(homeDir, ".bashrc")
		if _, statErr := os.Stat(configFile); os.IsNotExist(statErr) {
			return shellInfo{configFile: filepath.Join(homeDir, ".bash_profile"), shellType: "bash"}, nil
		}
		return shellInfo{configFile: configFile, shellType: "bash"}, nil
	} else if strings.Contains(shell, "fish") {
		return shellInfo{configFile: filepath.Join(homeDir, ".config/fish/config.fish"), shellType: "fish"}, nil
	}

	return shellInfo{}, fmt.Errorf("unsupported shell: %s", shell)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// backupConfigFile creates a timestamped backup of the config file
func backupConfigFile(configFile string) error {
	// Only backup if file exists
	if !fileExists(configFile) {
		return nil
	}

	// Create backup filename with timestamp
	timestamp := time.Now().Format("20060102-150405")
	backupPath := configFile + ".backup-" + timestamp

	// Read original file
	content, err := os.ReadFile(configFile)
	if err != nil {
		return fmt.Errorf("failed to read config file for backup: %w", err)
	}

	// Write backup
	if err := os.WriteFile(backupPath, content, 0644); err != nil {
		return fmt.Errorf("failed to create backup: %w", err)
	}

	// Show backup location to user
	displayPath := backupPath
	if homeDir, err := os.UserHomeDir(); err == nil {
		displayPath = strings.Replace(backupPath, homeDir, "~", 1)
	}
	if ui.GumAvailable() {
		fmt.Printf("%s  ✓ Backup created: %s%s\n", ui.ColorGrey, displayPath, ui.ColorReset)
	} else {
		fmt.Printf("  ✓ Backup created: %s\n", displayPath)
	}

	return nil
}

// aliasBlockStart/aliasBlockEnd delimit the managed alias block that older
// Construct releases wrote into shell rc files. The alias system is gone
// (replaced by `construct sys shims`); these markers remain only to clean up.
const (
	aliasBlockStart = "# construct-cli aliases start"
	aliasBlockEnd   = "# construct-cli aliases end"
)

// RemoveLegacyAliasBlock removes the managed alias block from the user's
// shell config without prompting. It backs the file up first. It is called
// by `construct sys shims --install` so users migrating from aliases to
// shims do not keep both layers. Hand-written aliases and functions outside
// the markers are never touched.
func RemoveLegacyAliasBlock() (removed bool, configFile string, err error) {
	info, err := getShellInfo()
	if err != nil {
		return false, "", err
	}
	configFile = info.configFile

	contentBytes, readErr := os.ReadFile(configFile)
	if readErr != nil {
		if os.IsNotExist(readErr) {
			return false, configFile, nil
		}
		return false, configFile, readErr
	}
	content := string(contentBytes)

	startIdx := strings.Index(content, aliasBlockStart)
	endIdx := strings.Index(content, aliasBlockEnd)
	if startIdx == -1 || endIdx == -1 || endIdx < startIdx {
		return false, configFile, nil
	}

	if backupErr := backupConfigFile(configFile); backupErr != nil {
		return false, configFile, backupErr
	}

	endLineIdx := endIdx + len(aliasBlockEnd)
	if offset := strings.Index(content[endIdx:], "\n"); offset != -1 {
		endLineIdx = endIdx + offset + 1
	}
	newContent := content[:startIdx] + content[endLineIdx:]
	if writeErr := os.WriteFile(configFile, []byte(newContent), 0644); writeErr != nil {
		return false, configFile, writeErr
	}
	return true, configFile, nil
}

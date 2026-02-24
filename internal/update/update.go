// Package update handles update checks and notifications.
package update

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/EstebanForge/construct-cli/internal/config"
	"github.com/EstebanForge/construct-cli/internal/constants"
	"github.com/EstebanForge/construct-cli/internal/ui"
	semver "github.com/EstebanForge/construct-cli/internal/version"
)

const (
	updateChannelStable = "stable"
	updateChannelBeta   = "beta"
	binaryName          = "construct"
	aliasName           = "ct"
)

// ShouldCheckForUpdates reports whether the update interval has elapsed.
func ShouldCheckForUpdates(cfg *config.Config) bool {
	if !cfg.Runtime.AutoUpdateCheck {
		return false
	}

	checkFile := filepath.Join(config.GetConfigDir(), constants.UpdateCheckFile)
	info, err := os.Stat(checkFile)
	if os.IsNotExist(err) {
		return true
	}

	// Default interval: 24 hours
	interval := time.Duration(cfg.Runtime.UpdateCheckInterval) * time.Second
	if interval == 0 {
		interval = 24 * time.Hour
	}

	return time.Since(info.ModTime()) > interval
}

// CheckForUpdates checks the configured release channel version file for the latest version.
// Returns the latest version string, whether an update is available, and any error.
func CheckForUpdates(cfg ...*config.Config) (string, bool, error) {
	channel := resolveUpdateChannel(cfg...)
	versionURL := versionURLForChannel(channel)

	resp, err := http.Get(versionURL)
	if err != nil {
		return "", false, err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			ui.LogWarning("Failed to close response body: %v", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return "", false, fmt.Errorf("failed to fetch %s version file: %s", channel, resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", false, err
	}

	latestVer := strings.TrimSpace(string(body))
	currentVer := strings.TrimPrefix(constants.Version, "v")
	latestVer = strings.TrimPrefix(latestVer, "v")

	// Only report update available if remote version > current version
	if compareVersions(latestVer, currentVer) > 0 {
		return latestVer, true, nil
	}

	return latestVer, false, nil
}

// compareVersions compares two semver strings
// Returns: 1 if v1 > v2, -1 if v1 < v2, 0 if equal
func compareVersions(v1, v2 string) int {
	return semver.Compare(v1, v2)
}

func resolveUpdateChannel(cfg ...*config.Config) string {
	if len(cfg) == 0 || cfg[0] == nil {
		return updateChannelStable
	}

	switch strings.ToLower(strings.TrimSpace(cfg[0].Runtime.UpdateChannel)) {
	case "", updateChannelStable:
		return updateChannelStable
	case updateChannelBeta:
		return updateChannelBeta
	default:
		return updateChannelStable
	}
}

func versionURLForChannel(channel string) string {
	if channel == updateChannelBeta {
		return constants.GithubRawBetaURL
	}
	return constants.GithubRawURL
}

// RecordUpdateCheck updates the update-check timestamp file.
func RecordUpdateCheck() {
	checkFile := filepath.Join(config.GetConfigDir(), constants.UpdateCheckFile)
	checkDir := filepath.Dir(checkFile)
	if err := os.MkdirAll(checkDir, 0755); err != nil {
		ui.LogWarning("Failed to create update-check directory: %v", err)
		return
	}

	// Ensure file exists before updating timestamps.
	if _, err := os.Stat(checkFile); os.IsNotExist(err) {
		if err := os.WriteFile(checkFile, []byte{}, 0644); err != nil {
			ui.LogWarning("Failed to write update-check file: %v", err)
			return
		}
	} else if err != nil {
		ui.LogWarning("Failed to stat update-check file: %v", err)
		return
	}

	now := time.Now()
	if err := os.Chtimes(checkFile, now, now); err != nil {
		ui.LogWarning("Failed to update update-check timestamp: %v", err)
	}
}

// DisplayNotification prints an update notification.
func DisplayNotification(latestVersion string) {
	msg := fmt.Sprintf("New version available: %s (current: %s)", latestVersion, constants.Version)
	if ui.GumAvailable() {
		ui.GumInfo(msg)
		if IsBrewInstalled() {
			ui.GumInfo("Run 'construct sys self-update' to upgrade (installs user-local override)")
		} else {
			ui.GumInfo("Run 'construct sys self-update' to upgrade")
		}
	} else {
		fmt.Println()
		fmt.Println(msg)
		if IsBrewInstalled() {
			fmt.Println("Run 'construct sys self-update' to upgrade (installs user-local override)")
		} else {
			fmt.Println("Run 'construct sys self-update' to upgrade")
		}
		fmt.Println()
	}
}

// IsBrewInstalled checks if the current binary was installed via Homebrew
func IsBrewInstalled() bool {
	execPath, err := os.Executable()
	if err != nil {
		return false
	}

	// Resolve symlinks to see the actual binary location
	realPath, err := filepath.EvalSymlinks(execPath)
	if err != nil {
		realPath = execPath
	}

	// Check if the binary is in a Homebrew Cellar
	// On macOS Apple Silicon: /opt/homebrew/Cellar/...
	// On macOS Intel: /usr/local/Cellar/...
	// On Linux: /home/linuxbrew/.linuxbrew/Cellar/...
	return strings.Contains(realPath, "Cellar/construct-cli") ||
		strings.Contains(realPath, ".linuxbrew/Cellar/construct-cli")
}

// SelfUpdate downloads and installs the latest version of the binary
func SelfUpdate(cfg ...*config.Config) error {
	// Check for latest version
	if ui.GumAvailable() {
		ui.GumInfo("Checking for updates...")
	} else {
		fmt.Println("Checking for updates...")
	}

	latestVersion, updateAvailable, err := CheckForUpdates(cfg...)
	if err != nil {
		return fmt.Errorf("failed to check for updates: %w", err)
	}

	// If no update available, ask user if they want to reinstall
	if !updateAvailable {
		if ui.GumAvailable() {
			ui.GumSuccess(fmt.Sprintf("Already on latest version: %s", constants.Version))
			if !ui.GumConfirm("Do you want to reinstall the current version?") {
				fmt.Println("Update canceled.")
				return nil
			}
		} else {
			fmt.Printf("Already on latest version: %s\n", constants.Version)
			fmt.Print("Do you want to reinstall the current version? [y/N]: ")
			var response string
			if _, err := fmt.Scanln(&response); err != nil {
				// If scan fails (e.g. empty input/newline), assume no
				response = "n"
			}
			response = strings.ToLower(strings.TrimSpace(response))
			if response != "y" && response != "yes" {
				fmt.Println("Update canceled.")
				return nil
			}
		}
	}

	// Detect platform
	platform := fmt.Sprintf("%s-%s", runtime.GOOS, runtime.GOARCH)
	if ui.GumAvailable() {
		ui.GumInfo(fmt.Sprintf("Platform: %s", platform))
		ui.GumInfo(fmt.Sprintf("Version: %s → %s", constants.Version, latestVersion))
	} else {
		fmt.Printf("Platform: %s\n", platform)
		fmt.Printf("Version: %s → %s\n", constants.Version, latestVersion)
	}

	// Construct download URL directly (no API call needed)
	assetName := fmt.Sprintf("construct-%s-%s.tar.gz", platform, latestVersion)
	downloadURL := fmt.Sprintf("https://github.com/%s/releases/download/%s/%s",
		constants.GithubRepo, latestVersion, assetName)

	// Download the archive
	if ui.GumAvailable() {
		ui.GumInfo(fmt.Sprintf("Downloading %s...", assetName))
	} else {
		fmt.Printf("Downloading %s...\n", assetName)
	}

	tmpFile, err := downloadFile(downloadURL)
	if err != nil {
		return fmt.Errorf("failed to download update: %w", err)
	}
	defer func() {
		if err := os.Remove(tmpFile); err != nil {
			ui.LogWarning("Failed to remove temp file: %v", err)
		}
	}()

	// Extract binary from archive
	binaryPath, err := extractBinary(tmpFile, platform)
	if err != nil {
		return fmt.Errorf("failed to extract binary: %w", err)
	}
	defer func() {
		if err := os.Remove(binaryPath); err != nil {
			ui.LogWarning("Failed to remove extracted binary: %v", err)
		}
	}()

	// Get current executable path
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		return fmt.Errorf("failed to resolve executable path: %w", err)
	}

	targetPath, userLocalOverride, err := resolveInstallTarget(execPath)
	if err != nil {
		return fmt.Errorf("failed to resolve install target: %w", err)
	}

	if ui.GumAvailable() {
		ui.GumInfo(fmt.Sprintf("Install target: %s", displayPath(targetPath)))
	} else {
		fmt.Printf("Install target: %s\n", displayPath(targetPath))
	}

	if err := installBinaryWithBackup(binaryPath, targetPath); err != nil {
		// For non-brew installs in protected paths, fall back to user-local location.
		if !userLocalOverride && isPermissionError(err) {
			fallbackPath, pathErr := userLocalBinaryPath()
			if pathErr != nil {
				return fmt.Errorf("failed to install update: %w", err)
			}
			if ui.GumAvailable() {
				ui.GumWarning("No write permission to current binary location.")
				ui.GumInfo(fmt.Sprintf("Falling back to user-local install: %s", displayPath(fallbackPath)))
			} else {
				fmt.Println("Warning: No write permission to current binary location.")
				fmt.Printf("Falling back to user-local install: %s\n", displayPath(fallbackPath))
			}
			if fallbackErr := installBinaryWithBackup(binaryPath, fallbackPath); fallbackErr != nil {
				return fmt.Errorf("failed to install update: %w (fallback failed: %v)", err, fallbackErr)
			}
			targetPath = fallbackPath
			userLocalOverride = true
		} else {
			return fmt.Errorf("failed to install update: %w", err)
		}
	}

	// Note: We used to delete the .version file here to force migration,
	// but that causes confusing "0.3.0 -> current" messages.
	// Template migrations now trigger naturally via hash-based detection
	// in the migration package, so keeping the version file is safer.
	if userLocalOverride {
		if err := configureUserLocalOverride(targetPath); err != nil {
			ui.LogWarning("Failed to finalize user-local override: %v", err)
		}
	}

	if ui.GumAvailable() {
		ui.GumSuccess(fmt.Sprintf("Successfully updated to %s", latestVersion))
		if userLocalOverride {
			ui.GumInfo(fmt.Sprintf("Now using user-local binary: %s", displayPath(targetPath)))
			ui.GumInfo("If needed, run: export PATH=\"$HOME/.local/bin:$PATH\"")
		}
	} else {
		fmt.Printf("✓ Successfully updated to %s\n", latestVersion)
		if userLocalOverride {
			fmt.Printf("Now using user-local binary: %s\n", displayPath(targetPath))
			fmt.Println("If needed, run: export PATH=\"$HOME/.local/bin:$PATH\"")
		}
	}

	return nil
}

func resolveInstallTarget(execPath string) (string, bool, error) {
	if isBrewCellarPath(execPath) {
		targetPath, err := userLocalBinaryPath()
		if err != nil {
			return "", false, err
		}
		return targetPath, true, nil
	}
	return execPath, false, nil
}

func userLocalBinaryPath() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(homeDir, ".local", "bin", binaryName), nil
}

func isBrewCellarPath(path string) bool {
	return strings.Contains(path, "/opt/homebrew/Cellar/construct-cli/") ||
		strings.Contains(path, "/usr/local/Cellar/construct-cli/") ||
		strings.Contains(path, "/home/linuxbrew/.linuxbrew/Cellar/construct-cli/")
}

func installBinaryWithBackup(srcPath, targetPath string) error {
	targetDir := filepath.Dir(targetPath)
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("failed to create install directory: %w", err)
	}

	backupPath := targetPath + ".backup"
	hasBackup := false

	if _, err := os.Stat(targetPath); err == nil {
		if err := os.Rename(targetPath, backupPath); err != nil {
			return fmt.Errorf("failed to backup current binary: %w", err)
		}
		hasBackup = true
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("failed to inspect current binary: %w", err)
	}

	if err := copyFile(srcPath, targetPath, 0755); err != nil {
		if hasBackup {
			if removeErr := os.Remove(targetPath); removeErr != nil && !os.IsNotExist(removeErr) {
				ui.LogWarning("Failed to remove partial binary during rollback: %v", removeErr)
			}
			if renameErr := os.Rename(backupPath, targetPath); renameErr != nil {
				ui.LogError(fmt.Errorf("CRITICAL: Failed to restore backup after update failure: %v", renameErr))
			}
		}
		return fmt.Errorf("failed to install new binary: %w", err)
	}

	if hasBackup {
		if err := os.Remove(backupPath); err != nil {
			ui.LogWarning("Failed to remove backup file: %v", err)
		}
	}

	return nil
}

func isPermissionError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, os.ErrPermission) || os.IsPermission(err) {
		return true
	}
	var pathErr *os.PathError
	if errors.As(err, &pathErr) && (errors.Is(pathErr.Err, os.ErrPermission) || os.IsPermission(pathErr.Err)) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "permission denied")
}

func configureUserLocalOverride(binaryPath string) error {
	localBin := filepath.Dir(binaryPath)
	if err := ensureLocalBinInPath(localBin); err != nil {
		return err
	}
	ensureLocalAlias(binaryPath, localBin)
	return nil
}

func ensureLocalAlias(binaryPath, localBin string) {
	aliasPath := filepath.Join(localBin, aliasName)
	desiredResolved := binaryPath
	if resolved, err := filepath.EvalSymlinks(binaryPath); err == nil {
		desiredResolved = resolved
	}

	if info, err := os.Lstat(aliasPath); err == nil {
		if info.Mode()&os.ModeSymlink == 0 {
			return
		}
		currentResolved := aliasPath
		if resolved, resolveErr := filepath.EvalSymlinks(aliasPath); resolveErr == nil {
			currentResolved = resolved
		}
		if currentResolved == desiredResolved {
			return
		}
		if !isBrewCellarPath(currentResolved) {
			return
		}
		if err := os.Remove(aliasPath); err != nil {
			return
		}
	} else if !os.IsNotExist(err) {
		return
	}

	if err := os.Symlink(binaryPath, aliasPath); err != nil {
		return
	}
}

func ensureLocalBinInPath(localBin string) error {
	pathEnv := os.Getenv("PATH")
	if strings.HasPrefix(pathEnv, localBin+":") || pathEnv == localBin {
		return nil
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	shell := os.Getenv("SHELL")
	if shell == "" {
		return nil
	}

	var configFile string
	var pathLine string
	var marker string

	if strings.Contains(shell, "zsh") {
		configFile = filepath.Join(homeDir, ".zshrc")
		pathLine = "\n# Prefer Construct user-local binary\nexport PATH=\"$HOME/.local/bin:$PATH\"\n"
		marker = "Prefer Construct user-local binary"
	} else if strings.Contains(shell, "bash") {
		configFile = filepath.Join(homeDir, ".bashrc")
		if _, statErr := os.Stat(configFile); os.IsNotExist(statErr) {
			configFile = filepath.Join(homeDir, ".bash_profile")
		}
		pathLine = "\n# Prefer Construct user-local binary\nexport PATH=\"$HOME/.local/bin:$PATH\"\n"
		marker = "Prefer Construct user-local binary"
	} else if strings.Contains(shell, "fish") {
		configFile = filepath.Join(homeDir, ".config", "fish", "config.fish")
		pathLine = "\n# Prefer Construct user-local binary\nset -gx PATH $HOME/.local/bin $PATH\n"
		marker = "Prefer Construct user-local binary"
	} else {
		return nil
	}

	if content, readErr := os.ReadFile(configFile); readErr == nil && strings.Contains(string(content), marker) {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(configFile), 0755); err != nil {
		return err
	}

	f, err := os.OpenFile(configFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer func() {
		if err := f.Close(); err != nil {
			ui.LogWarning("Failed to close shell config file: %v", err)
		}
	}()

	if _, err := f.WriteString(pathLine); err != nil {
		return err
	}

	return nil
}

func displayPath(path string) string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if strings.HasPrefix(path, homeDir+"/") {
		return "~/" + strings.TrimPrefix(path, homeDir+"/")
	}
	return path
}

// downloadFile downloads a file and returns the path to the temp file
func downloadFile(url string) (string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			ui.LogWarning("Failed to close response body: %v", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download failed with status: %s", resp.Status)
	}

	tmpFile, err := os.CreateTemp("", "construct-update-*.tar.gz")
	if err != nil {
		return "", err
	}
	defer func() {
		if err := tmpFile.Close(); err != nil {
			ui.LogWarning("Failed to close temp file: %v", err)
		}
	}()

	if _, err := io.Copy(tmpFile, resp.Body); err != nil {
		if removeErr := os.Remove(tmpFile.Name()); removeErr != nil {
			ui.LogWarning("Failed to remove temp file after copy error: %v", removeErr)
		}
		return "", err
	}

	return tmpFile.Name(), nil
}

// extractBinary extracts the construct binary from the tar.gz archive
func extractBinary(archivePath, platform string) (string, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return "", err
	}
	defer func() {
		if err := f.Close(); err != nil {
			ui.LogWarning("Failed to close archive file: %v", err)
		}
	}()

	gzr, err := gzip.NewReader(f)
	if err != nil {
		return "", err
	}
	defer func() {
		if err := gzr.Close(); err != nil {
			ui.LogWarning("Failed to close gzip reader: %v", err)
		}
	}()

	tr := tar.NewReader(gzr)

	// Look for the binary in the archive
	expectedNames := []string{
		fmt.Sprintf("construct-%s", platform),
		"construct",
	}

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}

		// Check if this is the binary we're looking for
		baseName := filepath.Base(header.Name)
		found := false
		for _, name := range expectedNames {
			if baseName == name {
				found = true
				break
			}
		}

		if !found {
			continue
		}

		// Extract to temp file
		tmpFile, err := os.CreateTemp("", "construct-binary-*")
		if err != nil {
			return "", err
		}

		if _, err := io.Copy(tmpFile, tr); err != nil {
			if closeErr := tmpFile.Close(); closeErr != nil {
				ui.LogWarning("Failed to close temp file after copy error: %v", closeErr)
			}
			if removeErr := os.Remove(tmpFile.Name()); removeErr != nil {
				ui.LogWarning("Failed to remove temp file after copy error: %v", removeErr)
			}
			return "", err
		}
		if err := tmpFile.Close(); err != nil {
			ui.LogWarning("Failed to close extracted binary file: %v", err)
		}

		// Make executable
		if err := os.Chmod(tmpFile.Name(), 0755); err != nil {
			if removeErr := os.Remove(tmpFile.Name()); removeErr != nil {
				ui.LogWarning("Failed to remove temp file after chmod error: %v", removeErr)
			}
			return "", err
		}

		return tmpFile.Name(), nil
	}

	return "", fmt.Errorf("binary not found in archive")
}

// copyFile copies a file from src to dst with the given permissions
func copyFile(src, dst string, perm os.FileMode) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() {
		if err := srcFile.Close(); err != nil {
			ui.LogWarning("Failed to close source file during copy: %v", err)
		}
	}()

	dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	defer func() {
		if err := dstFile.Close(); err != nil {
			ui.LogWarning("Failed to close destination file during copy: %v", err)
		}
	}()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return err
	}

	return dstFile.Chmod(perm)
}

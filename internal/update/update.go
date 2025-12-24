// Package update handles update checks and notifications.
package update

import (
	"archive/tar"
	"compress/gzip"
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

// CheckForUpdates checks the VERSION file for the latest version.
// Returns the latest version string, whether an update is available, and any error.
func CheckForUpdates() (string, bool, error) {
	resp, err := http.Get(constants.GithubRawURL)
	if err != nil {
		return "", false, err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			ui.LogWarning("Failed to close response body: %v", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return "", false, fmt.Errorf("failed to fetch VERSION: %s", resp.Status)
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
	v1Parts := strings.Split(v1, ".")
	v2Parts := strings.Split(v2, ".")

	for i := 0; i < 3; i++ {
		var n1, n2 int
		if i < len(v1Parts) {
			if _, err := fmt.Sscanf(v1Parts[i], "%d", &n1); err != nil {
				n1 = 0
			}
		}
		if i < len(v2Parts) {
			if _, err := fmt.Sscanf(v2Parts[i], "%d", &n2); err != nil {
				n2 = 0
			}
		}

		if n1 > n2 {
			return 1
		} else if n1 < n2 {
			return -1
		}
	}
	return 0
}

// RecordUpdateCheck updates the update-check timestamp file.
func RecordUpdateCheck() {
	checkFile := filepath.Join(config.GetConfigDir(), constants.UpdateCheckFile)
	now := time.Now()
	if err := os.Chtimes(checkFile, now, now); err != nil {
		ui.LogWarning("Failed to update update-check timestamp: %v", err)
	}

	// Ensure file exists
	if _, err := os.Stat(checkFile); os.IsNotExist(err) {
		if err := os.WriteFile(checkFile, []byte{}, 0644); err != nil {
			ui.LogWarning("Failed to write update-check file: %v", err)
		}
	}
}

// DisplayNotification prints an update notification.
func DisplayNotification(latestVersion string) {
	msg := fmt.Sprintf("New version available: %s (current: %s)", latestVersion, constants.Version)
	if ui.GumAvailable() {
		ui.GumInfo(msg)
		ui.GumInfo("Run 'construct sys self-update' to upgrade")
	} else {
		fmt.Println()
		fmt.Println(msg)
		fmt.Println("Run 'construct sys self-update' to upgrade")
		fmt.Println()
	}
}

// SelfUpdate downloads and installs the latest version of the binary
func SelfUpdate() error {
	// Check for latest version
	if ui.GumAvailable() {
		ui.GumInfo("Checking for updates...")
	} else {
		fmt.Println("Checking for updates...")
	}

	latestVersion, updateAvailable, err := CheckForUpdates()
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

	// Backup current binary
	backupPath := execPath + ".backup"
	if err := os.Rename(execPath, backupPath); err != nil {
		return fmt.Errorf("failed to backup current binary: %w", err)
	}

	// Install new binary
	if err := copyFile(binaryPath, execPath, 0755); err != nil {
		// Restore backup on failure
		if renameErr := os.Rename(backupPath, execPath); renameErr != nil {
			ui.LogError(fmt.Errorf("CRITICAL: Failed to restore backup after update failure: %v", renameErr))
		}
		return fmt.Errorf("failed to install new binary: %w", err)
	}

	// Remove backup
	if err := os.Remove(backupPath); err != nil {
		ui.LogWarning("Failed to remove backup file: %v", err)
	}

	if ui.GumAvailable() {
		ui.GumSuccess(fmt.Sprintf("Successfully updated to %s", latestVersion))
	} else {
		fmt.Printf("✓ Successfully updated to %s\n", latestVersion)
	}

	return nil
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

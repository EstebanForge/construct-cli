// Package update handles update checks and notifications.
package update

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/EstebanForge/construct-cli/internal/config"
	"github.com/EstebanForge/construct-cli/internal/constants"
	"github.com/EstebanForge/construct-cli/internal/ui"
)

// GitHubRelease represents a GitHub release response
type GitHubRelease struct {
	TagName string `json:"tag_name"`
	Body    string `json:"body"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

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

// CheckForUpdates queries GitHub for the latest release.
func CheckForUpdates() (*GitHubRelease, bool, error) {
	resp, err := http.Get(constants.GithubAPIURL)
	if err != nil {
		return nil, false, err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			ui.LogWarning("Failed to close update response body: %v", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("GitHub API returned status: %s", resp.Status)
	}

	var release GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, false, err
	}

	// Compare versions (simple string comparison for now, assuming vX.Y.Z)
	// Remove 'v' prefix if present
	latestVer := release.TagName
	if len(latestVer) > 0 && latestVer[0] == 'v' {
		latestVer = latestVer[1:]
	}

	currentVer := constants.Version
	if len(currentVer) > 0 && currentVer[0] == 'v' {
		currentVer = currentVer[1:]
	}

	if latestVer != currentVer {
		return &release, true, nil
	}

	return nil, false, nil
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
func DisplayNotification(release *GitHubRelease) {
	msg := fmt.Sprintf("New version available: %s (current: %s)", release.TagName, constants.Version)
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

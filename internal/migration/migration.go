// Package migration handles configuration and template migrations.
package migration

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/EstebanForge/construct-cli/internal/config"
	"github.com/EstebanForge/construct-cli/internal/constants"
	"github.com/EstebanForge/construct-cli/internal/templates"
	"github.com/EstebanForge/construct-cli/internal/ui"
)

const versionFile = ".version"
const configTemplateHashFile = ".config_template_hash"

// GetInstalledVersion returns the currently installed version from the version file
func GetInstalledVersion() string {
	versionPath := filepath.Join(config.GetConfigDir(), versionFile)
	data, err := os.ReadFile(versionPath)
	if err != nil {
		return "" // No version file means fresh install or very old version
	}
	return strings.TrimSpace(string(data))
}

// SetInstalledVersion writes the current version to the version file
func SetInstalledVersion(version string) error {
	versionPath := filepath.Join(config.GetConfigDir(), versionFile)
	return os.WriteFile(versionPath, []byte(version+"\n"), 0644)
}

// NeedsMigration checks if migration is needed
func NeedsMigration() bool {
	installed := GetInstalledVersion()
	current := constants.Version

	// If no version file exists, check if this is an upgrade from 0.3.0 or a fresh install
	if installed == "" {
		// Check if config.toml exists - if it does, this is an upgrade from 0.3.0
		configPath := filepath.Join(config.GetConfigDir(), "config.toml")
		if _, err := os.Stat(configPath); err == nil {
			// Config exists but no version file - must be 0.3.0 upgrade
			return true
		}
		// No config file either - this is a fresh install
		return false
	}

	// Same version - no migration needed
	if installed == current {
		return false
	}

	// Simple version comparison (assumes semver X.Y.Z format)
	return compareVersions(current, installed) > 0
}

// compareVersions compares two semver strings
// Returns: 1 if v1 > v2, -1 if v1 < v2, 0 if equal
func compareVersions(v1, v2 string) int {
	v1Parts := strings.Split(strings.TrimPrefix(v1, "v"), ".")
	v2Parts := strings.Split(strings.TrimPrefix(v2, "v"), ".")

	for i := 0; i < 3; i++ {
		var n1, n2 int
		if i < len(v1Parts) {
			if parsed, err := strconv.Atoi(v1Parts[i]); err == nil {
				n1 = parsed
			}
		}
		if i < len(v2Parts) {
			if parsed, err := strconv.Atoi(v2Parts[i]); err == nil {
				n2 = parsed
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

// getConfigTemplateHash returns SHA256 hash of the embedded config template
func getConfigTemplateHash() string {
	hash := sha256.Sum256([]byte(templates.Config))
	return hex.EncodeToString(hash[:])
}

// configTemplateChanged checks if embedded config template differs from last applied
func configTemplateChanged() bool {
	hashPath := filepath.Join(config.GetConfigDir(), configTemplateHashFile)

	storedHash, err := os.ReadFile(hashPath)
	if err != nil {
		// No hash file - either fresh install or upgrade from old version
		// If config.toml exists, assume template changed (be conservative)
		configPath := filepath.Join(config.GetConfigDir(), "config.toml")
		if _, err := os.Stat(configPath); err == nil {
			return true
		}
		return false
	}

	return strings.TrimSpace(string(storedHash)) != getConfigTemplateHash()
}

// saveConfigTemplateHash stores the current template hash
func saveConfigTemplateHash() error {
	hashPath := filepath.Join(config.GetConfigDir(), configTemplateHashFile)
	return os.WriteFile(hashPath, []byte(getConfigTemplateHash()+"\n"), 0644)
}

// RunMigrations performs all necessary migrations
func RunMigrations() error {
	installed := GetInstalledVersion()
	// If no version file, assume upgrade from 0.3.0
	if installed == "" {
		installed = "0.3.0"
	}
	current := constants.Version

	if ui.GumAvailable() {
		ui.GumSuccess(fmt.Sprintf("Upgrading configuration: %s → %s", installed, current))
	} else {
		fmt.Printf("✓ Upgrading configuration: %s → %s\n", installed, current)
	}

	// 1. Update container templates (always - may have bug fixes)
	if err := updateContainerTemplates(); err != nil {
		return fmt.Errorf("failed to update container templates: %w", err)
	}

	// 2. Merge config.toml only if template structure changed
	if configTemplateChanged() {
		if err := mergeConfigFile(); err != nil {
			return fmt.Errorf("failed to merge config file: %w", err)
		}
		if err := saveConfigTemplateHash(); err != nil {
			return fmt.Errorf("failed to save config template hash: %w", err)
		}
	} else {
		if ui.GumAvailable() {
			fmt.Printf("%s  → Config structure unchanged, skipping merge%s\n", ui.ColorGrey, ui.ColorReset)
		} else {
			fmt.Println("  → Config structure unchanged, skipping merge")
		}
	}

	// 3. Mark image for rebuild
	markImageForRebuild()

	// 4. Update installed version
	if err := SetInstalledVersion(current); err != nil {
		return fmt.Errorf("failed to update version file: %w", err)
	}

	if ui.GumAvailable() {
		ui.GumSuccess("Migration complete!")
		fmt.Printf("%s  Note: Container image will rebuild on next agent run%s\n", ui.ColorGrey, ui.ColorReset)
	} else {
		fmt.Println("✓ Migration complete!")
		fmt.Println("  Note: Container image will rebuild on next agent run")
	}

	return nil
}

// updateContainerTemplates replaces all container template files with new versions
func updateContainerTemplates() error {
	containerDir := filepath.Join(config.GetConfigDir(), "container")

	if ui.GumAvailable() {
		fmt.Printf("%sUpdating container templates...%s\n", ui.ColorCyan, ui.ColorReset)
	} else {
		fmt.Println("→ Updating container templates...")
	}

	// Container templates (safe to replace - no user modifications expected)
	containerFiles := map[string]string{
		"Dockerfile":         templates.Dockerfile,
		"docker-compose.yml": templates.DockerCompose,
		"entrypoint.sh":      templates.Entrypoint,
		"update-all.sh":      templates.UpdateAll,
		"network-filter.sh":  templates.NetworkFilter,
		"clipper":            templates.Clipper,
		"osascript":          templates.Osascript,
	}

	for filename, content := range containerFiles {
		path := filepath.Join(containerDir, filename)
		perm := os.FileMode(0644)
		if strings.HasSuffix(filename, ".sh") || filename == "clipper" || filename == "osascript" {
			perm = 0755
		}
		if err := os.WriteFile(path, []byte(content), perm); err != nil {
			return fmt.Errorf("failed to write %s: %w", filename, err)
		}
	}

	if ui.GumAvailable() {
		fmt.Printf("%s  ✓ Container templates updated%s\n", ui.ColorPink, ui.ColorReset)
	} else {
		fmt.Println("  ✓ Container templates updated")
	}

	return nil
}

// mergeConfigFile replaces config.toml with the template and reapplies user values.
// Preserves template layout/comments while copying supported values.
func mergeConfigFile() error {
	configPath := filepath.Join(config.GetConfigDir(), "config.toml")
	backupPath := configPath + ".backup"

	if ui.GumAvailable() {
		fmt.Printf("%sMerging configuration file...%s\n", ui.ColorCyan, ui.ColorReset)
	} else {
		fmt.Println("→ Merging configuration file...")
	}

	if _, err := os.Stat(backupPath); err == nil {
		if err := os.Remove(backupPath); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Failed to remove backup: %v\n", err)
		}
	}

	// Backup current config if present
	if _, err := os.Stat(configPath); err == nil {
		if err := os.Rename(configPath, backupPath); err != nil {
			return fmt.Errorf("failed to backup config: %w", err)
		}
		if ui.GumAvailable() {
			fmt.Printf("%s  → Backup saved: %s%s\n", ui.ColorGrey, backupPath, ui.ColorReset)
		} else {
			fmt.Printf("  → Backup saved: %s\n", backupPath)
		}
	}

	// Write fresh template config
	templateData := []byte(templates.Config)
	if err := os.WriteFile(configPath, templateData, 0644); err != nil {
		return fmt.Errorf("failed to write template config: %w", err)
	}

	// Apply user values from backup onto template
	if backupData, err := os.ReadFile(backupPath); err == nil {
		mergedData, err := mergeTemplateWithBackup(templateData, backupData)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Failed to merge user config values: %v\n", err)
		} else {
			if err := os.WriteFile(configPath, mergedData, 0644); err != nil {
				return fmt.Errorf("failed to write merged config: %w", err)
			}
		}
	}

	if ui.GumAvailable() {
		fmt.Printf("%s  ✓ Configuration merged (user settings preserved)%s\n", ui.ColorPink, ui.ColorReset)
	} else {
		fmt.Println("  ✓ Configuration merged (user settings preserved)")
	}

	return nil
}

// markImageForRebuild removes the old Docker image to force rebuild on next run
func markImageForRebuild() {
	if ui.GumAvailable() {
		fmt.Printf("%sRemoving old container image...%s\n", ui.ColorCyan, ui.ColorReset)
	} else {
		fmt.Println("→ Removing old container image...")
	}

	// Remove the old image so it gets rebuilt with new Dockerfile
	// We try both docker and podman since we don't know which runtime is in use
	// Errors are silently ignored - if image doesn't exist, that's fine
	imageName := "construct-box:latest"

	// Try docker
	if err := exec.Command("docker", "rmi", "-f", imageName).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to remove Docker image: %v\n", err)
	}

	// Try podman
	if err := exec.Command("podman", "rmi", "-f", imageName).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to remove Podman image: %v\n", err)
	}

	if ui.GumAvailable() {
		fmt.Printf("%s  ✓ Image marked for rebuild%s\n", ui.ColorPink, ui.ColorReset)
	} else {
		fmt.Println("  ✓ Image marked for rebuild")
	}
}

// CheckAndMigrate checks if migration is needed and runs it
// This is called early in the application lifecycle
func CheckAndMigrate() error {
	if !NeedsMigration() {
		return nil
	}

	installed := GetInstalledVersion()
	// If no version file, assume upgrade from 0.3.0
	if installed == "" {
		installed = "0.3.0"
	}

	// Show migration notice
	fmt.Println()
	if ui.GumAvailable() {
		ui.GumSuccess(fmt.Sprintf("New version detected: %s → %s", installed, constants.Version))
		fmt.Printf("%sRunning automatic migration...%s\n", ui.ColorCyan, ui.ColorReset)
	} else {
		fmt.Printf("✓ New version detected: %s → %s\n", installed, constants.Version)
		fmt.Println("→ Running automatic migration...")
	}
	fmt.Println()

	if err := RunMigrations(); err != nil {
		return err
	}

	fmt.Println()
	return nil
}

// ForceRefresh manually triggers a refresh of configuration and templates
// This is useful for debugging or when users want to sync with the binary version
func ForceRefresh() error {
	fmt.Println()
	if ui.GumAvailable() {
		ui.GumSuccess("Refreshing configuration and templates from binary")
		fmt.Printf("%sThis will update config, templates, and rebuild the image%s\n", ui.ColorGrey, ui.ColorReset)
	} else {
		fmt.Println("✓ Refreshing configuration and templates from binary")
		fmt.Println("  This will update config, templates, and rebuild the image")
	}
	fmt.Println()

	// 1. Update container templates
	if err := updateContainerTemplates(); err != nil {
		return fmt.Errorf("failed to update container templates: %w", err)
	}

	// 2. Always merge config on force refresh (user explicitly requested)
	if err := mergeConfigFile(); err != nil {
		return fmt.Errorf("failed to merge config file: %w", err)
	}
	if err := saveConfigTemplateHash(); err != nil {
		return fmt.Errorf("failed to save config template hash: %w", err)
	}

	// 3. Mark image for rebuild
	markImageForRebuild()

	// 4. Update installed version
	if err := SetInstalledVersion(constants.Version); err != nil {
		return fmt.Errorf("failed to update version file: %w", err)
	}

	fmt.Println()
	if ui.GumAvailable() {
		ui.GumSuccess("Refresh complete!")
		fmt.Printf("%s  Configuration and templates synced with binary version %s%s\n", ui.ColorGrey, constants.Version, ui.ColorReset)
	} else {
		fmt.Println("✓ Refresh complete!")
		fmt.Printf("  Configuration and templates synced with binary version %s\n", constants.Version)
	}
	fmt.Println()

	return nil
}

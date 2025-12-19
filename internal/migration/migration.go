package migration

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/EstebanForge/construct-cli/internal/config"
	"github.com/EstebanForge/construct-cli/internal/constants"
	"github.com/EstebanForge/construct-cli/internal/templates"
	"github.com/EstebanForge/construct-cli/internal/ui"
	"github.com/pelletier/go-toml/v2"
)

const versionFile = ".version"

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
			fmt.Sscanf(v1Parts[i], "%d", &n1)
		}
		if i < len(v2Parts) {
			fmt.Sscanf(v2Parts[i], "%d", &n2)
		}

		if n1 > n2 {
			return 1
		} else if n1 < n2 {
			return -1
		}
	}
	return 0
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

	// 1. Update container templates (safe to replace)
	if err := updateContainerTemplates(); err != nil {
		return fmt.Errorf("failed to update container templates: %w", err)
	}

	// 2. Merge config.toml (preserve user settings)
	if err := mergeConfigFile(); err != nil {
		return fmt.Errorf("failed to merge config file: %w", err)
	}

	// 3. Mark image for rebuild
	// Delete the image marker so it gets rebuilt on next run
	if err := markImageForRebuild(); err != nil {
		return fmt.Errorf("failed to mark image for rebuild: %w", err)
	}

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
		"Dockerfile":           templates.Dockerfile,
		"docker-compose.yml":   templates.DockerCompose,
		"entrypoint.sh":        templates.Entrypoint,
		"update-all.sh":        templates.UpdateAll,
		"network-filter.sh":    templates.NetworkFilter,
		"clipper":              templates.Clipper,
		"osascript":            templates.Osascript,
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

// mergeConfigFile merges new default config with existing user config
// Preserves all user settings while adding new default fields
func mergeConfigFile() error {
	configPath := filepath.Join(config.GetConfigDir(), "config.toml")

	if ui.GumAvailable() {
		fmt.Printf("%sMerging configuration file...%s\n", ui.ColorCyan, ui.ColorReset)
	} else {
		fmt.Println("→ Merging configuration file...")
	}

	// Read existing user config
	var userConfig map[string]interface{}
	if data, err := os.ReadFile(configPath); err == nil {
		if err := toml.Unmarshal(data, &userConfig); err != nil {
			return fmt.Errorf("failed to parse existing config: %w", err)
		}
	} else {
		userConfig = make(map[string]interface{})
	}

	// Parse default config
	var defaultConfig map[string]interface{}
	if err := toml.Unmarshal([]byte(templates.Config), &defaultConfig); err != nil {
		return fmt.Errorf("failed to parse default config: %w", err)
	}

	// Merge: default config + user overrides
	merged := deepMerge(defaultConfig, userConfig)

	// Write merged config back
	data, err := toml.Marshal(merged)
	if err != nil {
		return fmt.Errorf("failed to marshal merged config: %w", err)
	}

	// Create backup of old config
	backupPath := configPath + ".backup"
	if _, err := os.Stat(configPath); err == nil {
		if oldData, err := os.ReadFile(configPath); err == nil {
			os.WriteFile(backupPath, oldData, 0644)
			if ui.GumAvailable() {
				fmt.Printf("%s  → Backup saved: %s%s\n", ui.ColorGrey, backupPath, ui.ColorReset)
			} else {
				fmt.Printf("  → Backup saved: %s\n", backupPath)
			}
		}
	}

	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write merged config: %w", err)
	}

	if ui.GumAvailable() {
		fmt.Printf("%s  ✓ Configuration merged (user settings preserved)%s\n", ui.ColorPink, ui.ColorReset)
	} else {
		fmt.Println("  ✓ Configuration merged (user settings preserved)")
	}

	return nil
}

// deepMerge recursively merges two maps, preferring values from 'override'
func deepMerge(base, override map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{})

	// Start with base
	for k, v := range base {
		result[k] = v
	}

	// Override with user values
	for k, v := range override {
		if baseVal, exists := result[k]; exists {
			// If both are maps, merge recursively
			if baseMap, ok := baseVal.(map[string]interface{}); ok {
				if overrideMap, ok := v.(map[string]interface{}); ok {
					result[k] = deepMerge(baseMap, overrideMap)
					continue
				}
			}
		}
		// Otherwise, use override value
		result[k] = v
	}

	return result
}

// markImageForRebuild removes the old Docker image to force rebuild on next run
func markImageForRebuild() error {
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
	exec.Command("docker", "rmi", "-f", imageName).Run()

	// Try podman
	exec.Command("podman", "rmi", "-f", imageName).Run()

	if ui.GumAvailable() {
		fmt.Printf("%s  ✓ Image marked for rebuild%s\n", ui.ColorPink, ui.ColorReset)
	} else {
		fmt.Println("  ✓ Image marked for rebuild")
	}

	return nil
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

	// Run the same migration process
	if err := RunMigrations(); err != nil {
		return err
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

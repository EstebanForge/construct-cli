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
const packagesTemplateHashFile = ".packages_template_hash"
const entrypointTemplateHashFile = ".entrypoint_template_hash"

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

	// 1. Version change triggers migration
	if installed != current && compareVersions(current, installed) > 0 {
		return true
	}

	// 2. Template changes trigger migration even if version is same
	if configTemplateChanged() || packagesTemplateChanged() || entrypointTemplateChanged() {
		return true
	}

	return false
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

// getPackagesTemplateHash returns SHA256 hash of the embedded packages template
func getPackagesTemplateHash() string {
	hash := sha256.Sum256([]byte(templates.Packages))
	return hex.EncodeToString(hash[:])
}

// getEntrypointTemplateHash returns SHA256 hash of the embedded entrypoint template
func getEntrypointTemplateHash() string {
	hash := sha256.Sum256([]byte(templates.Entrypoint))
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

// packagesTemplateChanged checks if embedded packages template differs from last applied
func packagesTemplateChanged() bool {
	hashPath := filepath.Join(config.GetConfigDir(), packagesTemplateHashFile)

	storedHash, err := os.ReadFile(hashPath)
	if err != nil {
		// No hash file - either fresh install or upgrade from old version
		// If packages.toml exists, assume template changed (be conservative)
		packagesPath := filepath.Join(config.GetConfigDir(), "packages.toml")
		if _, err := os.Stat(packagesPath); err == nil {
			return true
		}
		return false
	}

	return strings.TrimSpace(string(storedHash)) != getPackagesTemplateHash()
}

// savePackagesTemplateHash stores the current packages template hash
func savePackagesTemplateHash() error {
	hashPath := filepath.Join(config.GetConfigDir(), packagesTemplateHashFile)
	return os.WriteFile(hashPath, []byte(getPackagesTemplateHash()+"\n"), 0644)
}

// entrypointTemplateChanged checks if embedded entrypoint template differs from last applied
func entrypointTemplateChanged() bool {
	hashPath := filepath.Join(config.GetConfigDir(), entrypointTemplateHashFile)

	storedHash, err := os.ReadFile(hashPath)
	if err != nil {
		// No hash file - if entrypoint.sh exists, assume template changed
		containerDir := filepath.Join(config.GetConfigDir(), "container")
		entrypointPath := filepath.Join(containerDir, "entrypoint.sh")
		if _, err := os.Stat(entrypointPath); err == nil {
			return true
		}
		return false
	}

	return strings.TrimSpace(string(storedHash)) != getEntrypointTemplateHash()
}

// saveEntrypointTemplateHash stores the current entrypoint template hash
func saveEntrypointTemplateHash() error {
	hashPath := filepath.Join(config.GetConfigDir(), entrypointTemplateHashFile)
	return os.WriteFile(hashPath, []byte(getEntrypointTemplateHash()+"\n"), 0644)
}

// RunMigrations performs all necessary migrations
func RunMigrations() error {
	installed := GetInstalledVersion()
	// If no version file, assume upgrade from 0.3.0
	if installed == "" {
		installed = "0.3.0"
	}
	current := constants.Version
	entrypointChanged := entrypointTemplateChanged()

	if ui.GumAvailable() {
		ui.GumSuccess(fmt.Sprintf("Upgrading configuration: %s → %s", installed, current))
	} else {
		fmt.Printf("✓ Upgrading configuration: %s → %s\n", installed, current)
	}

	// 1. Update container templates (always - may have bug fixes)
	if err := updateContainerTemplates(); err != nil {
		return fmt.Errorf("failed to update container templates: %w", err)
	}
	if err := saveEntrypointTemplateHash(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to save entrypoint template hash: %v\n", err)
	}
	if entrypointChanged {
		if err := config.SetRebuildRequired("entrypoint template changed"); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Failed to mark rebuild required: %v\n", err)
		}
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

	// 3. Merge packages.toml only if template structure changed
	if packagesTemplateChanged() {
		if err := mergePackagesFile(); err != nil {
			return fmt.Errorf("failed to merge packages file: %w", err)
		}
		if err := savePackagesTemplateHash(); err != nil {
			return fmt.Errorf("failed to save packages template hash: %w", err)
		}
	} else {
		if ui.GumAvailable() {
			fmt.Printf("%s  → Packages structure unchanged, skipping merge%s\n", ui.ColorGrey, ui.ColorReset)
		} else {
			fmt.Println("  → Packages structure unchanged, skipping merge")
		}
	}

	// 4. Regenerate topgrade config (depends on packages.toml)
	regenerateTopgradeConfig()

	// 5. Mark image for rebuild
	markImageForRebuild()

	// 6. Force entrypoint setup to rerun on next container start.
	clearEntrypointHash()
	forceEntrypointRun()

	// 7. Update installed version
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
		"Dockerfile":            templates.Dockerfile,
		"docker-compose.yml":    templates.DockerCompose,
		"entrypoint.sh":         templates.Entrypoint,
		"update-all.sh":         templates.UpdateAll,
		"network-filter.sh":     templates.NetworkFilter,
		"clipper":               templates.Clipper,
		"clipboard-x11-sync.sh": templates.ClipboardX11Sync,
		"osascript":             templates.Osascript,
		"powershell.exe":        templates.PowershellExe,
	}

	// Ensure directory exists
	if err := os.MkdirAll(containerDir, 0755); err != nil {
		return fmt.Errorf("failed to ensure container templates dir: %w", err)
	}

	// Clean up old/unknown files, but handle busy files gracefully
	entries, err := os.ReadDir(containerDir)
	if err != nil {
		return fmt.Errorf("failed to read container templates dir: %w", err)
	}

	for _, entry := range entries {
		filename := entry.Name()
		path := filepath.Join(containerDir, filename)

		// If it's a known template, we'll overwrite it later
		if _, isTemplate := containerFiles[filename]; isTemplate {
			continue
		}

		// Unknown file - try to remove it
		if err := os.Remove(path); err != nil {
			// If file is busy (mounted), warn but ignore
			if strings.Contains(err.Error(), "busy") || strings.Contains(err.Error(), "device or resource busy") {
				fmt.Fprintf(os.Stderr, "Warning: Could not remove %s (resource busy), skipping...\n", filename)
				continue
			}
			// Other errors are fatal
			return fmt.Errorf("failed to remove stale file %s: %w", filename, err)
		}
	}

	// Write new templates
	for filename, content := range containerFiles {
		path := filepath.Join(containerDir, filename)
		perm := os.FileMode(0644)
		if strings.HasSuffix(filename, ".sh") || filename == "clipper" || filename == "osascript" || filename == "powershell.exe" {
			perm = 0755
		}
		if err := os.WriteFile(path, []byte(content), perm); err != nil {
			return fmt.Errorf("failed to write %s: %w", filename, err)
		}
		// Verify write was successful
		if written, err := os.ReadFile(path); err != nil {
			return fmt.Errorf("failed to verify %s: %w", filename, err)
		} else if len(written) != len(content) {
			return fmt.Errorf("write verification failed for %s: expected %d bytes, got %d", filename, len(content), len(written))
		}
		// Always show which files were written
		if ui.GumAvailable() {
			fmt.Printf("%s  ✓ Written %s (%d bytes)%s\n", ui.ColorGrey, filename, len(content), ui.ColorReset)
		} else {
			fmt.Printf("  ✓ Written %s (%d bytes)\n", filename, len(content))
		}
	}

	if ui.GumAvailable() {
		fmt.Printf("%s  ✓ Container templates updated%s\n", ui.ColorPink, ui.ColorReset)
	} else {
		fmt.Println("  ✓ Container templates updated")
	}

	return nil
}

func clearEntrypointHash() {
	hashPath := filepath.Join(config.GetConfigDir(), "home", ".local", ".entrypoint_hash")
	if err := os.Remove(hashPath); err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Warning: Failed to remove entrypoint hash: %v\n", err)
	}
}

func forceEntrypointRun() {
	forcePath := filepath.Join(config.GetConfigDir(), "home", ".local", ".force_entrypoint")
	if err := os.MkdirAll(filepath.Dir(forcePath), 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to ensure entrypoint flag dir: %v\n", err)
		return
	}
	if err := os.WriteFile(forcePath, []byte("1\n"), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to write entrypoint flag: %v\n", err)
	}
}

func regenerateTopgradeConfig() {
	pkgs, err := config.LoadPackages()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to load packages for topgrade config: %v\n", err)
		return
	}

	topgradeDir := filepath.Join(config.GetConfigDir(), "home", ".config")
	if err := os.MkdirAll(topgradeDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to create topgrade config directory: %v\n", err)
		return
	}

	topgradePath := filepath.Join(topgradeDir, "topgrade.toml")
	topgradeConfig := pkgs.GenerateTopgradeConfig()
	if err := os.WriteFile(topgradePath, []byte(topgradeConfig), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to write topgrade config: %v\n", err)
		return
	}

	if ui.GumAvailable() {
		fmt.Printf("%s  ✓ Topgrade configuration regenerated%s\n", ui.ColorPink, ui.ColorReset)
	} else {
		fmt.Println("  ✓ Topgrade configuration regenerated")
	}
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

// mergePackagesFile replaces packages.toml with the template and reapplies user values.
// Preserves template layout/comments while copying supported values.
func mergePackagesFile() error {
	packagesPath := filepath.Join(config.GetConfigDir(), "packages.toml")
	backupPath := packagesPath + ".backup"

	if ui.GumAvailable() {
		fmt.Printf("%sMerging packages file...%s\n", ui.ColorCyan, ui.ColorReset)
	} else {
		fmt.Println("→ Merging packages file...")
	}

	// Check if packages.toml exists
	if _, err := os.Stat(packagesPath); os.IsNotExist(err) {
		// File doesn't exist - just provision from template
		if err := os.WriteFile(packagesPath, []byte(templates.Packages), 0644); err != nil {
			return fmt.Errorf("failed to write packages.toml: %w", err)
		}

		if ui.GumAvailable() {
			fmt.Printf("%s  ✓ packages.toml created from template%s\n", ui.ColorPink, ui.ColorReset)
		} else {
			fmt.Println("  ✓ packages.toml created from template")
		}
		return nil
	}

	// File exists - merge with template
	if _, err := os.Stat(backupPath); err == nil {
		if err := os.Remove(backupPath); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Failed to remove backup: %v\n", err)
		}
	}

	// Backup current packages file
	if err := os.Rename(packagesPath, backupPath); err != nil {
		return fmt.Errorf("failed to backup packages: %w", err)
	}
	if ui.GumAvailable() {
		fmt.Printf("%s  → Backup saved: %s%s\n", ui.ColorGrey, backupPath, ui.ColorReset)
	} else {
		fmt.Printf("  → Backup saved: %s\n", backupPath)
	}

	// Apply template defaults only for missing keys, preserving user values.
	templateData := []byte(templates.Packages)
	backupData, err := os.ReadFile(backupPath)
	if err != nil {
		return fmt.Errorf("failed to read backup packages: %w", err)
	}
	mergedData, err := mergeTemplateWithBackupMissingKeys(templateData, backupData)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to merge user packages values: %v\n", err)
		if err := os.WriteFile(packagesPath, backupData, 0644); err != nil {
			return fmt.Errorf("failed to restore backup packages: %w", err)
		}
	} else {
		if err := os.WriteFile(packagesPath, mergedData, 0644); err != nil {
			return fmt.Errorf("failed to write merged packages: %w", err)
		}
	}

	if ui.GumAvailable() {
		fmt.Printf("%s  ✓ Packages merged (user settings preserved)%s\n", ui.ColorPink, ui.ColorReset)
	} else {
		fmt.Println("  ✓ Packages merged (user settings preserved)")
	}

	return nil
}

// markImageForRebuild stops the container and removes the old image to force rebuild
func markImageForRebuild() {
	containerName := "construct-cli"
	imageName := "construct-box:latest"

	if ui.GumAvailable() {
		fmt.Printf("%sStopping and removing old container...%s\n", ui.ColorCyan, ui.ColorReset)
	} else {
		fmt.Println("→ Stopping and removing old container...")
	}

	// Try docker
	if _, err := exec.LookPath("docker"); err == nil {
		// Stop container (errors are OK - might not be running)
		if err := exec.Command("docker", "stop", containerName).Run(); err != nil {
			ui.LogDebug("Failed to stop container: %v", err)
		}
		// Remove container (errors are OK - might not exist)
		if err := exec.Command("docker", "rm", "-f", containerName).Run(); err != nil {
			ui.LogDebug("Failed to remove container: %v", err)
		}
		// Remove image to force rebuild (errors are OK - might not exist)
		if err := exec.Command("docker", "rmi", "-f", imageName).Run(); err != nil {
			ui.LogDebug("Failed to remove image: %v", err)
		}
	}

	// Try podman
	if _, err := exec.LookPath("podman"); err == nil {
		if err := exec.Command("podman", "stop", containerName).Run(); err != nil {
			ui.LogDebug("Failed to stop container: %v", err)
		}
		if err := exec.Command("podman", "rm", "-f", containerName).Run(); err != nil {
			ui.LogDebug("Failed to remove container: %v", err)
		}
		if err := exec.Command("podman", "rmi", "-f", imageName).Run(); err != nil {
			ui.LogDebug("Failed to remove image: %v", err)
		}
	}

	// Try Apple container (macOS 26+)
	if _, err := exec.LookPath("container"); err == nil {
		// Apple container uses different commands:
		// - container stop (not docker stop)
		// - container rm (not docker rm)
		// - container image rm (not docker rmi)
		if err := exec.Command("container", "stop", containerName).Run(); err != nil {
			ui.LogDebug("Failed to stop container: %v", err)
		}
		if err := exec.Command("container", "rm", containerName).Run(); err != nil {
			ui.LogDebug("Failed to remove container: %v", err)
		}
		if err := exec.Command("container", "image", "rm", imageName).Run(); err != nil {
			ui.LogDebug("Failed to remove image: %v", err)
		}
	}

	if ui.GumAvailable() {
		fmt.Printf("%s  ✓ Container and image removed, forcing rebuild%s\n", ui.ColorPink, ui.ColorReset)
	} else {
		fmt.Println("  ✓ Container and image removed, forcing rebuild")
	}
}

// CheckAndMigrate checks if migration is needed and runs it
// This is called early in the application lifecycle
func CheckAndMigrate() error {
	if !NeedsMigration() {
		return nil
	}

	installed := GetInstalledVersion()
	versionMissing := false
	// If no version file, assume upgrade from 0.3.0 (legacy or missing version file)
	if installed == "" {
		installed = "0.3.0"
		versionMissing = true
	}

	current := constants.Version
	cmp := compareVersions(current, installed)

	// Show migration notice
	fmt.Println()
	if ui.GumAvailable() {
		if versionMissing {
			ui.GumSuccess(fmt.Sprintf("Legacy or missing version detected → %s", current))
		} else if cmp > 0 {
			ui.GumSuccess(fmt.Sprintf("New version detected: %s → %s", installed, current))
		} else if cmp < 0 {
			ui.GumSuccess(fmt.Sprintf("Downgrade detected: %s → %s", installed, current))
		} else {
			ui.GumSuccess(fmt.Sprintf("Template changes detected (%s)", current))
		}
		fmt.Printf("%sRunning automatic migration...%s\n", ui.ColorCyan, ui.ColorReset)
	} else {
		if versionMissing {
			fmt.Printf("✓ Legacy or missing version detected → %s\n", current)
		} else if cmp > 0 {
			fmt.Printf("✓ New version detected: %s → %s\n", installed, current)
		} else if cmp < 0 {
			fmt.Printf("✓ Downgrade detected: %s → %s\n", installed, current)
		} else {
			fmt.Printf("✓ Template changes detected (%s)\n", current)
		}
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
		fmt.Printf("%sThis will update config, templates, and mark The Construct image to be rebuild%s\n", ui.ColorGrey, ui.ColorReset)
	} else {
		fmt.Println("✓ Refreshing configuration and templates from binary")
		fmt.Println("  This will update config, templates, and mark The Construct image to be rebuild")
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

	// 3. Merge packages config
	if err := mergePackagesFile(); err != nil {
		return fmt.Errorf("failed to merge packages file: %w", err)
	}
	if err := savePackagesTemplateHash(); err != nil {
		return fmt.Errorf("failed to save packages template hash: %w", err)
	}

	// 4. Regenerate topgrade config
	regenerateTopgradeConfig()

	// 5. Mark image for rebuild
	markImageForRebuild()

	// 6. Force entrypoint setup to rerun on next container start.
	clearEntrypointHash()
	forceEntrypointRun()

	// 7. Update installed version
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

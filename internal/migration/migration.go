// Package migration handles configuration and template migrations.
package migration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/EstebanForge/construct-cli/internal/config"
	"github.com/EstebanForge/construct-cli/internal/constants"
	"github.com/EstebanForge/construct-cli/internal/templates"
	"github.com/EstebanForge/construct-cli/internal/ui"
	semver "github.com/EstebanForge/construct-cli/internal/version"
)

const versionFile = ".version"
const packagesTemplateHashFile = ".packages_template_hash"
const templateHashesFile = ".template_hashes"

// Template tier classification for rebuild decisions.
//
// Image-tier templates are COPY'd into the Docker image and require a full
// image rebuild (markImageForRebuild) when changed.
//
// Soft-tier templates affect container runtime but are not image-baked;
// changes trigger SetRebuildRequired (deferred rebuild on next run).
var imageTierTemplates = templates.ImageTierTemplates
var softTierTemplates = templates.SoftTierTemplates

// templateDiff describes which template tiers changed.
type templateDiff struct {
	ImageChanged      bool     // Any image-tier template changed
	SoftChanged       bool     // Any soft-tier template changed
	EntrypointChanged bool     // entrypoint.sh specifically changed
	ChangedNames      []string // Human-readable names of changed templates
}

var attemptedOwnershipFix bool
var runOwnershipFixFn = runOwnershipFix
var confirmOwnershipFixFn = ui.GumConfirm
var errOwnershipFixDeclined = errors.New("ownership fix declined")

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
	if packagesTemplateChanged() {
		return true
	}

	// 3. Per-template hash check (image-tier and soft-tier)
	stored := loadTemplateHashes()
	if stored == nil {
		// No hash file - check if this is a pre-hash upgrade
		if isPreHashUpgrade() {
			return true
		}
		// Fresh install - no migration needed
	} else {
		diff := diffTemplates(stored)
		if diff.ImageChanged || diff.SoftChanged {
			return true
		}
	}

	return false
}

// compareVersions compares two semver strings
// Returns: 1 if v1 > v2, -1 if v1 < v2, 0 if equal
func compareVersions(v1, v2 string) int {
	return semver.Compare(v1, v2)
}

// getPackagesTemplateHash returns SHA256 hash of the embedded packages template
func getPackagesTemplateHash() string {
	hash := sha256.Sum256([]byte(templates.Packages))
	return hex.EncodeToString(hash[:])
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

// hashTemplate computes SHA256 hex digest for a template string.
func hashTemplate(content string) string {
	h := sha256.Sum256([]byte(content))
	return hex.EncodeToString(h[:])
}

// computeTemplateHashes builds a sorted map of filename→hash for all tracked templates.
func computeTemplateHashes() map[string]string {
	hashes := make(map[string]string)
	for name, content := range imageTierTemplates {
		hashes[name] = hashTemplate(content)
	}
	for name, content := range softTierTemplates {
		hashes[name] = hashTemplate(content)
	}
	return hashes
}

// loadTemplateHashes reads the stored per-template hashes from disk.
// Returns nil if the file does not exist (first install or pre-hash upgrade).
func loadTemplateHashes() map[string]string {
	path := filepath.Join(config.GetConfigDir(), templateHashesFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var hashes map[string]string
	if err := json.Unmarshal(data, &hashes); err != nil {
		return nil
	}
	return hashes
}

// saveTemplateHashes writes the per-template hashes to disk as JSON.
func saveTemplateHashes(hashes map[string]string) error {
	data, err := json.MarshalIndent(hashes, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal template hashes: %w", err)
	}
	path := filepath.Join(config.GetConfigDir(), templateHashesFile)
	return os.WriteFile(path, append(data, '\n'), 0644)
}

// diffTemplates compares current embedded templates against stored hashes
// and returns which tiers changed along with the specific template names.
func diffTemplates(stored map[string]string) templateDiff {
	current := computeTemplateHashes()
	var diff templateDiff

	for name, currentHash := range current {
		storedHash, exists := stored[name]
		if !exists || storedHash != currentHash {
			diff.ChangedNames = append(diff.ChangedNames, name)
		}
	}

	if len(diff.ChangedNames) == 0 {
		return diff
	}

	sort.Strings(diff.ChangedNames)

	// Classify into tiers
	for _, name := range diff.ChangedNames {
		if _, isImage := imageTierTemplates[name]; isImage {
			diff.ImageChanged = true
			if name == "entrypoint.sh" {
				diff.EntrypointChanged = true
			}
		}
		if _, isSoft := softTierTemplates[name]; isSoft {
			diff.SoftChanged = true
		}
	}

	return diff
}

// isPreHashUpgrade returns true when templates exist on disk but no hash file
// has been written yet (upgrade from a version before per-template hashing).
func isPreHashUpgrade() bool {
	hashPath := filepath.Join(config.GetConfigDir(), templateHashesFile)
	if _, err := os.Stat(hashPath); err == nil {
		return false // Hash file exists
	}
	// Hash file missing - check if templates exist on disk
	containerDir := filepath.Join(config.GetConfigDir(), "container")
	entrypointPath := filepath.Join(containerDir, "entrypoint.sh")
	if _, err := os.Stat(entrypointPath); err == nil {
		return true // Templates exist but no hash file
	}
	return false // Fresh install
}

// RunMigrations performs all necessary migrations
func RunMigrations() error {
	installed := GetInstalledVersion()
	// If no version file, assume upgrade from 0.3.0
	if installed == "" {
		installed = "0.3.0"
	}
	current := constants.Version

	// Determine template changes BEFORE writing files
	stored := loadTemplateHashes()
	preHashUpgrade := stored == nil && isPreHashUpgrade()
	var diff templateDiff
	if stored != nil {
		diff = diffTemplates(stored)
	}
	// Pre-hash upgrade: treat as all tiers changed (conservative)
	if preHashUpgrade {
		diff = templateDiff{ImageChanged: true, SoftChanged: true, EntrypointChanged: true}
		// Add all template names as changed for messaging
		for name := range imageTierTemplates {
			diff.ChangedNames = append(diff.ChangedNames, name)
		}
		for name := range softTierTemplates {
			diff.ChangedNames = append(diff.ChangedNames, name)
		}
		sort.Strings(diff.ChangedNames)
	}

	if ui.GumAvailable() {
		ui.GumSuccess(fmt.Sprintf("Upgrading configuration: %s → %s", installed, current))
	} else {
		fmt.Printf("✓ Upgrading configuration: %s → %s\n", installed, current)
	}

	// 1. Update container templates (always - may have bug fixes)
	if err := updateContainerTemplates(); err != nil {
		return fmt.Errorf("failed to update container templates: %w", err)
	}

	// 2. Tier-based rebuild decisions
	if diff.ImageChanged {
		// Image-tier templates changed: full rebuild required
		if len(diff.ChangedNames) > 0 {
			msg := fmt.Sprintf("%s changed, rebuild required", strings.Join(diff.ChangedNames, ", "))
			if ui.GumAvailable() {
				fmt.Printf("%s  → %s%s\n", ui.ColorYellow, msg, ui.ColorReset)
			} else {
				fmt.Printf("  → %s\n", msg)
			}
		}
		markImageForRebuild()
	} else if diff.SoftChanged {
		// Soft-tier only: deferred rebuild on next run
		var softNames []string
		for _, name := range diff.ChangedNames {
			if _, ok := softTierTemplates[name]; ok {
				softNames = append(softNames, name)
			}
		}
		if len(softNames) > 0 {
			msg := fmt.Sprintf("%s changed, restart on next run", strings.Join(softNames, ", "))
			if ui.GumAvailable() {
				fmt.Printf("%s  → %s%s\n", ui.ColorGrey, msg, ui.ColorReset)
			} else {
				fmt.Printf("  → %s\n", msg)
			}
		}
		if err := config.SetRebuildRequired(strings.Join(softNames, ", ") + " changed"); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Failed to mark rebuild required: %v\n", err)
		}
	} else {
		// No template changes at all
		if ui.GumAvailable() {
			fmt.Printf("%s  → No template changes, skipping rebuild%s\n", ui.ColorGrey, ui.ColorReset)
		} else {
			fmt.Println("  → No template changes, skipping rebuild")
		}
	}

	// 3. Gate entrypoint hash/force on actual entrypoint change
	if diff.EntrypointChanged {
		clearEntrypointHash()
		clearOverrideHash()
		forceEntrypointRun()
	}

	// 4. Merge packages.toml only if template structure changed
	if packagesTemplateChanged() {
		if err := mergePackagesFile(); err != nil {
			return fmt.Errorf("failed to merge packages file: %w", err)
		}
		if err := savePackagesTemplateHash(); err != nil {
			return fmt.Errorf("failed to save packages template hash: %w", err)
		}
		// Packages changed: force entrypoint re-run for package install
		forceEntrypointRun()
	} else {
		if ui.GumAvailable() {
			fmt.Printf("%s  → Packages structure unchanged, skipping merge%s\n", ui.ColorGrey, ui.ColorReset)
		} else {
			fmt.Println("  → Packages structure unchanged, skipping merge")
		}
	}

	// 5. Regenerate topgrade config (depends on packages.toml)
	regenerateTopgradeConfig()

	// 6. Save template hashes LAST (after all operations succeed)
	if err := saveTemplateHashes(computeTemplateHashes()); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to save template hashes: %v\n", err)
	}

	// 7. Clean up legacy per-file hash files
	cleanupLegacyHashFiles()

	// 8. Update installed version
	if err := SetInstalledVersion(current); err != nil {
		return fmt.Errorf("failed to update version file: %w", err)
	}

	if ui.GumAvailable() {
		ui.GumSuccess("Migration complete!")
		if diff.ImageChanged {
			fmt.Printf("%s  Note: Container image will rebuild on next agent run%s\n", ui.ColorGrey, ui.ColorReset)
		} else if diff.SoftChanged {
			fmt.Printf("%s  Note: Container will restart with updated config on next agent run%s\n", ui.ColorGrey, ui.ColorReset)
		}
	} else {
		fmt.Println("✓ Migration complete!")
		if diff.ImageChanged {
			fmt.Println("  Note: Container image will rebuild on next agent run")
		} else if diff.SoftChanged {
			fmt.Println("  Note: Container will restart with updated config on next agent run")
		}
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

	// Container templates (safe to replace - no user modifications expected).
	//
	// Merged from templates.ImageTierTemplates + templates.SoftTierTemplates so
	// embed.go is the single source of truth: adding a new template there flows
	// here automatically, no risk of a stale hand-curated list drifting out of
	// sync with the Dockerfile COPYs.
	containerFiles := make(map[string]string, len(templates.ImageTierTemplates)+len(templates.SoftTierTemplates))
	for k, v := range templates.ImageTierTemplates {
		containerFiles[k] = v
	}
	for k, v := range templates.SoftTierTemplates {
		containerFiles[k] = v
	}

	// Ensure directory exists
	if err := os.MkdirAll(containerDir, 0755); err != nil {
		if recovered, fixErr := attemptMigrationPermissionRecovery(err, config.GetConfigDir()); recovered {
			if retryErr := os.MkdirAll(containerDir, 0755); retryErr != nil {
				return migrationPermissionError("ensure container templates dir", retryErr, config.GetConfigDir(), fixErr)
			}
		} else if fixErr != nil {
			return migrationPermissionError("ensure container templates dir", err, config.GetConfigDir(), fixErr)
		} else {
			return migrationPermissionError("ensure container templates dir", err, config.GetConfigDir(), nil)
		}
	}

	// Validate writability before replacing templates.
	if testErr := verifyWritableDir(containerDir); testErr != nil {
		if recovered, fixErr := attemptMigrationPermissionRecovery(testErr, config.GetConfigDir()); recovered {
			if retryErr := verifyWritableDir(containerDir); retryErr != nil {
				return migrationPermissionError("verify container templates dir writability", retryErr, config.GetConfigDir(), fixErr)
			}
		} else if fixErr != nil {
			return migrationPermissionError("verify container templates dir writability", testErr, config.GetConfigDir(), fixErr)
		} else {
			return migrationPermissionError("verify container templates dir writability", testErr, config.GetConfigDir(), nil)
		}
	}

	// Clean up old/unknown files, but handle busy files gracefully
	entries, err := os.ReadDir(containerDir)
	if err != nil {
		if recovered, fixErr := attemptMigrationPermissionRecovery(err, config.GetConfigDir()); recovered {
			entries, err = os.ReadDir(containerDir)
			if err != nil {
				return migrationPermissionError("read container templates dir", err, config.GetConfigDir(), fixErr)
			}
		} else if fixErr != nil {
			return migrationPermissionError("read container templates dir", err, config.GetConfigDir(), fixErr)
		} else {
			return migrationPermissionError("read container templates dir", err, config.GetConfigDir(), nil)
		}
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
		if strings.HasSuffix(filename, ".sh") || filename == "clipper" || filename == "osascript" || filename == "construct-host-exec" {
			perm = 0755
		}
		if err := writeTemplateFile(path, []byte(content), perm); err != nil {
			if recovered, fixErr := attemptMigrationPermissionRecovery(err, config.GetConfigDir()); recovered {
				if retryErr := writeTemplateFile(path, []byte(content), perm); retryErr != nil {
					return migrationPermissionError(fmt.Sprintf("write %s", filename), retryErr, config.GetConfigDir(), fixErr)
				}
			} else if fixErr != nil {
				return migrationPermissionError(fmt.Sprintf("write %s", filename), err, config.GetConfigDir(), fixErr)
			} else {
				return migrationPermissionError(fmt.Sprintf("write %s", filename), err, config.GetConfigDir(), nil)
			}
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

func writeTemplateFile(path string, content []byte, perm os.FileMode) error {
	info, err := os.Stat(path)
	if err == nil && info.IsDir() {
		if removeErr := os.RemoveAll(path); removeErr != nil {
			return fmt.Errorf("remove directory blocking template write (%s): %w", path, removeErr)
		}
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}

	return os.WriteFile(path, content, perm)
}

func verifyWritableDir(dir string) error {
	testFile := filepath.Join(dir, ".construct-write-test")
	if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
		return err
	}
	if err := os.Remove(testFile); err != nil {
		return err
	}
	return nil
}

func attemptMigrationPermissionRecovery(cause error, configPath string) (bool, error) {
	return attemptMigrationPermissionRecoveryForOS(runtime.GOOS, cause, configPath)
}

func attemptMigrationPermissionRecoveryForOS(osName string, cause error, configPath string) (bool, error) {
	if osName != "linux" || attemptedOwnershipFix || !isPermissionWriteError(cause) {
		return false, nil
	}

	attemptedOwnershipFix = true
	if ui.GumAvailable() {
		fmt.Printf("%sDetected config ownership issue.%s\n", ui.ColorYellow, ui.ColorReset)
	} else {
		fmt.Println("Detected config ownership issue.")
	}

	commands := ownershipFixCommands(configPath)
	fmt.Fprintf(os.Stderr, "Run one of these commands manually to fix it:\n")
	for _, cmd := range commands {
		fmt.Fprintf(os.Stderr, "  %s%s%s\n", ui.ColorCyan, cmd, ui.ColorReset)
	}
	fmt.Fprintln(os.Stderr)

	if !confirmOwnershipFixFn(fmt.Sprintf("Attempt to fix ownership now? (%s)", configPath)) {
		return false, ownershipFixDeclinedError(commands)
	}

	if err := runOwnershipFixFn(configPath); err != nil {
		return false, err
	}
	return true, nil
}

func isPermissionWriteError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, os.ErrPermission) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "permission denied")
}

func migrationPermissionError(operation string, err error, configPath string, fixErr error) error {
	base := fmt.Sprintf("failed to %s: %v", operation, err)
	if !isPermissionWriteError(err) {
		return errors.New(base)
	}

	fixHint := formatOwnershipFixCommands(ownershipFixCommands(configPath))
	if fixErr != nil {
		if errors.Is(fixErr, errOwnershipFixDeclined) {
			return fmt.Errorf("%s. %v", base, fixErr)
		}
		return fmt.Errorf("%s (automatic ownership fix failed: %v). Fix manually with one of:\n%s", base, fixErr, fixHint)
	}
	return fmt.Errorf("%s. Fix ownership and retry with one of:\n%s", base, fixHint)
}

func clearEntrypointHash() {
	hashPath := filepath.Join(config.GetConfigDir(), "home", ".local", ".entrypoint_hash")
	if err := os.Remove(hashPath); err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Warning: Failed to remove entrypoint hash: %v\n", err)
	}
}

func clearOverrideHash() {
	hashPath := filepath.Join(config.GetConfigDir(), "container", ".override_hash")
	if err := os.Remove(hashPath); err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Warning: Failed to remove override hash: %v\n", err)
	}
}

func forceEntrypointRun() {
	forcePath := filepath.Join(config.GetConfigDir(), "home", ".local", ".force_entrypoint")
	if err := os.MkdirAll(filepath.Dir(forcePath), 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to ensure entrypoint flag dir: %v\n", err)
		warnConfigPermission(err)
		return
	}
	if err := os.WriteFile(forcePath, []byte("1\n"), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to write entrypoint flag: %v\n", err)
		warnConfigPermission(err)
	}
}

// cleanupLegacyHashFiles removes old per-file hash files that have been
// replaced by the unified .template_hashes JSON file.
func cleanupLegacyHashFiles() {
	legacyFiles := []string{".entrypoint_template_hash"}
	for _, name := range legacyFiles {
		path := filepath.Join(config.GetConfigDir(), name)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "Warning: Failed to remove legacy %s: %v\n", name, err)
		}
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
		warnConfigPermission(err)
		return
	}

	topgradePath := filepath.Join(topgradeDir, "topgrade.toml")
	topgradeConfig := pkgs.GenerateTopgradeConfig()
	if err := os.WriteFile(topgradePath, []byte(topgradeConfig), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to write topgrade config: %v\n", err)
		warnConfigPermission(err)
		return
	}

	if ui.GumAvailable() {
		fmt.Printf("%s  ✓ Topgrade configuration regenerated%s\n", ui.ColorPink, ui.ColorReset)
	} else {
		fmt.Println("  ✓ Topgrade configuration regenerated")
	}
}

func warnConfigPermission(err error) {
	if !errors.Is(err, os.ErrPermission) {
		return
	}
	configPath := config.GetConfigDir()
	fmt.Fprintf(os.Stderr, "Warning: Config directory is not writable: %s\n", configPath)
	fmt.Fprintf(os.Stderr, "%sWarning: Fix ownership with one of:%s\n", ui.ColorYellow, ui.ColorReset)
	for _, cmd := range ownershipFixCommands(configPath) {
		fmt.Fprintf(os.Stderr, "  %s%s%s\n", ui.ColorCyan, cmd, ui.ColorReset)
	}
}

func runOwnershipFix(configPath string) error {
	uid := os.Getuid()
	gid := os.Getgid()
	cmd := exec.Command("sudo", "chown", "-R", fmt.Sprintf("%d:%d", uid, gid), configPath)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func ownershipFixCommands(configPath string) []string {
	quotedPath := shellQuote(configPath)
	commands := []string{}
	if _, err := exec.LookPath("podman"); err == nil {
		commands = append(commands, fmt.Sprintf("podman unshare chown -R 0:0 %s", quotedPath))
	}
	commands = append(commands, fmt.Sprintf("sudo chown -R %d:%d %s", os.Getuid(), os.Getgid(), quotedPath))
	return commands
}

func formatOwnershipFixCommands(commands []string) string {
	lines := make([]string, 0, len(commands))
	for _, cmd := range commands {
		lines = append(lines, "  "+cmd)
	}
	return strings.Join(lines, "\n")
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func ownershipFixDeclinedError(commands []string) error {
	return fmt.Errorf("%w. Run one of:\n%s", errOwnershipFixDeclined, formatOwnershipFixCommands(commands))
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

// collectSessionContainers discovers daemon + all CWD-derived session containers
// for the given runtime. CWD-derived containers use the naming pattern
// "construct-cli-<hash>" so we discover them via prefix filter.
func collectSessionContainers(containerRuntime string) []string {
	names := []string{"construct-cli-daemon"}
	// The Apple container runtime ("container") uses docker-compatible CLI commands
	runtimeBin := "docker"
	if containerRuntime == "podman" {
		runtimeBin = "podman"
	}
	cmd := exec.Command(runtimeBin, "ps", "-aq", "--filter", "name=construct-cli-", "--format", "{{.Names}}")
	output, err := cmd.Output()
	if err == nil {
		for _, name := range strings.Split(strings.TrimSpace(string(output)), "\n") {
			if name != "" && name != "construct-cli-daemon" {
				names = append(names, name)
			}
		}
	}
	// Legacy singleton container (pre-CWD-naming)
	checkState := exec.Command(runtimeBin, "ps", "-aq", "--filter", "name=^construct-cli$", "--format", "{{.Names}}")
	legacyOut, legacyErr := checkState.Output()
	if legacyErr == nil {
		legacyName := strings.TrimSpace(string(legacyOut))
		if legacyName == "construct-cli" {
			names = append(names, "construct-cli")
		}
	}
	return names
}

// markImageForRebuild stops containers and removes the old image to force rebuild
func markImageForRebuild() {
	imageName := "construct-box:latest"

	if ui.GumAvailable() {
		fmt.Printf("%sStopping and removing old containers...%s\n", ui.ColorCyan, ui.ColorReset)
	} else {
		fmt.Println("→ Stopping and removing old containers...")
	}

	// Try docker
	if _, err := exec.LookPath("docker"); err == nil {
		for _, containerName := range collectSessionContainers("docker") {
			// Stop container (errors are OK - might not be running)
			if err := exec.Command("docker", "stop", containerName).Run(); err != nil {
				ui.LogDebug("Failed to stop container %s: %v", containerName, err)
			}
			// Remove container (errors are OK - might not exist)
			if err := exec.Command("docker", "rm", "-f", containerName).Run(); err != nil {
				ui.LogDebug("Failed to remove container %s: %v", containerName, err)
			}
		}
		// Remove image to force rebuild (errors are OK - might not exist)
		if err := exec.Command("docker", "rmi", "-f", imageName).Run(); err != nil {
			ui.LogDebug("Failed to remove image: %v", err)
		}
	}

	// Try podman
	if _, err := exec.LookPath("podman"); err == nil {
		for _, containerName := range collectSessionContainers("podman") {
			if err := exec.Command("podman", "stop", containerName).Run(); err != nil {
				ui.LogDebug("Failed to stop container %s: %v", containerName, err)
			}
			if err := exec.Command("podman", "rm", "-f", containerName).Run(); err != nil {
				ui.LogDebug("Failed to remove container %s: %v", containerName, err)
			}
		}
		if err := exec.Command("podman", "rmi", "-f", imageName).Run(); err != nil {
			ui.LogDebug("Failed to remove image: %v", err)
		}
	}

	// Try Apple container (macOS 26+)
	if _, err := exec.LookPath("container"); err == nil {
		for _, containerName := range collectSessionContainers("container") {
			// Apple container uses different commands:
			// - container stop (not docker stop)
			// - container rm (not docker rm)
			// - container image rm (not docker rmi)
			if err := exec.Command("container", "stop", containerName).Run(); err != nil {
				ui.LogDebug("Failed to stop container %s: %v", containerName, err)
			}
			if err := exec.Command("container", "rm", containerName).Run(); err != nil {
				ui.LogDebug("Failed to remove container %s: %v", containerName, err)
			}
		}
		if err := exec.Command("container", "image", "rm", imageName).Run(); err != nil {
			ui.LogDebug("Failed to remove image: %v", err)
		}
	}

	if ui.GumAvailable() {
		fmt.Printf("%s  ✓ Containers and image removed, forcing rebuild%s\n", ui.ColorPink, ui.ColorReset)
	} else {
		fmt.Println("  ✓ Containers and image removed, forcing rebuild")
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
		fmt.Printf("%sThis will update config, templates, and rebuild the container image%s\n", ui.ColorGrey, ui.ColorReset)
	} else {
		fmt.Println("✓ Refreshing configuration and templates from binary")
		fmt.Println("  This will update config, templates, and rebuild the container image")
	}
	fmt.Println()

	// 1. Update container templates
	if err := updateContainerTemplates(); err != nil {
		return fmt.Errorf("failed to update container templates: %w", err)
	}

	// 2. Merge packages config
	if err := mergePackagesFile(); err != nil {
		return fmt.Errorf("failed to merge packages file: %w", err)
	}
	if err := savePackagesTemplateHash(); err != nil {
		return fmt.Errorf("failed to save packages template hash: %w", err)
	}

	// 3. Regenerate topgrade config
	regenerateTopgradeConfig()

	// 4. Force full rebuild (user explicitly requested refresh)
	markImageForRebuild()

	// 5. Force entrypoint setup to rerun on next container start.
	clearEntrypointHash()
	clearOverrideHash()
	forceEntrypointRun()

	// 6. Save template hashes (after all operations succeed)
	if err := saveTemplateHashes(computeTemplateHashes()); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to save template hashes: %v\n", err)
	}

	// 7. Clean up legacy hash files
	cleanupLegacyHashFiles()

	// 8. Update installed version
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

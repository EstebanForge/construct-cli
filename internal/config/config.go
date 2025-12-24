// Package config manages configuration loading and persistence.
package config

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/EstebanForge/construct-cli/internal/constants"
	"github.com/EstebanForge/construct-cli/internal/templates"
	"github.com/EstebanForge/construct-cli/internal/ui"
	"github.com/pelletier/go-toml/v2"
)

// Config represents the root configuration.
type Config struct {
	Runtime     RuntimeConfig     `toml:"runtime"`
	Sandbox     SandboxConfig     `toml:"sandbox"`
	Network     NetworkConfig     `toml:"network"`
	Maintenance MaintenanceConfig `toml:"maintenance"`
	Claude      ClaudeConfig      `toml:"claude"`
}

// RuntimeConfig holds container runtime settings.
type RuntimeConfig struct {
	Engine              string `toml:"engine"`
	AutoUpdateCheck     bool   `toml:"auto_update_check"`
	UpdateCheckInterval int    `toml:"update_check_interval"` // seconds
}

// SandboxConfig holds sandbox options.
type SandboxConfig struct {
	MountHome       bool   `toml:"mount_home"`
	ForwardSSHAgent bool   `toml:"forward_ssh_agent"`
	Shell           string `toml:"shell"`
}

// NetworkConfig holds network allow/block settings.
type NetworkConfig struct {
	Mode           string   `toml:"mode"`
	AllowedDomains []string `toml:"allowed_domains"`
	AllowedIPs     []string `toml:"allowed_ips"`
	BlockedDomains []string `toml:"blocked_domains"`
	BlockedIPs     []string `toml:"blocked_ips"`
}

// MaintenanceConfig holds log cleanup settings.
type MaintenanceConfig struct {
	CleanupEnabled         bool `toml:"cleanup_enabled"`
	CleanupIntervalSeconds int  `toml:"cleanup_interval_seconds"`
	LogRetentionDays       int  `toml:"log_retention_days"`
}

// ClaudeConfig stores Claude provider configuration.
type ClaudeConfig struct {
	Providers map[string]map[string]string `toml:"cc"`
}

// GetConfigDir returns the user config directory path.
func GetConfigDir() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: Unable to determine home directory: %v\n", err)
		os.Exit(1)
	}
	return filepath.Join(homeDir, constants.ConfigDir)
}

// GetContainerDir returns the container template directory path.
func GetContainerDir() string {
	return filepath.Join(GetConfigDir(), "container")
}

// GetLogsDir returns the logs directory path.
func GetLogsDir() string {
	return filepath.Join(GetConfigDir(), "logs")
}

// CreateLogFile creates a log file for build/update operations
func CreateLogFile(operation string) (*os.File, error) {
	logsDir := GetLogsDir()

	// Create logs directory if it doesn't exist
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create logs directory: %w", err)
	}

	// Create log file with timestamp
	timestamp := time.Now().Format("2006-01-02_15-04-05")
	logFileName := fmt.Sprintf("%s_%s.log", operation, timestamp)
	logPath := filepath.Join(logsDir, logFileName)

	logFile, err := os.Create(logPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create log file: %w", err)
	}

	return logFile, nil
}

// TeeWriter writes to both stdout and a log file
type TeeWriter struct {
	writers []io.Writer
}

func (t *TeeWriter) Write(p []byte) (n int, err error) {
	for _, w := range t.writers {
		n, err = w.Write(p)
		if err != nil {
			return
		}
	}
	return len(p), nil
}

// Load reads the config file, creating it if necessary
// Returns (config, createdNew, error)
func Load() (*Config, bool, error) {
	configPath := filepath.Join(GetConfigDir(), "config.toml")
	containerDir := GetContainerDir()

	dockerfilePath := filepath.Join(containerDir, "Dockerfile")
	composePath := filepath.Join(containerDir, "docker-compose.yml")
	entrypointPath := filepath.Join(containerDir, "entrypoint.sh")
	updateAllPath := filepath.Join(containerDir, "update-all.sh")
	networkFilterPath := filepath.Join(containerDir, "network-filter.sh")
	clipperPath := filepath.Join(containerDir, "clipper")
	osascriptPath := filepath.Join(containerDir, "osascript")

	// Check if any required file is missing
	configMissing := false
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		configMissing = true
	}
	if _, err := os.Stat(dockerfilePath); os.IsNotExist(err) {
		configMissing = true
	}
	if _, err := os.Stat(composePath); os.IsNotExist(err) {
		configMissing = true
	}
	if _, err := os.Stat(entrypointPath); os.IsNotExist(err) {
		configMissing = true
	}
	if _, err := os.Stat(updateAllPath); os.IsNotExist(err) {
		configMissing = true
	}
	if _, err := os.Stat(networkFilterPath); os.IsNotExist(err) {
		configMissing = true
	}
	if _, err := os.Stat(clipperPath); os.IsNotExist(err) {
		configMissing = true
	}
	if _, err := os.Stat(osascriptPath); os.IsNotExist(err) {
		configMissing = true
	}

	createdNew := false
	// Run init if any file is missing
	if configMissing {
		fmt.Println("Required files missing. Running initialization...")
		Init()
		createdNew = true
		fmt.Println()
	}

	// Note: Migration check is handled separately in main.go before config.Load()
	// to ensure it runs early in the application lifecycle

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, createdNew, fmt.Errorf("failed to read config file: %w", err)
	}

	var config Config
	if err := toml.Unmarshal(data, &config); err != nil {
		return nil, createdNew, fmt.Errorf("failed to parse config.toml: %w", err)
	}

	return &config, createdNew, nil
}

// Save writes the config back to config.toml
func (c *Config) Save() error {
	configPath := filepath.Join(GetConfigDir(), "config.toml")

	data, err := toml.Marshal(c)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}

// Init creates the config directory and template files.
func Init() {
	configPath := GetConfigDir()
	containerDir := GetContainerDir()

	// Helper to create file with gum spinner
	createFile := func(path string, content []byte, perm os.FileMode) {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			if ui.GumAvailable() {
				// Use gum spinner for creating file
				ui.GumSpinner(fmt.Sprintf("Creating %s...", filepath.Base(path)), func() []string {
					if err := os.WriteFile(path, content, perm); err != nil {
						return []string{fmt.Sprintf("Error creating %s: %v", filepath.Base(path), err)}
					}
					return []string{fmt.Sprintf("Created: %s", path)}
				})
			} else {
				if err := os.WriteFile(path, content, perm); err != nil {
					fmt.Fprintf(os.Stderr, "Error: Failed to write %s: %v\n", filepath.Base(path), err)
					os.Exit(1)
				}
			}
		} else {
			if !ui.GumAvailable() {
				fmt.Printf("⊗ Exists:  %s\n", path)
			}
		}
	}

	// Create directories
	dirs := []string{
		configPath,
		containerDir,
		filepath.Join(configPath, "home"),
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			ui.GumError(fmt.Sprintf("Failed to create directory %s: %v", dir, err))
			os.Exit(1)
		}
	}

	// Create files using helper (in container directory)
	createFile(filepath.Join(containerDir, "Dockerfile"), []byte(templates.Dockerfile), 0644)
	createFile(filepath.Join(containerDir, "docker-compose.yml"), []byte(templates.DockerCompose), 0644)
	createFile(filepath.Join(containerDir, "entrypoint.sh"), []byte(templates.Entrypoint), 0755)
	createFile(filepath.Join(containerDir, "update-all.sh"), []byte(templates.UpdateAll), 0755)
	createFile(filepath.Join(containerDir, "network-filter.sh"), []byte(templates.NetworkFilter), 0755)
	createFile(filepath.Join(containerDir, "clipper"), []byte(templates.Clipper), 0755)
	createFile(filepath.Join(containerDir, "osascript"), []byte(templates.Osascript), 0755)

	// Create config.toml in root config dir
	createFile(filepath.Join(configPath, "config.toml"), []byte(templates.Config), 0644)

	if ui.GumAvailable() {
		ui.GumSuccess("The Construct initialized successfully!")
		cmd := ui.GetGumCommand("style", "--foreground", "242", fmt.Sprintf("Config directory: %s", configPath))
		cmd.Stdout = os.Stdout
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to render config path: %v\n", err)
		}
	} else {
		fmt.Println("\nThe Construct initialized successfully!")
		fmt.Printf("Config directory: %s\n", configPath)
	}

	// Set initial version for new installations
	// This allows future migrations to detect version changes
	SetInitialVersion()

	// Set initial config template hash
	hash := sha256.Sum256([]byte(templates.Config))
	SetConfigTemplateHash(hex.EncodeToString(hash[:]))
}

// SetInitialVersion writes the current version to .version file
// This is called during initial setup to track the installed version
func SetInitialVersion() {
	versionPath := filepath.Join(GetConfigDir(), ".version")
	if err := os.WriteFile(versionPath, []byte(constants.Version+"\n"), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to write version file: %v\n", err)
	}
}

// SetConfigTemplateHash writes the SHA256 hash of the config template
// This is called during initial setup and migration to track template changes
func SetConfigTemplateHash(hash string) {
	hashPath := filepath.Join(GetConfigDir(), ".config_template_hash")
	if err := os.WriteFile(hashPath, []byte(hash+"\n"), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to write config template hash: %v\n", err)
	}
}

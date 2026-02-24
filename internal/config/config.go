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
	Agents      AgentsConfig      `toml:"agents"`
	Daemon      DaemonConfig      `toml:"daemon"`
	Claude      ClaudeConfig      `toml:"claude"`
}

// RuntimeConfig holds container runtime settings.
type RuntimeConfig struct {
	Engine              string `toml:"engine"`
	AutoUpdateCheck     bool   `toml:"auto_update_check"`
	UpdateCheckInterval int    `toml:"update_check_interval"` // seconds
	UpdateChannel       string `toml:"update_channel"`        // stable|beta
}

// SandboxConfig holds sandbox options.
type SandboxConfig struct {
	MountHome            bool   `toml:"mount_home"`
	ForwardSSHAgent      bool   `toml:"forward_ssh_agent"`
	PropagateGitIdentity bool   `toml:"propagate_git_identity"`
	NonRootStrict        bool   `toml:"non_root_strict"`
	AllowCustomOverride  bool   `toml:"allow_custom_compose_override"`
	ExecAsHostUser       bool   `toml:"exec_as_host_user"`
	Shell                string `toml:"shell"`
	ClipboardHost        string `toml:"clipboard_host"`
	SelinuxLabels        string `toml:"selinux_labels"`
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

// AgentsConfig holds per-agent behavior flags.
type AgentsConfig struct {
	YoloAll             bool     `toml:"yolo_all"`
	YoloAgents          []string `toml:"yolo_agents"`
	ClipboardImagePatch bool     `toml:"clipboard_image_patch"`
}

// DaemonConfig holds daemon behavior settings.
type DaemonConfig struct {
	AutoStart         bool     `toml:"auto_start"`          // Auto-start daemon on first agent run (default: true)
	MultiPathsEnabled bool     `toml:"multi_paths_enabled"` // Enable multi-path daemon mounts (default: false)
	MountPaths        []string `toml:"mount_paths"`         // Multi-path daemon mount roots (opt-in)
}

// ClaudeConfig stores Claude provider configuration.
type ClaudeConfig struct {
	Providers map[string]map[string]string `toml:"cc"`
}

// DefaultConfig returns the default configuration values.
func DefaultConfig() Config {
	return Config{
		Runtime: RuntimeConfig{
			Engine:              "auto",
			AutoUpdateCheck:     true,
			UpdateCheckInterval: 86400,
			UpdateChannel:       "stable",
		},
		Sandbox: SandboxConfig{
			MountHome:            false,
			ForwardSSHAgent:      true,
			PropagateGitIdentity: true,
			NonRootStrict:        false,
			AllowCustomOverride:  false,
			ExecAsHostUser:       true,
			Shell:                "/bin/bash",
			ClipboardHost:        "host.docker.internal",
			SelinuxLabels:        "auto",
		},
		Network: NetworkConfig{
			Mode: "permissive",
			AllowedDomains: []string{
				"*.anthropic.com",
				"*.openai.com",
				"*.googleapis.com",
				"api.z.ai",
			},
			AllowedIPs: []string{
				"1.1.1.1/32",
				"8.8.8.8/32",
			},
			BlockedDomains: []string{
				"*.malicious-site.example",
				"*.phishing.attempt.com",
			},
			BlockedIPs: []string{
				"192.168.100.100/32",
				"203.0.113.0/24",
			},
		},
		Maintenance: MaintenanceConfig{
			CleanupEnabled:         true,
			CleanupIntervalSeconds: 86400,
			LogRetentionDays:       15,
		},
		Agents: AgentsConfig{
			YoloAll:             false,
			YoloAgents:          []string{},
			ClipboardImagePatch: true,
		},
		Daemon: DaemonConfig{
			AutoStart:         true,
			MultiPathsEnabled: false,
			MountPaths:        []string{},
		},
	}
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
	packagesPath := filepath.Join(GetConfigDir(), "packages.toml")

	// Check if any required file is missing
	configMissing := false
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		configMissing = true
	}
	if _, err := os.Stat(filepath.Join(containerDir, "Dockerfile")); os.IsNotExist(err) {
		configMissing = true
	}
	if _, err := os.Stat(filepath.Join(containerDir, "powershell.exe")); os.IsNotExist(err) {
		configMissing = true
	}
	if _, err := os.Stat(packagesPath); os.IsNotExist(err) {
		configMissing = true
	}

	createdNew := false
	// Run init if any file is missing
	if configMissing {
		fmt.Println("Required files missing. Running initialization...")
		if err := Init(); err != nil {
			return nil, false, fmt.Errorf("initialization failed: %w", err)
		}
		createdNew = true
		fmt.Println()
	}

	// Note: Migration check is handled separately in main.go before config.Load()
	// to ensure it runs early in the application lifecycle

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, createdNew, fmt.Errorf("failed to read config file: %w", err)
	}

	config := DefaultConfig()
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
func Init() error {
	configPath := GetConfigDir()
	containerDir := GetContainerDir()

	// Helper to create file with gum spinner
	createFile := func(path string, content []byte, perm os.FileMode) error {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			if ui.GumAvailable() {
				// Use gum spinner for creating file
				var createErr error
				ui.GumSpinner(fmt.Sprintf("Creating %s...", filepath.Base(path)), func() []string {
					if err := os.WriteFile(path, content, perm); err != nil {
						createErr = err
						return []string{fmt.Sprintf("Error creating %s: %v", filepath.Base(path), err)}
					}
					return []string{fmt.Sprintf("Created: %s", path)}
				})
				return createErr
			}
			if err := os.WriteFile(path, content, perm); err != nil {
				return fmt.Errorf("failed to write %s: %w", filepath.Base(path), err)
			}
		} else {
			if !ui.GumAvailable() {
				fmt.Printf("⊗ Exists:  %s\n", path)
			}
		}
		return nil
	}

	// Create directories
	dirs := []string{
		configPath,
		containerDir,
		filepath.Join(configPath, "home"),
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			if ui.GumAvailable() {
				ui.GumError(fmt.Sprintf("Failed to create directory %s: %v", dir, err))
			} else {
				fmt.Fprintf(os.Stderr, "Error: Failed to create directory %s: %v\n", dir, err)
			}
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	// Create files using helper
	files := []struct {
		path    string
		content []byte
		perm    os.FileMode
	}{
		{filepath.Join(containerDir, "Dockerfile"), []byte(templates.Dockerfile), 0644},
		{filepath.Join(containerDir, "docker-compose.yml"), []byte(templates.DockerCompose), 0644},
		{filepath.Join(containerDir, "entrypoint.sh"), []byte(templates.Entrypoint), 0755},
		{filepath.Join(containerDir, "update-all.sh"), []byte(templates.UpdateAll), 0755},
		{filepath.Join(containerDir, "network-filter.sh"), []byte(templates.NetworkFilter), 0755},
		{filepath.Join(containerDir, "clipper"), []byte(templates.Clipper), 0755},
		{filepath.Join(containerDir, "clipboard-x11-sync.sh"), []byte(templates.ClipboardX11Sync), 0755},
		{filepath.Join(containerDir, "osascript"), []byte(templates.Osascript), 0755},
		{filepath.Join(containerDir, "powershell.exe"), []byte(templates.PowershellExe), 0755},
		{filepath.Join(configPath, "config.toml"), []byte(templates.Config), 0644},
		{filepath.Join(configPath, "packages.toml"), []byte(templates.Packages), 0644},
	}

	for _, f := range files {
		if err := createFile(f.path, f.content, f.perm); err != nil {
			return err
		}
	}

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

	// Set initial packages template hash
	pkgHash := sha256.Sum256([]byte(templates.Packages))
	SetPackagesTemplateHash(hex.EncodeToString(pkgHash[:]))

	// Set initial entrypoint template hash
	epHash := sha256.Sum256([]byte(templates.Entrypoint))
	SetEntrypointTemplateHash(hex.EncodeToString(epHash[:]))

	return nil
}

// SetInitialVersion writes the current version to .version file
// This is called during initial setup to track the installed version
func SetInitialVersion() {
	versionPath := filepath.Join(GetConfigDir(), ".version")
	if err := os.WriteFile(versionPath, []byte(constants.Version+"\n"), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to write version file: %v\n", err)
	}
}

// SetPackagesTemplateHash writes the SHA256 hash of the packages template
func SetPackagesTemplateHash(hash string) {
	hashPath := filepath.Join(GetConfigDir(), ".packages_template_hash")
	if err := os.WriteFile(hashPath, []byte(hash+"\n"), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to write packages template hash: %v\n", err)
	}
}

// SetEntrypointTemplateHash writes the SHA256 hash of the entrypoint template
func SetEntrypointTemplateHash(hash string) {
	hashPath := filepath.Join(GetConfigDir(), ".entrypoint_template_hash")
	if err := os.WriteFile(hashPath, []byte(hash+"\n"), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to write entrypoint template hash: %v\n", err)
	}
}

// GetDefaultPackages returns the default packages.toml template content
func GetDefaultPackages() string {
	return templates.Packages
}

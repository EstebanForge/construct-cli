// Package config manages configuration loading and persistence.
package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
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
	Security    SecurityConfig    `toml:"security"`
}

// RuntimeConfig holds container runtime settings.
type RuntimeConfig struct {
	// Backend is the single isolation knob: "auto" (default; detects
	// container > podman > docker), a pinned OCI binary ("container",
	// "podman", "docker"), or "microvm" (microsandbox; fails closed if not
	// installed).
	Backend string `toml:"backend"`
	// Engine is the deprecated pre-merge key (auto|container|podman|docker).
	// Kept for the in-file migration and the in-memory fallback in Load();
	// the file rewrite removes it, so it never round-trips via Save().
	Engine              string `toml:"engine,omitempty"`
	AutoUpdateCheck     bool   `toml:"auto_update_check"`
	UpdateCheckInterval int    `toml:"update_check_interval"` // seconds
	UpdateChannel       string `toml:"update_channel"`        // stable|beta
}

// SandboxConfig holds sandbox options.
type SandboxConfig struct {
	MountHome              bool     `toml:"mount_home"`
	AllowHomeWorkspace     bool     `toml:"allow_home_workspace"`  // Allow mounting host $HOME into /workspace under microvm (dangerous; default false)
	WorkspaceMaxEntries    int      `toml:"workspace_max_entries"` // Max file entries in workspace before confirmation prompt (default: 60000)
	ForwardSSHAgent        bool     `toml:"forward_ssh_agent"`
	PropagateGitIdentity   bool     `toml:"propagate_git_identity"`
	NonRootStrict          bool     `toml:"non_root_strict"`
	AllowCustomOverride    bool     `toml:"allow_custom_compose_override"`
	DisableSeccomp         bool     `toml:"disable_seccomp"`
	ExecAsHostUser         bool     `toml:"exec_as_host_user"`
	EnvPassthrough         []string `toml:"env_passthrough"`
	EnvPassthroughPrefixes []string `toml:"env_passthrough_prefixes"`
	Shell                  string   `toml:"shell"`
	ClipboardHost          string   `toml:"clipboard_host"`
	SelinuxLabels          string   `toml:"selinux_labels"`
	HostServiceEnv         []string `toml:"host_service_env"`    // ENV=http://localhost:port, rewritten to host.docker.internal
	HostLoopbackPorts      []int    `toml:"host_loopback_ports"` // container 127.0.0.1 ports relayed to host.docker.internal (Chromium-hardcoded *.localhost/localhost traffic)
	SSHPinIdentities       []string `toml:"ssh_pin_identities"`  // host=keyname entries; pin one SSH identity per host to avoid sshd MaxAuthTries when the agent holds many keys
	HostBinaries           []string `toml:"host_binaries"`       // binaries proxied to the host instead of run in-container; see docs/HOST-EXEC.md
	MountSkills            bool     `toml:"mount_skills"`        // Bind-mount host skills source into each supported agent's skills dir (default: true; auto-detects ~/Dev/EstebanForge/AGENTS/skills, ~/AGENTS/skills, ~/.config/construct-cli/skills, $XDG_DATA_HOME/construct/skills). See docs/VMsv2.md phase 7.
	SkillsReadOnly         bool     `toml:"skills_read_only"`    // Mount skills source read-only (default: true). Set false to allow agents inside the sandbox to author or edit skills (writes flow back to host). Opt-in RW; default is the safe choice.
	SkillsSource           string   `toml:"skills_source"`       // Override the host skills source path; empty = auto-detect. Env var $CONSTRUCT_SKILLS_SOURCE wins over this key.
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

// SecurityConfig holds secret redaction and security settings.
type SecurityConfig struct {
	HideSecrets                bool     `toml:"hide_secrets"`                  // Master switch for hide-secrets mode (opt-in, requires CONSTRUCT_EXPERIMENT_HIDE_SECRETS=1)
	HideSecretsMaskStyle       string   `toml:"hide_secrets_mask_style"`       // Mask style: "hash" (default) or "fixed"
	HideSecretsDenyPaths       []string `toml:"hide_secrets_deny_paths"`       // Globs always scanned regardless of heuristics
	HideSecretsAllowPaths      []string `toml:"hide_secrets_allow_paths"`      // Files to NEVER redact (use sparingly! breaks security model)
	HideSecretsPassthroughVars []string `toml:"hide_secrets_passthrough_vars"` // Env vars to never mask (e.g. ["PUBLIC_API_URL"])
	HideSecretsReport          bool     `toml:"hide_secrets_report"`           // Emit session report
	HideGitDir                 bool     `toml:"hide_git_dir"`                  // Hide .git directory in merged view (default true)
}

// DefaultConfig returns the default configuration values.
func DefaultConfig() Config {
	return Config{
		Runtime: RuntimeConfig{
			Backend:             "auto",
			AutoUpdateCheck:     true,
			UpdateCheckInterval: 86400,
			UpdateChannel:       "stable",
		},
		Sandbox: SandboxConfig{
			MountHome:            false,
			AllowHomeWorkspace:   false,
			WorkspaceMaxEntries:  60000,
			ForwardSSHAgent:      true,
			PropagateGitIdentity: true,
			NonRootStrict:        false,
			AllowCustomOverride:  false,
			ExecAsHostUser:       true,
			MountSkills:          true,
			SkillsReadOnly:       true,
			SkillsSource:         "",
			EnvPassthrough: []string{
				"GITHUB_TOKEN",
				"CONTEXT7_API_KEY",
				"BRAVE_API_KEY",
				"OPENAI_API_KEY",
				"ANTHROPIC_API_KEY",
				"GEMINI_API_KEY",
				"ANTIGRAVITY_API_KEY",
				"QWEN_API_KEY",
				"DEEPSEEK_API_KEY",
				"MINIMAX_API_KEY",
				"KIMI_API_KEY",
				"ZAI_API_KEY",
				"MIMO_API_KEY",
				"OPENCODE_API_KEY",
				"PI_CACHE_RETENTION",
			},
			EnvPassthroughPrefixes: []string{"CNSTR_"},
			Shell:                  "/bin/bash",
			ClipboardHost:          "host.docker.internal",
			SelinuxLabels:          "auto",
			HostLoopbackPorts:      []int{80, 443},
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
		Security: SecurityConfig{
			HideSecrets:                false,
			HideSecretsMaskStyle:       "hash",
			HideSecretsDenyPaths:       []string{},
			HideSecretsAllowPaths:      []string{},
			HideSecretsPassthroughVars: []string{},
			HideSecretsReport:          true,
			HideGitDir:                 true,
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
	requiredContainerFiles := []string{
		"Dockerfile",
		"entrypoint.sh",
		"entrypoint-hash.sh",
		"update-all.sh",
		"agent-patch.sh",
	}
	for _, filename := range requiredContainerFiles {
		if _, err := os.Stat(filepath.Join(containerDir, filename)); os.IsNotExist(err) {
			configMissing = true
			break
		}
	}
	if _, err := os.Stat(packagesPath); os.IsNotExist(err) {
		configMissing = true
	}

	createdNew := false
	// Run init if any file is missing
	if configMissing {
		ui.InfoLn("Required files missing. Running initialization...")
		if err := Init(); err != nil {
			return nil, false, fmt.Errorf("initialization failed: %w", err)
		}
		createdNew = true
		ui.InfoLn()
	}

	// Note: Migration check is handled separately in main.go before config.Load()
	// to ensure it runs early in the application lifecycle

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, createdNew, fmt.Errorf("failed to read config file: %w", err)
	}

	// One-time in-file migration: fold the deprecated [runtime] engine key
	// into backend with single-line surgery (comments preserved). When the
	// file cannot be rewritten, the in-memory fold below keeps behavior
	// identical for that session.
	if newText, migrated := migrateLegacyEngineKey(string(data)); migrated {
		if werr := writeFileAtomic(configPath, []byte(newText), 0o644); werr == nil {
			data = []byte(newText)
			ui.InfoLn("♻️  Migrated config.toml: [runtime] engine merged into backend")
		} else {
			ui.InfoF("⚠️  Could not rewrite config.toml to migrate the legacy engine key (%v); applying it in memory for this session\n", werr)
		}
	}

	config := DefaultConfig()
	if err := toml.Unmarshal(data, &config); err != nil {
		return nil, createdNew, fmt.Errorf("failed to parse config.toml: %w", err)
	}

	// In-memory fold for files the rewrite could not touch: a pinned legacy
	// engine must never be silently dropped (auto-detect could pick a
	// different binary than the user pinned).
	foldLegacyEngine(&config)

	return &config, createdNew, nil
}

// writeFileAtomic writes via temp file + rename so a crash mid-write can
// never leave a truncated config.toml behind.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".config-migrate-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, werr := tmp.Write(data); werr != nil {
		tmp.Close()        //nolint:errcheck // best-effort cleanup
		os.Remove(tmpName) //nolint:errcheck // best-effort cleanup
		return werr
	}
	if cerr := tmp.Chmod(perm); cerr != nil {
		tmp.Close()        //nolint:errcheck // best-effort cleanup
		os.Remove(tmpName) //nolint:errcheck // best-effort cleanup
		return cerr
	}
	if cerr := tmp.Close(); cerr != nil {
		os.Remove(tmpName) //nolint:errcheck // best-effort cleanup
		return cerr
	}
	return os.Rename(tmpName, path)
}

// foldLegacyEngine maps a decoded legacy engine value onto Backend.
// Old semantics: engine picked the OCI binary; backend only switched to
// microvm. So a pinned engine wins over a default/"docker" backend, and
// microvm always wins (engine was dead there).
func foldLegacyEngine(cfg *Config) {
	if cfg.Runtime.Engine == "" {
		return
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Runtime.Engine)) {
	case "container", "podman", "docker":
		switch cfg.Runtime.Backend {
		case "", "auto", "docker":
			cfg.Runtime.Backend = strings.ToLower(strings.TrimSpace(cfg.Runtime.Engine))
		}
	}
	// The legacy value is folded: clear it so it can never round-trip into
	// config.toml via Save() when the in-file rewrite could not remove the
	// line (single-quoted or dotted-key TOML shapes decode fine but escape
	// the line surgery).
	cfg.Runtime.Engine = ""
}

// migrateLegacyEngineKey folds the deprecated [runtime] engine key into the
// unified backend key with single-line surgery: comments and every other
// line stay byte-identical. Rules mirror foldLegacyEngine exactly:
//   - engine=container|podman|docker with backend unset/"auto"/"docker": the
//     engine line becomes backend = "<engine>" in place; the explicit
//     backend line is removed (two backend keys in one section would be
//     invalid TOML)
//   - engine with any other explicit backend (podman, microvm, ...): the
//     engine line is dropped; the explicit backend pin governs
//   - engine=auto or unknown: the engine line is dropped (the "auto"
//     default covers it)
//
// The surgery is best-effort for the common shape (double-quoted
// key = "value" inside [runtime]); uncommon-but-valid shapes (single
// quotes, dotted keys) are left untouched and covered by the in-memory
// fold, which also clears the field so it cannot round-trip via Save().
// Returns the new text and whether anything changed. Idempotent: once the
// engine key is gone the function is a no-op.
func migrateLegacyEngineKey(data string) (string, bool) {
	lines := strings.Split(data, "\n")
	section := ""
	engineIdx, backendIdx := -1, -1
	engineVal, backendVal := "", ""
	for i, raw := range lines {
		t := strings.TrimSpace(raw)
		if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
			section = t
			continue
		}
		if section != "[runtime]" {
			continue
		}
		if k, v, ok := tomlQuotedKV(t); ok && k == "engine" && engineIdx == -1 {
			engineIdx, engineVal = i, v
		}
		if k, v, ok := tomlQuotedKV(t); ok && k == "backend" && backendIdx == -1 {
			backendIdx, backendVal = i, v
		}
	}
	if engineIdx == -1 {
		return data, false
	}

	replaceWith := ""
	switch strings.ToLower(engineVal) {
	case "container", "podman", "docker":
		switch backendVal {
		case "", "auto", "docker":
			replaceWith = `backend = "` + strings.ToLower(engineVal) + `"`
		}
	}

	out := make([]string, 0, len(lines))
	for i, raw := range lines {
		switch i {
		case engineIdx:
			if replaceWith != "" {
				out = append(out, replaceWith)
			}
		case backendIdx:
			if replaceWith == "" {
				out = append(out, raw) // keep backend=microvm (or unset value) line
			}
			// else: superseded by the migrated backend line; drop it
		default:
			out = append(out, raw)
		}
	}
	return strings.Join(out, "\n"), true
}

// tomlQuotedKV parses a `key = "value"` line with optional trailing
// comment. Comment lines and non-quoted values are rejected.
func tomlQuotedKV(trimmed string) (key, value string, ok bool) {
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return "", "", false
	}
	eq := strings.Index(trimmed, "=")
	if eq < 0 {
		return "", "", false
	}
	key = strings.TrimSpace(trimmed[:eq])
	rest := strings.TrimSpace(trimmed[eq+1:])
	if !strings.HasPrefix(rest, "\"") {
		return "", "", false
	}
	end := strings.Index(rest[1:], "\"")
	if end < 0 {
		return "", "", false
	}
	return key, rest[1 : end+1], true
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
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			if removeErr := os.RemoveAll(path); removeErr != nil {
				return fmt.Errorf("failed to replace directory %s with file: %w", filepath.Base(path), removeErr)
			}
		} else if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to stat %s: %w", filepath.Base(path), err)
		}

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
				ui.InfoF("⊗ Exists:  %s\n", path)
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
		{filepath.Join(containerDir, "entrypoint-hash.sh"), []byte(templates.EntrypointHash), 0755},
		{filepath.Join(containerDir, "update-all.sh"), []byte(templates.UpdateAll), 0755},
		{filepath.Join(containerDir, "agent-patch.sh"), []byte(templates.AgentPatch), 0755},
		{filepath.Join(containerDir, "network-filter.sh"), []byte(templates.NetworkFilter), 0755},
		{filepath.Join(containerDir, "clipper"), []byte(templates.Clipper), 0755},
		{filepath.Join(containerDir, "clipboard-x11-sync.sh"), []byte(templates.ClipboardX11Sync), 0755},
		{filepath.Join(containerDir, "osascript"), []byte(templates.Osascript), 0755},
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
		ui.InfoLn("\nThe Construct initialized successfully!")
		ui.InfoF("Config directory: %s\n", configPath)
	}

	// Set initial version for new installations
	// This allows future migrations to detect version changes
	SetInitialVersion()

	// Set initial packages template hash
	pkgHash := sha256.Sum256([]byte(templates.Packages))
	SetPackagesTemplateHash(hex.EncodeToString(pkgHash[:]))

	// Set initial per-template hashes (used by migration system)
	SetInitialTemplateHashes()

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

// SetInitialTemplateHashes writes the per-template hash file for a fresh install.
// Single source of truth: template tier lists live in the templates package.
func SetInitialTemplateHashes() {
	all := make(map[string]string)
	for name, content := range templates.ImageTierTemplates {
		h := sha256.Sum256([]byte(content))
		all[name] = hex.EncodeToString(h[:])
	}
	for name, content := range templates.SoftTierTemplates {
		h := sha256.Sum256([]byte(content))
		all[name] = hex.EncodeToString(h[:])
	}

	data, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to marshal template hashes: %v\n", err)
		return
	}

	hashPath := filepath.Join(GetConfigDir(), ".template_hashes")
	if err := os.WriteFile(hashPath, append(data, '\n'), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to write template hashes: %v\n", err)
	}
}

// GetDefaultPackages returns the default packages.toml template content
func GetDefaultPackages() string {
	return templates.Packages
}

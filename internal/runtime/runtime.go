// Package runtime manages container runtime detection and operations.
//
//revive:disable:var-naming // Package name intentionally matches folder name used across imports.
package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	runtime "runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	cerrors "github.com/EstebanForge/construct-cli/internal/cerrors"
	"github.com/EstebanForge/construct-cli/internal/config"
	"github.com/EstebanForge/construct-cli/internal/constants"
	"github.com/EstebanForge/construct-cli/internal/ui"
)

// ContainerState represents the state of a container.
type ContainerState string

// ContainerState values.
const (
	ContainerStateRunning ContainerState = "running"
	ContainerStateExited  ContainerState = "exited"
	ContainerStateMissing ContainerState = "missing"
)

var attemptedOwnershipFix bool

// DetectRuntime selects an available container runtime using parallel detection.
func DetectRuntime(preferredEngine string) string {
	runtimes := []string{"container", "podman", "docker"}

	if preferredEngine != "auto" && preferredEngine != "" {
		runtimes = append([]string{preferredEngine}, runtimes...)
	}

	// First pass: parallel check if runtime is available and running
	// Use a short timeout for parallel checks (500ms total)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	result := checkRuntimesParallel(ctx, runtimes, true) // true = check if already running
	if result != "" {
		return result
	}

	// Second pass: try to start runtimes in order (sequential to avoid races)
	if ui.CurrentLogLevel >= ui.LogLevelInfo {
		fmt.Fprintln(os.Stderr, "No container runtime running - checking available runtimes...")
	}

	for _, rt := range runtimes {
		if _, err := exec.LookPath(rt); err == nil {
			if startRuntime(rt) {
				if waitForRuntime(rt, 60*time.Second) {
					if ui.CurrentLogLevel >= ui.LogLevelInfo {
						fmt.Fprintf(os.Stderr, "✓ Started %s\n", rt)
					}
					return rt
				}
			}
		}
	}

	fmt.Fprintln(os.Stderr, "Error: No container runtime available. Please install Docker, Podman, or use macOS container runtime.")
	os.Exit(1)
	return ""
}

// checkRuntimesParallel checks multiple runtimes in parallel and returns the first one that's ready.
// If checkRunning is true, only returns runtimes that are already running.
// If checkRunning is false, returns any runtime that is installed (available in PATH).
func checkRuntimesParallel(ctx context.Context, runtimes []string, checkRunning bool) string {
	type runtimeResult struct {
		name  string
		ready bool
	}

	results := make(chan runtimeResult, len(runtimes))

	// Launch goroutines for each runtime check
	for _, rt := range runtimes {
		go func(runtimeName string) {
			result := runtimeResult{name: runtimeName, ready: false}

			// Check if runtime is in PATH
			if _, err := exec.LookPath(runtimeName); err != nil {
				results <- result
				return
			}

			// Check if runtime is running (if requested)
			if checkRunning {
				result.ready = IsRuntimeRunning(runtimeName)
			} else {
				result.ready = true // Installed is enough
			}

			results <- result
		}(rt)
	}

	// Wait for first positive result or context timeout
	for i := 0; i < len(runtimes); i++ {
		select {
		case result := <-results:
			if result.ready {
				// Found a ready runtime
				return result.name
			}
		case <-ctx.Done():
			// Timeout - return empty to fall through to sequential startup
			return ""
		}
	}

	return ""
}

// IsRuntimeRunning reports whether the runtime is available and responsive.
func IsRuntimeRunning(runtimeName string) bool {
	var cmd *exec.Cmd

	switch runtimeName {
	case "container":
		// macOS container runtime - check if we're on macOS 26+
		if runtime.GOOS == "darwin" {
			return checkMacOSVersion() >= 26
		}
		return false
	case "podman":
		// Try podman machine list on macOS, podman info on Linux
		if runtime.GOOS == "darwin" {
			cmd = exec.Command("podman", "machine", "list")
		} else {
			cmd = exec.Command("podman", "info", "--format", "{{.Host.RemoteSocket.Exists}}")
		}
	case "docker":
		// Check if docker daemon is responding
		cmd = exec.Command("docker", "info")
	default:
		return false
	}

	if cmd == nil {
		return false
	}

	// Run command without output
	return cmd.Run() == nil
}

func checkMacOSVersion() int {
	// Get macOS version (e.g., 14.2.1 = macOS Sonoma)
	cmd := exec.Command("sw_vers", "-productVersion")
	output, err := cmd.Output()
	if err != nil {
		return 0
	}

	versionStr := strings.TrimSpace(string(output))
	// Parse major version
	parts := strings.Split(versionStr, ".")
	if len(parts) > 0 {
		if major, err := strconv.Atoi(parts[0]); err == nil {
			return major
		}
	}

	return 0
}

func startRuntime(runtimeName string) bool {
	switch runtimeName {
	case "container":
		if runtime.GOOS == "darwin" && checkMacOSVersion() >= 26 {
			fmt.Fprintln(os.Stderr, "macOS container runtime is built-in and should be available")
			return true
		}
		return false

	case "podman":
		if runtime.GOOS == "darwin" {
			// Try to start Podman machine on macOS
			fmt.Fprintln(os.Stderr, "Launching Podman machine...")
			cmd := exec.Command("podman", "machine", "start")
			cmd.Stdout = os.Stderr
			cmd.Stderr = os.Stderr
			return cmd.Run() == nil
		}
		// On Linux (including WSL), try to start podman socket
		fmt.Fprintln(os.Stderr, "Starting Podman service...")
		cmd := exec.Command("systemctl", "--user", "start", "podman.socket")
		if cmd.Run() == nil {
			return true
		}
		// Fallback: try podman systemd
		cmd = exec.Command("systemctl", "start", "podman")
		return cmd.Run() == nil

	case "docker":
		if runtime.GOOS == "darwin" {
			if isOrbStackInstalled() {
				fmt.Fprintln(os.Stderr, "Found OrbStack - launching engine...")
				cmd := exec.Command("open", "-a", "OrbStack")
				if err := cmd.Run(); err == nil {
					return true
				}
			}
			fmt.Fprintln(os.Stderr, "Launching Docker...")
			cmd := exec.Command("open", "-a", "Docker")
			return cmd.Run() == nil
		}
		// On Linux (including WSL), try to start docker service
		fmt.Fprintln(os.Stderr, "Starting Docker service...")
		cmd := exec.Command("systemctl", "start", "docker")
		if cmd.Run() == nil {
			// Give it a moment to start
			time.Sleep(3 * time.Second)
			return true
		}
		// Fallback: try docker via service command
		cmd = exec.Command("service", "docker", "start")
		return cmd.Run() == nil
	}

	return false
}

func waitForRuntime(runtimeName string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		if IsRuntimeRunning(runtimeName) {
			return true
		}
		time.Sleep(2 * time.Second)
	}

	return false
}

func isOrbStackInstalled() bool {
	if runtime.GOOS != "darwin" {
		return false
	}

	if _, err := exec.LookPath("orbctl"); err == nil {
		return true
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}

	paths := []string{
		"/Applications/OrbStack.app",
		filepath.Join(home, "Applications", "OrbStack.app"),
	}

	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			return true
		}
	}

	return false
}

// IsOrbStackRunning checks if OrbStack is currently running on macOS
func IsOrbStackRunning() bool {
	if runtime.GOOS != "darwin" {
		return false
	}

	// Check if OrbStack process is running
	cmd := exec.Command("pgrep", "-f", "OrbStack")
	return cmd.Run() == nil
}

// BuildImage builds the container image and installs agents if needed.
func BuildImage(cfg *config.Config) {
	if shouldSkipImageBuild() {
		if ui.GumAvailable() {
			ui.GumWarning("Skipping image build (CONSTRUCT_SKIP_IMAGE_BUILD)")
		} else {
			fmt.Println("Skipping image build (CONSTRUCT_SKIP_IMAGE_BUILD)")
		}
		return
	}

	containerRuntime := DetectRuntime(cfg.Runtime.Engine)
	configPath := config.GetConfigDir()

	// Create log file for build output
	logFile, err := config.CreateLogFile("build")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to create log file: %v\n", err)
	}
	logPath := ""
	if logFile != nil {
		defer func() {
			if err := logFile.Close(); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: Failed to close log file: %v\n", err)
			}
		}()
		logPath = logFile.Name()
		if ui.GumAvailable() {
			fmt.Printf("%sBuild log: %s%s\n", ui.ColorGrey, logPath, ui.ColorReset)
		} else {
			fmt.Printf("Build log: %s\n", logPath)
		}
	}
	fmt.Println() // Spacer

	var cmd *exec.Cmd

	// Build command with --no-cache to ensure fresh build
	cmd, err = BuildComposeCommand(containerRuntime, configPath, "build", []string{"--no-cache"})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to construct build command: %v\n", err)
		os.Exit(1)
	}
	if containerRuntime == "container" {
		fmt.Println("Note: macOS container runtime detected, using docker for build")
	}

	cmd.Dir = configPath

	// Use helper to run with spinner and handle logging
	if err := ui.RunCommandWithSpinner(cmd, fmt.Sprintf("Building The Construct image using %s...", containerRuntime), logFile); err != nil {
		fmt.Fprintf(os.Stderr, "Error: Build failed: %v\n", err)
		os.Exit(1)
	}

	if ui.GumAvailable() {
		ui.GumSuccess("The Construct image built successfully!")
	} else {
		fmt.Println("\n✓ The Construct image built successfully!")
	}
	if err := config.ClearRebuildRequired(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to clear rebuild marker: %v\n", err)
	}

	// Check if agents are installed and install them if needed
	if !AreAgentsInstalled() {
		if ui.GumAvailable() {
			fmt.Println()
			fmt.Printf("%s🔧 Setup required - installing agents and packages...%s\n", ui.ColorOrange, ui.ColorReset)
			fmt.Printf("%sThis will take 5-10 minutes...%s\n", ui.ColorGrey, ui.ColorReset)
		} else {
			fmt.Println("\n🔧 Setup required - installing agents and packages...")
			fmt.Println("This will take 5-10 minutes...")
		}
		if err := InstallAgentsAfterBuild(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err) // Simple logging for now
			if ui.GumAvailable() {
				ui.GumError("Setup failed.")
			} else {
				fmt.Fprintf(os.Stderr, "\n❌ Setup failed.\n")
			}
			os.Exit(1)
		}
		if ui.GumAvailable() {
			ui.GumSuccess("Setup complete!")
		} else {
			fmt.Println("✅ Setup complete!")
		}
	} else {
		if ui.GumAvailable() {
			ui.GumSuccess("Setup already completed in persistent volumes")
		} else {
			fmt.Println("\n✅ Setup already completed in persistent volumes")
		}
	}
}

func shouldSkipImageBuild() bool {
	value := strings.TrimSpace(strings.ToLower(os.Getenv("CONSTRUCT_SKIP_IMAGE_BUILD")))
	return value == "1" || value == "true" || value == "yes"
}

// AreAgentsInstalled checks if agent binaries exist in the config directory
func AreAgentsInstalled() bool {
	configDir := config.GetConfigDir()
	binDir := filepath.Join(configDir, "home", ".local", "bin")

	candidates := []string{
		"claude",
		"amp",
		"mcp-cli-ent",
		"opencode",
		"gemini",
		"codex",
		"qwen-code",
	}

	for _, name := range candidates {
		if _, err := os.Stat(filepath.Join(binDir, name)); err == nil {
			return true
		}
	}

	return false
}

// InstallAgentsAfterBuild runs the container once to install agents
func InstallAgentsAfterBuild(cfg *config.Config) error {
	containerRuntime := DetectRuntime(cfg.Runtime.Engine)
	configPath := config.GetConfigDir()

	// Prepare runtime environment (network, overrides)
	if err := Prepare(cfg, containerRuntime, configPath); err != nil {
		return &cerrors.ConstructError{
			Category:   cerrors.ErrorCategoryRuntime,
			Operation:  "prepare runtime for agent installation",
			Runtime:    containerRuntime,
			Err:        err,
			Suggestion: "Run 'construct doctor' to diagnose",
		}
	}

	// Run the container once; entrypoint handles setup, then runs the command.
	runFlags := []string{"--rm", "construct-box", "echo", "Installation complete"}

	cmd, err := BuildComposeCommand(containerRuntime, configPath, "run", runFlags)
	if err != nil {
		return &cerrors.ConstructError{
			Category:  cerrors.ErrorCategoryRuntime,
			Operation: "build agent install command",
			Runtime:   containerRuntime,
			Err:       err,
		}
	}
	cmd.Dir = config.GetContainerDir()

	agentLogFile, err := config.CreateLogFile("setup_install")
	if err == nil {
		defer func() {
			if closeErr := agentLogFile.Close(); closeErr != nil {
				fmt.Fprintf(os.Stderr, "Warning: Failed to close agent log file: %v\n", closeErr)
			}
		}()
		if ui.GumAvailable() {
			fmt.Printf("%sSetup log: %s%s\n", ui.ColorGrey, agentLogFile.Name(), ui.ColorReset)
		} else {
			fmt.Printf("Setup log: %s\n", agentLogFile.Name())
		}
	}

	if err := ui.RunCommandWithSpinner(cmd, "Installing agents and user packages...", agentLogFile); err != nil {
		return &cerrors.ConstructError{
			Category:   cerrors.ErrorCategoryContainer,
			Operation:  "run setup",
			Command:    fmt.Sprintf("%s run --rm construct-box echo Installation complete", containerRuntime),
			Runtime:    containerRuntime,
			Err:        err,
			Suggestion: "Check logs or run 'construct doctor'",
		}
	}

	return nil
}

// GetComposeFileArgs returns compose file args for the runtime.
func GetComposeFileArgs(configPath string) []string {
	containerDir := filepath.Join(configPath, "container")
	basePath := filepath.Join(containerDir, "docker-compose.yml")
	overridePath := filepath.Join(containerDir, "docker-compose.override.yml")

	args := []string{"-f", basePath}
	if _, err := os.Stat(overridePath); err == nil {
		args = append(args, "-f", overridePath)
	}
	return args
}

// GetCheckImageCommand returns a runtime-specific image inspect command.
func GetCheckImageCommand(containerRuntime string) []string {
	imageName := "construct-box:latest"

	switch containerRuntime {
	case "docker", "container":
		return []string{"docker", "image", "inspect", imageName}
	case "podman":
		return []string{"podman", "image", "inspect", imageName}
	default:
		return []string{"docker", "image", "inspect", imageName}
	}
}

// Prepare ensures the runtime environment is ready
// - Creates custom network for strict mode if needed
// - Generates docker-compose override for OS-specific settings and network isolation
func Prepare(cfg *config.Config, containerRuntime string, configPath string) error {
	// Fix config directory permissions if needed (Linux/WSL only)
	if err := ensureConfigPermissions(configPath); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to fix config permissions: %v\n", err)
	}

	// Ensure custom network exists for strict mode
	if cfg.Network.Mode == "strict" {
		if err := EnsureCustomNetwork(containerRuntime); err != nil {
			return fmt.Errorf("failed to create custom network: %w", err)
		}
	}

	containerDir := config.GetContainerDir()
	if err := os.MkdirAll(containerDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to create container config directory: %v\n", err)
		warnConfigPermission(err, configPath)
	}

	// Generate OS-specific docker-compose override (Linux UID/GID, SELinux, Network)
	projectPath := GetProjectMountPath()
	if err := GenerateDockerComposeOverride(configPath, projectPath, cfg.Network.Mode, containerRuntime); err != nil {
		return fmt.Errorf("failed to generate docker-compose override: %w", err)
	}

	// Load user packages config and generate installation script
	pkgs, err := config.LoadPackages()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to load packages configuration: %v\n", err)
	} else {
		if err := os.MkdirAll(containerDir, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Failed to create container config directory: %v\n", err)
			warnConfigPermission(err, configPath)
		}
		script := pkgs.GenerateInstallScript()
		scriptPath := filepath.Join(containerDir, "install_user_packages.sh")
		if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Failed to write user installation script: %v\n", err)
			warnConfigPermission(err, configPath)
		}

		topgradeConfig := pkgs.GenerateTopgradeConfig()
		topgradeDir := filepath.Join(configPath, "home", ".config")
		if err := os.MkdirAll(topgradeDir, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Failed to create topgrade config directory: %v\n", err)
			warnConfigPermission(err, configPath)
		} else {
			topgradePath := filepath.Join(topgradeDir, "topgrade.toml")
			if err := os.WriteFile(topgradePath, []byte(topgradeConfig), 0644); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: Failed to write topgrade configuration: %v\n", err)
				warnConfigPermission(err, configPath)
			}
		}
	}

	return nil
}

// EnsureCustomNetwork creates the construct-net custom network if it doesn't exist
func EnsureCustomNetwork(containerRuntime string) error {
	// Check if the network exists
	var checkCmd *exec.Cmd
	switch containerRuntime {
	case "docker", "container":
		checkCmd = exec.Command("docker", "network", "inspect", "construct-net")
	case "podman":
		checkCmd = exec.Command("podman", "network", "inspect", "construct-net")
	}

	// Network exists, nothing to do
	if checkCmd.Run() == nil {
		return nil
	}

	// Create network
	fmt.Println("Creating custom network for strict mode...")
	var createCmd *exec.Cmd
	switch containerRuntime {
	case "docker", "container":
		createCmd = exec.Command("docker", "network", "create",
			"--driver", "bridge",
			"--subnet", "172.28.0.0/16",
			"construct-net")
	case "podman":
		createCmd = exec.Command("podman", "network", "create",
			"--driver", "bridge",
			"--subnet", "172.28.0.0/16",
			"construct-net")
	}

	if err := createCmd.Run(); err != nil {
		return fmt.Errorf("failed to create custom network: %w", err)
	}

	fmt.Println("✓ Custom network 'construct-net' created")
	return nil
}

// GetProjectMountPath returns the dynamic mount path for the current project
func GetProjectMountPath() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "/projects"
	}
	return getProjectMountPathFromDir(cwd)
}

// AppendProjectPathEnv ensures CONSTRUCT_PROJECT_PATH is set for compose interpolation.
func AppendProjectPathEnv(env []string) []string {
	if envHasKey(env, "CONSTRUCT_PROJECT_PATH") {
		return env
	}
	return append(env, "CONSTRUCT_PROJECT_PATH="+GetProjectMountPath())
}

func envHasKey(env []string, key string) bool {
	prefix := key + "="
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			return true
		}
	}
	return false
}

// AppendHostIdentityEnv ensures Linux compose commands include host UID/GID for container startup scripts.
func AppendHostIdentityEnv(env []string) []string {
	if runtime.GOOS != "linux" {
		return env
	}

	env = setEnvVar(env, "CONSTRUCT_HOST_UID", strconv.Itoa(os.Getuid()))
	env = setEnvVar(env, "CONSTRUCT_HOST_GID", strconv.Itoa(os.Getgid()))
	return env
}

func setEnvVar(env []string, key, value string) []string {
	prefix := key + "="
	for i, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}

func getProjectMountPathFromDir(dir string) string {
	projectName := filepath.Base(dir)
	if projectName == "." || projectName == "/" {
		return "/projects/"
	}
	return "/projects/" + projectName
}

// overrideInputs captures all inputs that affect docker-compose.override.yml generation
type overrideInputs struct {
	Version        string // Construct CLI version - ensures cache invalidation on upgrades
	Runtime        string // Container runtime - affects override generation
	UID            int    // Host user ID (Linux only)
	GID            int    // Host group ID (Linux only)
	SELinuxEnabled bool   // SELinux status
	NetworkMode    string // Network isolation mode
	GitName        string // Git user.name config
	GitEmail       string // Git user.email config
	ProjectPath    string // Project mount path
	SSHAuthSock    string // SSH agent socket path
	ForwardSSH     bool   // SSH agent forwarding enabled
	PropagateGit   bool   // Git identity propagation enabled
	DaemonMulti    bool   // Multi-path daemon mounts enabled
	DaemonMounts   string // Hash of normalized mount paths
}

// hashOverrideInputs computes a SHA256 hash of override inputs
func hashOverrideInputs(inputs overrideInputs) string {
	h := sha256.New()
	// Hash all inputs in a deterministic order
	// fmt.Fprintf to hash.Hash cannot error for sha256.New()
	writeHashString(h, "version:%s", inputs.Version)
	writeHashString(h, "runtime:%s", inputs.Runtime)
	writeHashString(h, "uid:%d", inputs.UID)
	writeHashString(h, "gid:%d", inputs.GID)
	writeHashString(h, "selinux:%v", inputs.SELinuxEnabled)
	writeHashString(h, "network:%s", inputs.NetworkMode)
	writeHashString(h, "gitname:%s", inputs.GitName)
	writeHashString(h, "gitemail:%s", inputs.GitEmail)
	writeHashString(h, "project:%s", inputs.ProjectPath)
	writeHashString(h, "sshsock:%s", inputs.SSHAuthSock)
	writeHashString(h, "forwardssh:%v", inputs.ForwardSSH)
	writeHashString(h, "propagategit:%v", inputs.PropagateGit)
	writeHashString(h, "daemonmulti:%v", inputs.DaemonMulti)
	writeHashString(h, "daemonmounts:%s", inputs.DaemonMounts)
	return hex.EncodeToString(h.Sum(nil))
}

// writeHashString writes a formatted string to a hash, ignoring errors
// sha256.Write() never returns an error, so this is safe
func writeHashString(h io.Writer, format string, a ...interface{}) {
	//nolint:errcheck // sha256.Write() never returns an error
	fmt.Fprintf(h, format, a...)
}

// getOverrideHashPath returns the path to the override hash cache file
func getOverrideHashPath(configPath string) string {
	return filepath.Join(configPath, "container", ".override_hash")
}

// readOverrideHash reads the stored override hash from disk
func readOverrideHash(configPath string) string {
	hashPath := getOverrideHashPath(configPath)
	if data, err := os.ReadFile(hashPath); err == nil {
		return strings.TrimSpace(string(data))
	}
	return ""
}

func overrideHasUserMapping(configPath string) (bool, error) {
	overridePath := filepath.Join(configPath, "container", "docker-compose.override.yml")
	data, err := os.ReadFile(overridePath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to read docker-compose.override.yml: %w", err)
	}

	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "user:") {
			return true, nil
		}
	}
	return false, nil
}

// writeOverrideHash stores the override hash to disk
func writeOverrideHash(configPath, hash string) error {
	hashPath := getOverrideHashPath(configPath)
	return os.WriteFile(hashPath, []byte(hash), 0644)
}

// GenerateDockerComposeOverride creates a docker-compose.override.yml file
// Uses hash-based caching to skip regeneration when inputs haven't changed
func GenerateDockerComposeOverride(configPath string, projectPath string, networkMode string, containerRuntime string) error {
	// Load config to check preferences (SSH agent, Git identity, SELinux labels)
	cfg, _, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to load config during runtime preparation: %v\n", err)
	}

	daemonMounts := ResolveDaemonMounts(cfg)
	for _, warning := range daemonMounts.Warnings {
		fmt.Fprintf(os.Stderr, "Warning: %s\n", warning)
	}

	// Determine SELinux suffix
	selinuxSuffix := selinuxSuffixFromConfig(cfg)
	selinuxEnabled := selinuxSuffix != ""

	// Collect all inputs that affect override generation
	hostUID := -1
	hostGID := -1
	if runtime.GOOS == "linux" {
		hostUID = os.Getuid()
		hostGID = os.Getgid()
	}

	// Get Git identity if enabled
	gitName := ""
	gitEmail := ""
	propagateGit := true
	if cfg != nil {
		propagateGit = cfg.Sandbox.PropagateGitIdentity
	}
	if propagateGit {
		gitName = getGitConfig("user.name")
		gitEmail = getGitConfig("user.email")
	}

	// SSH agent socket
	sshAuthSock := os.Getenv("SSH_AUTH_SOCK")
	forwardSSH := true
	if cfg != nil {
		forwardSSH = cfg.Sandbox.ForwardSSHAgent
	}
	if !forwardSSH {
		sshAuthSock = "" // Don't include in hash if forwarding disabled
	}

	// Build override inputs struct
	inputs := overrideInputs{
		Version:        constants.Version,
		Runtime:        containerRuntime,
		UID:            hostUID,
		GID:            hostGID,
		SELinuxEnabled: selinuxEnabled,
		NetworkMode:    networkMode,
		GitName:        gitName,
		GitEmail:       gitEmail,
		ProjectPath:    projectPath,
		SSHAuthSock:    sshAuthSock,
		ForwardSSH:     forwardSSH,
		PropagateGit:   propagateGit,
		DaemonMulti:    cfg != nil && cfg.Daemon.MultiPathsEnabled,
		DaemonMounts:   daemonMounts.Hash,
	}

	// Check if override needs regeneration
	currentHash := hashOverrideInputs(inputs)
	storedHash := readOverrideHash(configPath)
	if currentHash == storedHash {
		// Safety check: on Linux Docker, "user:" in override can break brew/npm permissions.
		if runtime.GOOS == "linux" && containerRuntime == "docker" {
			hasUserMapping, err := overrideHasUserMapping(configPath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
			} else if hasUserMapping {
				if cfg != nil && cfg.Sandbox.NonRootStrict {
					// non_root_strict explicitly requires user mapping; keep current override.
					return nil
				}
				fmt.Fprintln(os.Stderr, "Warning: docker-compose.override.yml contains a manual 'user:' mapping for Docker.")
				fmt.Fprintln(os.Stderr, "Warning: This may cause Homebrew/npm permission errors. Regenerating override without 'user:'.")
			} else {
				// Inputs unchanged and no unsafe manual override - skip regeneration.
				return nil
			}
		} else {
			// Inputs unchanged - skip regeneration
			return nil
		}
	}

	// Inputs changed - regenerate override file
	var override strings.Builder

	override.WriteString("# Auto-generated docker-compose.override.yml\n")
	override.WriteString("# This file provides runtime-specific configurations\n\n")
	override.WriteString("services:\n  construct-box:\n")
	fmt.Fprintf(&override, "    working_dir: %s\n", projectPath)
	if daemonMounts.Enabled {
		override.WriteString("    labels:\n")
		fmt.Fprintf(&override, "      - %s=%s\n", DaemonMountsLabelKey, daemonMounts.Hash)
	}

	if selinuxEnabled {
		fmt.Println("✓ SELinux labels enabled for volume mounts")
	}
	projectSelinuxSuffix := selinuxSuffix
	if selinuxEnabled && isHomeCwd() {
		projectSelinuxSuffix = ""
		fmt.Println("Warning: SELinux relabeling of home directory is not allowed; skipping :z for project mount")
		fmt.Println("Warning: Run from a project directory to re-enable SELinux labeling for the workspace")
	}

	nonRootStrict := cfg != nil && cfg.Sandbox.NonRootStrict
	// Linux-specific user mapping behavior:
	// - Podman: always force user mapping for rootless compatibility.
	// - Docker: only force user mapping when explicitly requested via non_root_strict.
	// Default Docker flow runs as root briefly to fix permissions, then drops privileges.
	if runtime.GOOS == "linux" && (containerRuntime == "podman" || (containerRuntime == "docker" && nonRootStrict)) {
		fmt.Fprintf(&override, "    user: \"%d:%d\"\n", hostUID, hostGID)
		if containerRuntime == "podman" {
			fmt.Printf("✓ Container (podman) will run as user %d:%d\n", hostUID, hostGID)
		} else {
			fmt.Printf("⚠️  non_root_strict enabled: Docker container will run as user %d:%d\n", hostUID, hostGID)
			fmt.Println("⚠️  Limitations: root bootstrap permission fixes are disabled; brew/npm installs may fail on first run.")
			fmt.Println("⚠️  Recommendation: prefer runtime.engine='podman' for strict non-root workflows.")
		}
	}

	// Volumes block
	override.WriteString("    volumes:\n")

	// Platform-specific volume declarations
	switch runtime.GOOS {
	case "linux":
		// On Linux, we must re-declare base volumes to apply permissions/SELinux labels correctly
		fmt.Fprintf(&override, "      - ${PWD}:%s%s\n", projectPath, projectSelinuxSuffix)
		for _, mount := range daemonMounts.Mounts {
			fmt.Fprintf(&override, "      - %s:%s%s\n", mount.HostPath, mount.ContainerPath, selinuxSuffix)
		}
		fmt.Fprintf(&override, "      - ~/.config/construct-cli/home:/home/construct%s\n", selinuxSuffix)
		fmt.Fprintf(&override, "      - ~/.config/construct-cli/container/install_user_packages.sh:/home/construct/.config/construct-cli/container/install_user_packages.sh%s\n", selinuxSuffix)
		fmt.Fprintf(&override, "      - ~/.config/construct-cli/container/entrypoint-hash.sh:/home/construct/.config/construct-cli/container/entrypoint-hash.sh%s\n", selinuxSuffix)
		fmt.Fprintf(&override, "      - ~/.config/construct-cli/container/update-all.sh:/home/construct/.config/construct-cli/container/update-all.sh%s\n", selinuxSuffix)
		fmt.Fprintf(&override, "      - ~/.config/construct-cli/container/agent-patch.sh:/home/construct/.config/construct-cli/container/agent-patch.sh%s\n", selinuxSuffix)
		override.WriteString("      - construct-packages:/home/linuxbrew/.linuxbrew\n")
	case "darwin":
		fmt.Fprintf(&override, "      - ${PWD}:%s%s\n", projectPath, projectSelinuxSuffix)
		for _, mount := range daemonMounts.Mounts {
			fmt.Fprintf(&override, "      - %s:%s%s\n", mount.HostPath, mount.ContainerPath, selinuxSuffix)
		}
		fmt.Fprintf(&override, "      - ~/.config/construct-cli/home:/home/construct%s\n", selinuxSuffix)
		fmt.Fprintf(&override, "      - ~/.config/construct-cli/container/install_user_packages.sh:/home/construct/.config/construct-cli/container/install_user_packages.sh%s\n", selinuxSuffix)
		fmt.Fprintf(&override, "      - ~/.config/construct-cli/container/entrypoint-hash.sh:/home/construct/.config/construct-cli/container/entrypoint-hash.sh%s\n", selinuxSuffix)
		fmt.Fprintf(&override, "      - ~/.config/construct-cli/container/update-all.sh:/home/construct/.config/construct-cli/container/update-all.sh%s\n", selinuxSuffix)
		fmt.Fprintf(&override, "      - ~/.config/construct-cli/container/agent-patch.sh:/home/construct/.config/construct-cli/container/agent-patch.sh%s\n", selinuxSuffix)
		override.WriteString("      - construct-packages:/home/linuxbrew/.linuxbrew\n")
	}

	// SSH Agent Forwarding
	forwardAgent := forwardSSH
	sshSockPath := sshAuthSock
	if forwardAgent && sshSockPath != "" {
		if runtime.GOOS == "linux" {
			// Standard Linux socket mounting
			fmt.Fprintf(&override, "      - %s:/ssh-agent%s\n", sshSockPath, selinuxSuffix)
			fmt.Println("✓ SSH Agent forwarding configured")
		}
		// On macOS, we use a TCP bridge handled in agent/runner.go and entrypoint.sh
	}

	// Extra hosts for Linux (host.docker.internal)
	if runtime.GOOS == "linux" {
		override.WriteString("    extra_hosts:\n")
		override.WriteString("      - \"host.docker.internal:host-gateway\"\n")
	}

	// Environment variables
	override.WriteString("    environment:\n")
	if nonRootStrict {
		override.WriteString("      - CONSTRUCT_NON_ROOT_STRICT=1\n")
	}

	// Propagate Git Identity
	propagateGitIdentity := propagateGit

	if propagateGitIdentity {
		if name := gitName; name != "" {
			fmt.Fprintf(&override, "      - GIT_AUTHOR_NAME=%s\n", name)
			fmt.Fprintf(&override, "      - GIT_COMMITTER_NAME=%s\n", name)
		}
		if email := gitEmail; email != "" {
			fmt.Fprintf(&override, "      - GIT_AUTHOR_EMAIL=%s\n", email)
			fmt.Fprintf(&override, "      - GIT_COMMITTER_EMAIL=%s\n", email)
		}
	}

	if forwardAgent && sshSockPath != "" {
		if runtime.GOOS == "linux" {
			override.WriteString("      - SSH_AUTH_SOCK=/ssh-agent\n")
		}
		// On macOS, SSH_AUTH_SOCK is set in entrypoint.sh via the TCP bridge
	}

	// Network isolation mode
	switch networkMode {
	case "offline":
		override.WriteString("    network_mode: none\n")
		fmt.Println("✓ Network isolation: offline (no network access)")
	case "strict":
		override.WriteString("    networks:\n")
		override.WriteString("      - construct-net\n")
		override.WriteString("    cap_add:\n")
		override.WriteString("      - NET_ADMIN\n")
		fmt.Println("✓ Network isolation: strict (allowlist mode)")

		// Add network definition for strict mode
		override.WriteString("\nnetworks:\n  construct-net:\n    name: construct-net\n    driver: bridge\n")
	}

	// Write override file
	overridePath := filepath.Join(configPath, "container", "docker-compose.override.yml")
	if err := os.WriteFile(overridePath, []byte(override.String()), 0644); err != nil {
		return fmt.Errorf("failed to write docker-compose.override.yml: %w", err)
	}

	// Store hash for next time
	if err := writeOverrideHash(configPath, currentHash); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to write override hash: %v\n", err)
	}

	return nil
}

// IsSELinuxEnabled checks if SELinux is enabled on the system
func IsSELinuxEnabled() bool {
	if runtime.GOOS != "linux" {
		return false
	}

	// Check if SELinux is enforcing or permissive
	cmd := exec.Command("getenforce")
	output, err := cmd.Output()
	if err != nil {
		return false
	}

	status := strings.TrimSpace(string(output))
	return status == "Enforcing" || status == "Permissive"
}

func selinuxSuffixFromConfig(cfg *config.Config) string {
	if runtime.GOOS != "linux" {
		return ""
	}

	mode := "auto"
	if cfg != nil {
		mode = strings.TrimSpace(strings.ToLower(cfg.Sandbox.SelinuxLabels))
	}

	switch mode {
	case "", "auto":
		if IsSELinuxEnabled() {
			return ":z"
		}
		return ""
	case "enabled", "true", "yes", "on":
		return ":z"
	case "disabled", "false", "no", "off":
		return ""
	default:
		fmt.Fprintf(os.Stderr, "Warning: Unknown sandbox.selinux_labels value: %s (using auto)\n", mode)
		if IsSELinuxEnabled() {
			return ":z"
		}
		return ""
	}
}

func isHomeCwd() bool {
	cwd, err := os.Getwd()
	if err != nil {
		return false
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	return samePath(cwd, homeDir)
}

func samePath(a, b string) bool {
	a = filepath.Clean(a)
	b = filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// ensureConfigPermissions checks and fixes config directory ownership on Linux
func ensureConfigPermissions(configPath string) error {
	// Only needed on Linux where we mount volumes with potential UID mismatches
	if runtime.GOOS != "linux" {
		return nil
	}

	// Already attempted fix this session
	if attemptedOwnershipFix {
		return nil
	}

	// Check critical directories that need to be writable
	checkPaths := []string{
		filepath.Join(configPath, "home"),
		filepath.Join(configPath, "container"),
	}

	// Check if any directory is not writable
	var problemPaths []string
	var ownedButUnwritable []string
	for _, path := range checkPaths {
		// Skip if directory doesn't exist yet (will be created with correct ownership)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			continue
		}

		// Try to create a test file to check writability
		testFile := filepath.Join(path, ".construct-write-test")
		if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
			ownerUID, ownerErr := getOwnerUID(path)
			if ownerErr != nil {
				problemPaths = append(problemPaths, path)
			} else if ownerUID != os.Getuid() {
				problemPaths = append(problemPaths, path)
			} else {
				ownedButUnwritable = append(ownedButUnwritable, path)
			}
			continue
		}
		// Ignore cleanup errors - we're just testing writability
		//nolint:errcheck
		os.Remove(testFile)
	}

	if len(problemPaths) == 0 {
		if len(ownedButUnwritable) > 0 {
			fmt.Fprintf(os.Stderr, "\n%sWarning: Config directory permissions prevent writing%s\n", ui.ColorYellow, ui.ColorReset)
			fmt.Fprintf(os.Stderr, "The following directories are owned by you but not writable:\n")
			for _, p := range ownedButUnwritable {
				fmt.Fprintf(os.Stderr, "  • %s\n", p)
			}
		}
		return nil
	}

	// Mark that we've attempted a fix this session
	attemptedOwnershipFix = true

	// Get current user info
	username := currentUserName()
	if username == "" {
		username = "root"
	}

	// Explain the problem clearly
	fmt.Fprintf(os.Stderr, "\n%sWarning: Config directory has incorrect ownership%s\n", ui.ColorYellow, ui.ColorReset)
	fmt.Fprintf(os.Stderr, "The following directories are not writable by your user (%s):\n", username)
	for _, p := range problemPaths {
		fmt.Fprintf(os.Stderr, "  • %s\n", p)
	}
	fmt.Fprintf(os.Stderr, "\nThis typically happens when the container created files as a different user.\n")
	fmt.Fprintf(os.Stderr, "Fix: %ssudo chown -R %s:%s %s%s\n\n", ui.ColorCyan, username, username, configPath, ui.ColorReset)

	// Try non-interactive sudo first (best effort, avoids blocking in non-interactive sessions).
	if err := runOwnershipFixNonInteractive(configPath); err == nil {
		if ui.GumAvailable() {
			ui.GumSuccess("Config directory ownership fixed")
		} else {
			fmt.Fprintf(os.Stderr, "✓ Config directory ownership fixed\n")
		}
		return nil
	}

	// Ask for confirmation before running sudo
	if !ui.GumConfirm("Attempt to fix ownership now with sudo?") {
		fmt.Fprintf(os.Stderr, "Skipping automatic fix. You can run the command manually.\n")
		return nil
	}

	// Fix the entire config path to catch all files
	if err := runOwnershipFix(configPath); err != nil {
		return fmt.Errorf("failed to fix ownership: %w", err)
	}

	if ui.GumAvailable() {
		ui.GumSuccess("Config directory ownership fixed")
	} else {
		fmt.Fprintf(os.Stderr, "✓ Config directory ownership fixed\n")
	}
	return nil
}

func warnConfigPermission(err error, configPath string) {
	if !errors.Is(err, os.ErrPermission) {
		return
	}
	fmt.Fprintf(os.Stderr, "Warning: Config directory is not writable: %s\n", configPath)
	fmt.Fprintf(os.Stderr, "%sWarning: Fix ownership with: sudo chown -R $USER:$USER %s%s\n", ui.ColorYellow, configPath, ui.ColorReset)
	if runtime.GOOS != "linux" || attemptedOwnershipFix {
		return
	}
	shouldFix, ownerErr := shouldAttemptOwnershipFix(configPath)
	if ownerErr == nil && !shouldFix {
		return
	}
	attemptedOwnershipFix = true

	// Try non-interactive sudo first (best effort).
	if err := runOwnershipFixNonInteractive(configPath); err == nil {
		if ui.GumAvailable() {
			ui.GumSuccess("Ownership fixed")
		} else {
			fmt.Println("✅ Ownership fixed")
		}
		return
	}

	if !ui.GumConfirm(fmt.Sprintf("Attempt to fix ownership now with sudo? (%s)", configPath)) {
		return
	}
	if err := runOwnershipFix(configPath); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Ownership fix failed: %v\n", err)
	} else if ui.GumAvailable() {
		ui.GumSuccess("Ownership fixed")
	} else {
		fmt.Println("✅ Ownership fixed")
	}
}

func shouldAttemptOwnershipFix(path string) (bool, error) {
	ownerUID, err := getOwnerUID(path)
	if err != nil {
		return false, err
	}
	return ownerUID != os.Getuid(), nil
}

func getOwnerUID(path string) (int, error) {
	info, err := os.Stat(path)
	if err != nil {
		return -1, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return -1, fmt.Errorf("unsupported file stat for %s", path)
	}
	return int(stat.Uid), nil
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

func runOwnershipFixNonInteractive(configPath string) error {
	if _, err := exec.LookPath("sudo"); err != nil {
		return fmt.Errorf("sudo not available: %w", err)
	}
	if err := exec.Command("sudo", "-n", "true").Run(); err != nil {
		return fmt.Errorf("sudo non-interactive check failed: %w", err)
	}

	uid := os.Getuid()
	gid := os.Getgid()
	cmd := exec.Command("sudo", "-n", "chown", "-R", fmt.Sprintf("%d:%d", uid, gid), configPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return err
		}
		return fmt.Errorf("%w: %s", err, msg)
	}
	return nil
}

func currentUserName() string {
	if userName := os.Getenv("USER"); userName != "" {
		return userName
	}
	current, err := user.Current()
	if err != nil {
		return ""
	}
	return current.Username
}

// ExecInContainer executes a command in a running container and returns output
func ExecInContainer(containerRuntime, containerName string, cmdArgs []string) (string, error) {
	var cmd *exec.Cmd

	switch containerRuntime {
	case "docker", "container":
		args := append([]string{"exec", containerName}, cmdArgs...)
		cmd = exec.Command("docker", args...)
	case "podman":
		args := append([]string{"exec", containerName}, cmdArgs...)
		cmd = exec.Command("podman", args...)
	default:
		return "", fmt.Errorf("unsupported runtime: %s", containerRuntime)
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to exec in container: %w", err)
	}

	return string(output), nil
}

// ExecInContainerWithEnv executes a command in a running container with environment variables.
func ExecInContainerWithEnv(containerRuntime, containerName string, cmdArgs []string, envVars []string, user string) (string, error) {
	var cmd *exec.Cmd

	args := make([]string, 0, 3+2*len(envVars)+len(cmdArgs))
	args = append(args, "exec")
	if user != "" {
		args = append(args, "-u", user)
	}
	for _, envVar := range envVars {
		args = append(args, "-e", envVar)
	}
	args = append(args, containerName)
	args = append(args, cmdArgs...)

	switch containerRuntime {
	case "docker", "container":
		cmd = exec.Command("docker", args...)
	case "podman":
		cmd = exec.Command("podman", args...)
	default:
		return "", fmt.Errorf("unsupported runtime: %s", containerRuntime)
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(output))
		if msg == "" {
			return "", fmt.Errorf("failed to exec in container: %w", err)
		}
		return string(output), fmt.Errorf("failed to exec in container: %w: %s", err, msg)
	}

	return string(output), nil
}

// ContainerHasUIDEntry reports whether /etc/passwd in the container has an entry for uid.
func ContainerHasUIDEntry(containerRuntime, containerName string, uid int) (bool, error) {
	cmdArgs := []string{"sh", "-lc", fmt.Sprintf("grep -qE '^[^:]*:[^:]*:%d:' /etc/passwd", uid)}
	_, err := ExecInContainerWithEnv(containerRuntime, containerName, cmdArgs, nil, "")
	if err == nil {
		return true, nil
	}
	if strings.Contains(err.Error(), "unsupported runtime") {
		return false, err
	}
	return false, nil
}

// ExecInteractive executes a command interactively in a running container
// with stdin/stdout/stderr passed through. Returns the exit code.
func ExecInteractive(containerRuntime, containerName string, cmdArgs []string, envVars []string, workdir string) (int, error) {
	return ExecInteractiveAsUser(containerRuntime, containerName, cmdArgs, envVars, workdir, "")
}

// ExecInteractiveAsUser executes a command interactively in a running container as a specific user.
// with stdin/stdout/stderr passed through. Returns the exit code.
func ExecInteractiveAsUser(containerRuntime, containerName string, cmdArgs []string, envVars []string, workdir, user string) (int, error) {
	var cmd *exec.Cmd

	// Build exec args with -it for interactive + tty
	// Preallocate capacity for: "exec", "-it", optional "-w", env vars, container name, cmd args
	capacity := 2 + (2 * len(envVars)) + 1 + len(cmdArgs)
	if workdir != "" {
		capacity += 2
	}
	if user != "" {
		capacity += 2
	}
	args := make([]string, 0, capacity)
	args = append(args, "exec", "-it")

	if workdir != "" {
		args = append(args, "-w", workdir)
	}

	if user != "" {
		args = append(args, "-u", user)
	}

	// Add environment variables
	for _, env := range envVars {
		args = append(args, "-e", env)
	}

	args = append(args, containerName)
	args = append(args, cmdArgs...)

	switch containerRuntime {
	case "docker", "container":
		cmd = exec.Command("docker", args...)
	case "podman":
		cmd = exec.Command("podman", args...)
	default:
		return 1, fmt.Errorf("unsupported runtime: %s", containerRuntime)
	}

	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err := cmd.Run()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode(), nil
		}
		return 1, fmt.Errorf("failed to exec in container: %w", err)
	}

	return 0, nil
}

// GetContainerWorkingDir returns the configured working directory for a container.
func GetContainerWorkingDir(containerRuntime, containerName string) (string, error) {
	var cmd *exec.Cmd

	switch containerRuntime {
	case "docker", "container":
		cmd = exec.Command("docker", "inspect", "--format", "{{.Config.WorkingDir}}", containerName)
	case "podman":
		cmd = exec.Command("podman", "inspect", "--format", "{{.Config.WorkingDir}}", containerName)
	default:
		return "", fmt.Errorf("unsupported runtime: %s", containerRuntime)
	}

	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to inspect container working dir: %w", err)
	}

	return strings.TrimSpace(string(output)), nil
}

// GetContainerMountSource returns the host source path for a given container mount destination.
func GetContainerMountSource(containerRuntime, containerName, destination string) (string, error) {
	var cmd *exec.Cmd
	format := fmt.Sprintf("{{range .Mounts}}{{if eq .Destination %q}}{{.Source}}{{end}}{{end}}", destination)

	switch containerRuntime {
	case "docker", "container":
		cmd = exec.Command("docker", "inspect", "--format", format, containerName)
	case "podman":
		cmd = exec.Command("podman", "inspect", "--format", format, containerName)
	default:
		return "", fmt.Errorf("unsupported runtime: %s", containerRuntime)
	}

	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to inspect container mounts: %w", err)
	}

	source := strings.TrimSpace(string(output))
	if source == "" {
		return "", fmt.Errorf("mount destination not found: %s", destination)
	}

	return source, nil
}

// GetContainerImageID returns the image ID that a container is using
func GetContainerImageID(containerRuntime, containerName string) (string, error) {
	var cmd *exec.Cmd

	switch containerRuntime {
	case "docker", "container":
		cmd = exec.Command("docker", "inspect", "--format", "{{.Image}}", containerName)
	case "podman":
		cmd = exec.Command("podman", "inspect", "--format", "{{.Image}}", containerName)
	default:
		return "", fmt.Errorf("unsupported runtime: %s", containerRuntime)
	}

	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get container image ID: %w", err)
	}

	return strings.TrimSpace(string(output)), nil
}

// GetImageID returns the ID of an image by name
func GetImageID(containerRuntime, imageName string) (string, error) {
	var cmd *exec.Cmd

	switch containerRuntime {
	case "docker", "container":
		cmd = exec.Command("docker", "image", "inspect", "--format", "{{.Id}}", imageName)
	case "podman":
		cmd = exec.Command("podman", "image", "inspect", "--format", "{{.Id}}", imageName)
	default:
		return "", fmt.Errorf("unsupported runtime: %s", containerRuntime)
	}

	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get image ID: %w", err)
	}

	return strings.TrimSpace(string(output)), nil
}

// IsContainerStale checks if a container is running an outdated image
func IsContainerStale(containerRuntime, containerName, imageName string) bool {
	containerImageID, err := GetContainerImageID(containerRuntime, containerName)
	if err != nil {
		return true // Assume stale if we can't check
	}

	currentImageID, err := GetImageID(containerRuntime, imageName)
	if err != nil {
		return true // Assume stale if we can't check
	}

	return containerImageID != currentImageID
}

// GetContainerState checks the state of a container
func GetContainerState(containerRuntime, containerName string) ContainerState {
	if !ContainerExists(containerRuntime, containerName) {
		return ContainerStateMissing
	}

	if IsContainerRunning(containerRuntime, containerName) {
		return ContainerStateRunning
	}

	return ContainerStateExited
}

// ContainerExists checks if a container exists (running or stopped)
func ContainerExists(containerRuntime, containerName string) bool {
	var cmd *exec.Cmd

	switch containerRuntime {
	case "docker", "container":
		cmd = exec.Command("docker", "ps", "-a", "--filter", fmt.Sprintf("name=^%s$", containerName), "--format", "{{.Names}}")
	case "podman":
		cmd = exec.Command("podman", "ps", "-a", "--filter", fmt.Sprintf("name=^%s$", containerName), "--format", "{{.Names}}")
	default:
		return false
	}

	output, err := cmd.Output()
	if err != nil {
		return false
	}

	return strings.TrimSpace(string(output)) == containerName
}

// IsContainerRunning checks if a container is currently running
func IsContainerRunning(containerRuntime, containerName string) bool {
	var cmd *exec.Cmd

	switch containerRuntime {
	case "docker", "container":
		cmd = exec.Command("docker", "ps", "--filter", fmt.Sprintf("name=^%s$", containerName), "--format", "{{.Names}}")
	case "podman":
		cmd = exec.Command("podman", "ps", "--filter", fmt.Sprintf("name=^%s$", containerName), "--format", "{{.Names}}")
	default:
		return false
	}

	output, err := cmd.Output()
	if err != nil {
		return false
	}

	return strings.TrimSpace(string(output)) == containerName
}

// CleanupExitedContainer removes a stopped container
func CleanupExitedContainer(containerRuntime, containerName string) error {
	var cmd *exec.Cmd

	switch containerRuntime {
	case "docker", "container":
		cmd = exec.Command("docker", "rm", containerName)
	case "podman":
		cmd = exec.Command("podman", "rm", containerName)
	default:
		return fmt.Errorf("unsupported runtime: %s", containerRuntime)
	}

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to remove container: %w", err)
	}

	return nil
}

// StartContainer starts a stopped container
func StartContainer(containerRuntime, containerName string) error {
	var cmd *exec.Cmd

	switch containerRuntime {
	case "docker", "container":
		cmd = exec.Command("docker", "start", containerName)
	case "podman":
		cmd = exec.Command("podman", "start", containerName)
	default:
		return fmt.Errorf("unsupported runtime: %s", containerRuntime)
	}

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to start container: %w", err)
	}

	return nil
}

// StopContainer stops a running container
func StopContainer(containerRuntime, containerName string) error {
	var cmd *exec.Cmd

	switch containerRuntime {
	case "docker", "container":
		cmd = exec.Command("docker", "stop", containerName)
	case "podman":
		cmd = exec.Command("podman", "stop", containerName)
	default:
		return fmt.Errorf("unsupported runtime: %s", containerRuntime)
	}

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to stop container: %w", err)
	}

	return nil
}

// getGitConfig retrieves a git configuration value from the host
func getGitConfig(key string) string {
	cmd := exec.Command("git", "config", key)
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

// BuildComposeCommand constructs a docker-compose command
func BuildComposeCommand(containerRuntime, configPath, subCommand string, args []string) (*exec.Cmd, error) {
	composeArgs := GetComposeFileArgs(configPath)
	var cmd *exec.Cmd

	if containerRuntime == "docker" {
		if _, err := exec.LookPath("docker-compose"); err == nil {
			cmdArgs := make([]string, 0, 1+len(composeArgs)+1+len(args))
			cmdArgs = append(cmdArgs, "docker-compose")
			cmdArgs = append(cmdArgs, composeArgs...)
			cmdArgs = append(cmdArgs, subCommand)
			cmdArgs = append(cmdArgs, args...)
			cmd = exec.Command("docker-compose", cmdArgs[1:]...)
		} else {
			cmdArgs := make([]string, 0, 2+len(composeArgs)+1+len(args))
			cmdArgs = append(cmdArgs, "docker", "compose")
			cmdArgs = append(cmdArgs, composeArgs...)
			cmdArgs = append(cmdArgs, subCommand)
			cmdArgs = append(cmdArgs, args...)
			cmd = exec.Command("docker", cmdArgs[1:]...)
		}
	}
	if containerRuntime == "podman" {
		if _, err := exec.LookPath("podman-compose"); err == nil {
			cmdArgs := make([]string, 0, 1+len(composeArgs)+1+len(args))
			cmdArgs = append(cmdArgs, "podman-compose")
			cmdArgs = append(cmdArgs, composeArgs...)
			cmdArgs = append(cmdArgs, subCommand)
			cmdArgs = append(cmdArgs, args...)
			cmd = exec.Command("podman-compose", cmdArgs[1:]...)
		} else {
			cmdArgs := make([]string, 0, 2+len(composeArgs)+1+len(args))
			cmdArgs = append(cmdArgs, "podman", "compose")
			cmdArgs = append(cmdArgs, composeArgs...)
			cmdArgs = append(cmdArgs, subCommand)
			cmdArgs = append(cmdArgs, args...)
			cmd = exec.Command("podman", cmdArgs[1:]...)
		}
	}
	if containerRuntime == "container" {
		cmdArgs := make([]string, 0, 2+len(composeArgs)+1+len(args))
		cmdArgs = append(cmdArgs, "docker", "compose")
		cmdArgs = append(cmdArgs, composeArgs...)
		cmdArgs = append(cmdArgs, subCommand)
		cmdArgs = append(cmdArgs, args...)
		cmd = exec.Command("docker", cmdArgs[1:]...)
	}
	if cmd == nil {
		return nil, fmt.Errorf("unsupported runtime: %s", containerRuntime)
	}
	cmd.Env = AppendHostIdentityEnv(AppendProjectPathEnv(os.Environ()))
	return cmd, nil
}

// GetPlatformRunFlags returns platform-specific flags for the 'run' command
func GetPlatformRunFlags() []string {
	if runtime.GOOS == "linux" {
		return []string{"--add-host", "host.docker.internal:host-gateway"}
	}
	return []string{}
}

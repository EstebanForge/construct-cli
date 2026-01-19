// Package runtime manages container runtime detection and operations.
package runtime

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	cerrors "github.com/EstebanForge/construct-cli/internal/cerrors"
	"github.com/EstebanForge/construct-cli/internal/config"
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

// DetectRuntime selects an available container runtime.
func DetectRuntime(preferredEngine string) string {
	runtimes := []string{"container", "podman", "docker"}

	if preferredEngine != "auto" && preferredEngine != "" {
		runtimes = append([]string{preferredEngine}, runtimes...)
	}

	// First pass: check if runtime is available
	for _, rt := range runtimes {
		if _, err := exec.LookPath(rt); err == nil {
			if IsRuntimeRunning(rt) {
				return rt
			}
		}
	}

	// Second pass: try to start runtimes in order
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

	// Generate OS-specific docker-compose override (Linux UID/GID, SELinux, Network)
	projectPath := GetProjectMountPath()
	if err := GenerateDockerComposeOverride(configPath, projectPath, cfg.Network.Mode); err != nil {
		return fmt.Errorf("failed to generate docker-compose override: %w", err)
	}

	// Load user packages config and generate installation script
	pkgs, err := config.LoadPackages()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to load packages configuration: %v\n", err)
	} else {
		containerDir := config.GetContainerDir()
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
		return "/workspace"
	}
	return getProjectMountPathFromDir(cwd)
}

func getProjectMountPathFromDir(dir string) string {
	projectName := filepath.Base(dir)
	if projectName == "." || projectName == "/" {
		return "/projects/"
	}
	return "/projects/" + projectName
}

// GenerateDockerComposeOverride creates a docker-compose.override.yml file
func GenerateDockerComposeOverride(configPath string, projectPath string, networkMode string) error {
	var override strings.Builder

	override.WriteString("# Auto-generated docker-compose.override.yml\n")
	override.WriteString("# This file provides runtime-specific configurations\n\n")
	override.WriteString("services:\n  construct-box:\n")
	fmt.Fprintf(&override, "    working_dir: %s\n", projectPath)

	// Load config to check preferences (SSH agent, Git identity, SELinux labels)
	cfg, _, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to load config during runtime preparation: %v\n", err)
	}

	// Determine SELinux suffix
	selinuxSuffix := selinuxSuffixFromConfig(cfg)
	if selinuxSuffix != "" {
		fmt.Println("✓ SELinux labels enabled for volume mounts")
	}
	projectSelinuxSuffix := selinuxSuffix
	if selinuxSuffix != "" && isHomeCwd() {
		projectSelinuxSuffix = ""
		fmt.Println("Warning: SELinux relabeling of home directory is not allowed; skipping :z for project mount")
		fmt.Println("Warning: Run from a project directory to re-enable SELinux labeling for the workspace")
	}

	// Linux-specific: Always run as user (not root) for Podman compatibility
	// Host-side permission fixes (ensureConfigPermissions) handle ownership before mounting
	if runtime.GOOS == "linux" {
		uid := os.Getuid()
		gid := os.Getgid()
		fmt.Fprintf(&override, "    user: \"%d:%d\"\n", uid, gid)
		fmt.Printf("✓ Container will run as user %d:%d\n", uid, gid)
	}

	// Volumes block
	override.WriteString("    volumes:\n")

	// Platform-specific volume declarations
	switch runtime.GOOS {
	case "linux":
		// On Linux, we must re-declare base volumes to apply permissions/SELinux labels correctly
		fmt.Fprintf(&override, "      - ${PWD}:%s%s\n", projectPath, projectSelinuxSuffix)
		fmt.Fprintf(&override, "      - ~/.config/construct-cli/home:/home/construct%s\n", selinuxSuffix)
		fmt.Fprintf(&override, "      - ~/.config/construct-cli/container/install_user_packages.sh:/home/construct/.config/construct-cli/container/install_user_packages.sh%s\n", selinuxSuffix)
		override.WriteString("      - construct-packages:/home/linuxbrew/.linuxbrew\n")
	case "darwin":
		fmt.Fprintf(&override, "      - ${PWD}:%s%s\n", projectPath, projectSelinuxSuffix)
		fmt.Fprintf(&override, "      - ~/.config/construct-cli/home:/home/construct%s\n", selinuxSuffix)
		fmt.Fprintf(&override, "      - ~/.config/construct-cli/container/install_user_packages.sh:/home/construct/.config/construct-cli/container/install_user_packages.sh%s\n", selinuxSuffix)
		override.WriteString("      - construct-packages:/home/linuxbrew/.linuxbrew\n")
	}

	// SSH Agent Forwarding
	forwardAgent := true
	if cfg != nil {
		forwardAgent = cfg.Sandbox.ForwardSSHAgent
	}

	sshAuthSock := os.Getenv("SSH_AUTH_SOCK")
	if forwardAgent && sshAuthSock != "" {
		if runtime.GOOS == "linux" {
			// Standard Linux socket mounting
			fmt.Fprintf(&override, "      - %s:/ssh-agent%s\n", sshAuthSock, selinuxSuffix)
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

	// Propagate Git Identity
	propagateGit := true
	if cfg != nil {
		propagateGit = cfg.Sandbox.PropagateGitIdentity
	}

	if propagateGit {
		if name := getGitConfig("user.name"); name != "" {
			fmt.Fprintf(&override, "      - GIT_AUTHOR_NAME=%s\n", name)
			fmt.Fprintf(&override, "      - GIT_COMMITTER_NAME=%s\n", name)
		}
		if email := getGitConfig("user.email"); email != "" {
			fmt.Fprintf(&override, "      - GIT_AUTHOR_EMAIL=%s\n", email)
			fmt.Fprintf(&override, "      - GIT_COMMITTER_EMAIL=%s\n", email)
		}
	}

	if forwardAgent && sshAuthSock != "" {
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
		override.WriteString("\nnetworks:\n  construct-net:\n    name: construct-cli\n    driver: bridge\n")
	}

	// Write override file
	overridePath := filepath.Join(configPath, "container", "docker-compose.override.yml")
	if err := os.WriteFile(overridePath, []byte(override.String()), 0644); err != nil {
		return fmt.Errorf("failed to write docker-compose.override.yml: %w", err)
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
	for _, path := range checkPaths {
		// Skip if directory doesn't exist yet (will be created with correct ownership)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			continue
		}

		// Try to create a test file to check writability
		testFile := filepath.Join(path, ".construct-write-test")
		if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
			problemPaths = append(problemPaths, path)
			continue
		}
		// Ignore cleanup errors - we're just testing writability
		//nolint:errcheck
		os.Remove(testFile)
	}

	if len(problemPaths) == 0 {
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
	attemptedOwnershipFix = true
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

func runOwnershipFix(configPath string) error {
	username := currentUserName()
	if username == "" {
		username = "root"
	}
	cmd := exec.Command("sudo", "chown", "-R", fmt.Sprintf("%s:%s", username, username), configPath)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
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

// ExecInContainer executes a command in a running container
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

	if containerRuntime == "docker" {
		if _, err := exec.LookPath("docker-compose"); err == nil {
			cmdArgs := make([]string, 0, 1+len(composeArgs)+1+len(args))
			cmdArgs = append(cmdArgs, "docker-compose")
			cmdArgs = append(cmdArgs, composeArgs...)
			cmdArgs = append(cmdArgs, subCommand)
			cmdArgs = append(cmdArgs, args...)
			return exec.Command("docker-compose", cmdArgs[1:]...), nil
		}
		cmdArgs := make([]string, 0, 2+len(composeArgs)+1+len(args))
		cmdArgs = append(cmdArgs, "docker", "compose")
		cmdArgs = append(cmdArgs, composeArgs...)
		cmdArgs = append(cmdArgs, subCommand)
		cmdArgs = append(cmdArgs, args...)
		return exec.Command("docker", cmdArgs[1:]...), nil
	}
	if containerRuntime == "podman" {
		cmdArgs := make([]string, 0, 1+len(composeArgs)+1+len(args))
		cmdArgs = append(cmdArgs, "podman-compose")
		cmdArgs = append(cmdArgs, composeArgs...)
		cmdArgs = append(cmdArgs, subCommand)
		cmdArgs = append(cmdArgs, args...)
		return exec.Command("podman-compose", cmdArgs[1:]...), nil
	}
	if containerRuntime == "container" {
		cmdArgs := make([]string, 0, 2+len(composeArgs)+1+len(args))
		cmdArgs = append(cmdArgs, "docker", "compose")
		cmdArgs = append(cmdArgs, composeArgs...)
		cmdArgs = append(cmdArgs, subCommand)
		cmdArgs = append(cmdArgs, args...)
		return exec.Command("docker", cmdArgs[1:]...), nil
	}
	return nil, fmt.Errorf("unsupported runtime: %s", containerRuntime)
}

// GetPlatformRunFlags returns platform-specific flags for the 'run' command
func GetPlatformRunFlags() []string {
	if runtime.GOOS == "linux" {
		return []string{"--add-host", "host.docker.internal:host-gateway"}
	}
	return []string{}
}

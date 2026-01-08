package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	stdruntime "runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/EstebanForge/construct-cli/internal/cerrors"
	"github.com/EstebanForge/construct-cli/internal/clipboard"
	"github.com/EstebanForge/construct-cli/internal/config"
	"github.com/EstebanForge/construct-cli/internal/constants"
	"github.com/EstebanForge/construct-cli/internal/env"
	"github.com/EstebanForge/construct-cli/internal/network"
	"github.com/EstebanForge/construct-cli/internal/runtime"
	"github.com/EstebanForge/construct-cli/internal/templates"
	"github.com/EstebanForge/construct-cli/internal/ui"
)

const defaultLoginForwardPorts = "1455,8085"
const loginForwardListenOffset = 10000
const loginBridgeFlagFile = ".login_bridge"

// RunWithArgs executes an agent inside the container with optional network override.
func RunWithArgs(args []string, networkFlag string) {
	cfg, _, err := config.Load()
	if err != nil {
		ui.LogError(err)
		os.Exit(1)
	}

	// Override network mode if flag is provided
	if networkFlag != "" {
		if err := network.ValidateMode(networkFlag); err != nil {
			fmt.Fprintf(os.Stderr, "Error: Invalid network mode '%s': %v\n\n", networkFlag, err)
			os.Exit(1)
		}

		// Show current network mode in verbose output
		if ui.CurrentLogLevel >= ui.LogLevelInfo {
			fmt.Printf("Network mode: %s (CLI flag override)\n", networkFlag)
		}

		cfg.Network.Mode = networkFlag
	} else if ui.CurrentLogLevel >= ui.LogLevelInfo {
		fmt.Printf("Network mode: %s (from config)\n", cfg.Network.Mode)
	}

	containerRuntime := runtime.DetectRuntime(cfg.Runtime.Engine)
	configPath := config.GetConfigDir()

	// Prepare runtime environment (network, overrides)
	if err := runtime.Prepare(cfg, containerRuntime, configPath); err != nil {
		ui.LogError(&cerrors.ConstructError{
			Category:   cerrors.ErrorCategoryRuntime,
			Operation:  "prepare runtime environment",
			Runtime:    containerRuntime,
			Err:        err,
			Suggestion: "Run 'construct doctor' to diagnose",
		})
		os.Exit(1)
	}

	// Ensure setup is complete before running interactive shell
	if err := ensureSetupComplete(cfg, containerRuntime, configPath); err != nil {
		// Error already logged by ensureSetupComplete/runSetup
		os.Exit(1)
	}

	// Continue with container execution (no provider env vars)
	runWithProviderEnv(args, cfg, containerRuntime, configPath, nil)
}

// RunWithProvider executes Claude with a configured provider alias.
func RunWithProvider(args []string, networkFlag, providerName string) {
	cfg, _, err := config.Load()
	if err != nil {
		ui.LogError(err)
		os.Exit(1)
	}

	// Validate provider exists
	providerEnvMap, exists := cfg.Claude.Providers[providerName]
	if !exists {
		fmt.Fprintf(os.Stderr, "Error: Claude provider '%s' not found in config\n\n", providerName)
		fmt.Fprintf(os.Stderr, "Available providers:\n")
		if len(cfg.Claude.Providers) == 0 {
			fmt.Fprintf(os.Stderr, "  (none configured)\n\n")
		} else {
			for name := range cfg.Claude.Providers {
				fmt.Fprintf(os.Stderr, "  - %s\n", name)
			}
			fmt.Fprintf(os.Stderr, "\n")
		}
		fmt.Fprintf(os.Stderr, "Configure providers in ~/.config/construct-cli/config.toml\n")
		fmt.Fprintf(os.Stderr, "Example:\n")
		fmt.Fprintf(os.Stderr, "  [claude.cc.%s]\n", providerName)
		fmt.Fprintf(os.Stderr, "  ANTHROPIC_BASE_URL = \"https://api.example.com\"\n")
		fmt.Fprintf(os.Stderr, "  ANTHROPIC_AUTH_TOKEN = \"${YOUR_API_KEY}\"\n")
		os.Exit(1)
	}

	// Expand environment variables and inject
	providerEnv := env.ExpandProviderEnv(providerEnvMap)

	if ui.CurrentLogLevel >= ui.LogLevelInfo {
		fmt.Printf("Using Claude provider: %s\n", providerName)
	}

	// Override network mode if flag is provided
	if networkFlag != "" {
		if err := network.ValidateMode(networkFlag); err != nil {
			fmt.Fprintf(os.Stderr, "Error: Invalid network mode '%s': %v\n\n", networkFlag, err)
			os.Exit(1)
		}

		if ui.CurrentLogLevel >= ui.LogLevelInfo {
			fmt.Printf("Network mode: %s (CLI flag override)\n", networkFlag)
		}

		cfg.Network.Mode = networkFlag
	} else if ui.CurrentLogLevel >= ui.LogLevelInfo {
		fmt.Printf("Network mode: %s (from config)\n", cfg.Network.Mode)
	}

	containerRuntime := runtime.DetectRuntime(cfg.Runtime.Engine)
	configPath := config.GetConfigDir()

	// Prepare runtime environment (network, overrides)
	if err := runtime.Prepare(cfg, containerRuntime, configPath); err != nil {
		ui.LogError(&cerrors.ConstructError{
			Category:   cerrors.ErrorCategoryRuntime,
			Operation:  "prepare runtime environment",
			Runtime:    containerRuntime,
			Err:        err,
			Suggestion: "Run 'construct doctor' to diagnose",
		})
		os.Exit(1)
	}

	// Ensure setup is complete before running interactive shell
	if err := ensureSetupComplete(cfg, containerRuntime, configPath); err != nil {
		// Error already logged
		os.Exit(1)
	}

	// Continue with container execution, passing provider env vars
	runWithProviderEnv(args, cfg, containerRuntime, configPath, providerEnv)
}

func ensureSetupComplete(cfg *config.Config, containerRuntime, configPath string) error {
	// Paths
	containerDir := filepath.Join(configPath, "container")
	homeDir := filepath.Join(configPath, "home")
	userScriptPath := filepath.Join(containerDir, "install_user_packages.sh")
	hashFile := filepath.Join(homeDir, ".local", ".entrypoint_hash")
	forceFile := filepath.Join(homeDir, ".local", ".force_entrypoint")

	if reason, ok := config.GetRebuildRequired(); ok {
		if reason != "" {
			fmt.Printf("⚠️  Rebuild required: %s\n", reason)
		} else {
			fmt.Println("⚠️  Rebuild required.")
		}
		fmt.Println("Run 'construct sys rebuild' to refresh the container image.")
		return fmt.Errorf("rebuild required")
	}

	// Calculate expected hash
	// Use embedded template as the source of truth for entrypoint
	h := sha256.Sum256([]byte(templates.Entrypoint))
	entrypointHash := hex.EncodeToString(h[:])

	expectedHash := entrypointHash
	if _, err := os.Stat(userScriptPath); err == nil {
		scriptHash, err := getFileHash(userScriptPath)
		if err == nil {
			expectedHash = fmt.Sprintf("%s-%s", expectedHash, scriptHash)
		}
	}

	if imageHash, err := getImageEntrypointHash(containerRuntime, configPath); err == nil && imageHash != entrypointHash {
		if err := config.SetRebuildRequired("container image entrypoint is stale"); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Failed to mark rebuild required: %v\n", err)
		}
		fmt.Println("⚠️  Container image entrypoint is stale.")
		fmt.Println("Run 'construct sys rebuild' to refresh the container image.")
		return fmt.Errorf("rebuild required")
	}

	// Check for force flag
	if _, err := os.Stat(forceFile); err == nil {
		if ui.CurrentLogLevel >= ui.LogLevelDebug {
			fmt.Println("Debug: Force setup flag detected")
		}
		return runSetup(cfg, containerRuntime, configPath)
	}

	// Check existing hash
	if currentHash, err := os.ReadFile(hashFile); err == nil {
		actualHash := strings.TrimSpace(string(currentHash))
		if actualHash == expectedHash {
			if ui.CurrentLogLevel >= ui.LogLevelDebug {
				fmt.Printf("Debug: Setup hash matches (%s)\n", actualHash)
			}
			return nil // Already up to date
		}
		if ui.CurrentLogLevel >= ui.LogLevelDebug {
			fmt.Printf("Debug: Setup hash mismatch:\n  Expected: %s\n  Actual:   %s\n", expectedHash, actualHash)
		}
	} else if ui.CurrentLogLevel >= ui.LogLevelDebug {
		fmt.Printf("Debug: Could not read hash file: %v\n", err)
	}

	// Hash mismatch or missing - run setup
	return runSetup(cfg, containerRuntime, configPath)
}

func getFileHash(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:]), nil
}

func getImageEntrypointHash(containerRuntime, configPath string) (string, error) {
	checkCmdArgs := runtime.GetCheckImageCommand(containerRuntime)
	checkCmd := exec.Command(checkCmdArgs[0], checkCmdArgs[1:]...)
	checkCmd.Dir = config.GetContainerDir()
	if err := checkCmd.Run(); err != nil {
		return "", err
	}

	runtimeCmd := containerRuntime
	if runtimeCmd == "container" {
		runtimeCmd = "docker"
	}

	imageName := constants.ImageName + ":latest"
	cmd := exec.Command(runtimeCmd, "run", "--rm", "--entrypoint", "sha256sum", imageName, "/usr/local/bin/entrypoint.sh")
	cmd.Dir = configPath
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(output))
	if len(fields) < 1 {
		return "", fmt.Errorf("unexpected sha256sum output")
	}
	return fields[0], nil
}

func runSetup(cfg *config.Config, containerRuntime, configPath string) error {
	// Create log file for setup output
	logFile, err := config.CreateLogFile("setup")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to create log file: %v\n", err)
	}
	if logFile != nil {
		defer func() {
			if err := logFile.Close(); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: Failed to close log file: %v\n", err)
			}
		}()
	}

	// Run 'true' inside the container.
	// The entrypoint.sh will run, detect the hash change, perform setup, update hash, and then exec 'true'.
	// Use -T to ensure non-interactive run (no TTY allocation)

	// We need to bypass runtime.BuildComposeCommand because it prepends "run" and doesn't support -T easily if we use its RunFlags.
	// Actually BuildComposeCommand takes subCommand as argument.

	runArgs := []string{"--rm", "-T", "construct-box", "true"}
	runArgs = append(runtime.GetPlatformRunFlags(), runArgs...)

	cmd, err := runtime.BuildComposeCommand(containerRuntime, configPath, "run", runArgs)
	if err != nil {
		return fmt.Errorf("failed to build setup command: %w", err)
	}

	cmd.Dir = config.GetContainerDir()
	// Ensure we pass necessary env vars so entrypoint behaves correctly
	// We use minimal env here as we are just running setup
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	osEnv := os.Environ()
	osEnv = append(osEnv, "PWD="+cwd)
	// Inject network env if needed (though setup usually needs network)
	osEnv = network.InjectEnv(osEnv, cfg)

	cmd.Env = osEnv

	// Use helper to run with spinner
	if err := ui.RunCommandWithSpinner(cmd, "Configuring environment and installing packages...", logFile); err != nil {
		fmt.Fprintf(os.Stderr, "\n❌ Setup command failed: %v\n", err)
		fmt.Fprintf(os.Stderr, "Peeking logs might help diagnose the issue.\n")
		return fmt.Errorf("environment setup failed: %w", err)
	}
	return nil
}

func runWithProviderEnv(args []string, cfg *config.Config, containerRuntime, configPath string, providerEnv []string) {
	// Check for container collision
	containerName := "construct-cli"
	state := runtime.GetContainerState(containerRuntime, containerName)

	switch state {
	case runtime.ContainerStateRunning:
		fmt.Printf("⚠️  Container '%s' is already running.\n\n", containerName)

		// Use Gum for beautiful option selection
		if ui.GumAvailable() {
			cmd := exec.Command("gum", "choose",
				"Attach to existing session (recommended)",
				"Stop and create new session",
				"Abort")
			output, err := cmd.Output()
			if err != nil {
				fmt.Println("Operation canceled.")
				os.Exit(0)
			}

			selected := strings.TrimSpace(string(output))
			switch {
			case strings.HasPrefix(selected, "Attach"):
				// Execute in existing container
				cmdArgs := args
				result, err := runtime.ExecInContainer(containerRuntime, containerName, cmdArgs)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Error: Failed to attach: %v\n", err)
					os.Exit(1)
				}
				fmt.Print(result)
				return
			case strings.HasPrefix(selected, "Stop"):
				if err := runtime.StopContainer(containerRuntime, containerName); err != nil {
					fmt.Fprintf(os.Stderr, "Error: Failed to stop container: %v\n", err)
					os.Exit(1)
				}
				if err := runtime.CleanupExitedContainer(containerRuntime, containerName); err != nil {
					fmt.Fprintf(os.Stderr, "Error: Failed to remove container: %v\n", err)
					os.Exit(1)
				}
				// Continue to new container
			case strings.HasPrefix(selected, "Abort"):
				os.Exit(0)
			}
		} else {
			// Fallback to basic prompt if Gum not available
			fmt.Println("1. Attach to existing session (recommended)")
			fmt.Println("2. Stop and create new session")
			fmt.Println("3. Abort")
			fmt.Print("Choice [1-3]: ")
			var basicChoice string
			if _, err := fmt.Scanln(&basicChoice); err != nil {
				fmt.Fprintln(os.Stderr, "Error: Failed to read choice")
				os.Exit(1)
			}

			switch basicChoice {
			case "1":
				cmdArgs := args
				result, err := runtime.ExecInContainer(containerRuntime, containerName, cmdArgs)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Error: Failed to attach: %v\n", err)
					os.Exit(1)
				}
				fmt.Print(result)
				return
			case "2":
				if err := runtime.StopContainer(containerRuntime, containerName); err != nil {
					fmt.Fprintf(os.Stderr, "Error: Failed to stop container: %v\n", err)
					os.Exit(1)
				}
				if err := runtime.CleanupExitedContainer(containerRuntime, containerName); err != nil {
					fmt.Fprintf(os.Stderr, "Error: Failed to remove container: %v\n", err)
					os.Exit(1)
				}
			case "3":
				os.Exit(0)
			default:
				fmt.Println("Invalid choice. Aborting.")
				os.Exit(1)
			}
		}

	case runtime.ContainerStateExited:
		fmt.Printf("🧹 Removing old stopped container '%s'...\n", containerName)
		if err := runtime.CleanupExitedContainer(containerRuntime, containerName); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Failed to cleanup container: %v\n", err)
		}
	}

	// Check if image exists
	checkCmdArgs := runtime.GetCheckImageCommand(containerRuntime)
	checkCmd := exec.Command(checkCmdArgs[0], checkCmdArgs[1:]...)
	checkCmd.Dir = config.GetContainerDir()
	if err := checkCmd.Run(); err != nil {
		fmt.Println("Construct image not found. Building...")
		runtime.BuildImage(cfg)
		fmt.Println()
	}

	// Get current working directory
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to get current directory: %v\n", err)
		os.Exit(1)
	}

	// Prepare environment variables
	osEnv := os.Environ()
	osEnv = append(osEnv, "PWD="+cwd)

	// Ensure comprehensive PATH for container (fixes agent subprocess PATH issues)
	// The container user's home is always /home/construct
	env.EnsureConstructPath(&osEnv, "/home/construct")

	// Start Clipboard Server
	clipboardHost := ""
	if cfg != nil {
		clipboardHost = cfg.Sandbox.ClipboardHost
	}
	cbServer, err := clipboard.StartServer(clipboardHost)
	if err != nil {
		if ui.CurrentLogLevel >= ui.LogLevelInfo {
			fmt.Printf("Warning: Failed to start clipboard server: %v\n", err)
		}
	} else {
		if ui.CurrentLogLevel >= ui.LogLevelDebug {
			fmt.Printf("Clipboard server running at %s\n", cbServer.URL)
		}
		osEnv = append(osEnv, "CONSTRUCT_CLIPBOARD_URL="+cbServer.URL)
		osEnv = append(osEnv, "CONSTRUCT_CLIPBOARD_TOKEN="+cbServer.Token)
	}

	// Network configuration
	osEnv = network.InjectEnv(osEnv, cfg)

	// SSH Agent Bridge (for macOS reliability)
	if stdruntime.GOOS == "darwin" && cfg.Sandbox.ForwardSSHAgent && os.Getenv("SSH_AUTH_SOCK") != "" {
		bridge, err := StartSSHBridge()
		if err != nil {
			if ui.CurrentLogLevel >= ui.LogLevelInfo {
				fmt.Printf("Warning: Failed to start SSH bridge: %v\n", err)
			}
		} else {
			defer bridge.Stop()
			if ui.CurrentLogLevel >= ui.LogLevelDebug {
				fmt.Printf("SSH bridge running on port %d\n", bridge.Port)
			}
			osEnv = append(osEnv, fmt.Sprintf("CONSTRUCT_SSH_BRIDGE_PORT=%d", bridge.Port))
		}
	}

	// Provider configuration (if provided)
	if len(providerEnv) > 0 {
		// Reset any existing Claude environment variables to avoid conflicts
		osEnv = env.ResetClaudeEnv(osEnv)

		// Inject provider-specific environment variables
		osEnv = append(osEnv, providerEnv...)

		if ui.CurrentLogLevel >= ui.LogLevelInfo {
			fmt.Printf("Provider environment variables injected: %d\n", len(providerEnv))
		}
		if ui.CurrentLogLevel >= ui.LogLevelDebug {
			for _, e := range providerEnv {
				parts := strings.SplitN(e, "=", 2)
				if len(parts) == 2 {
					fmt.Printf("  %s=%s\n", parts[0], env.MaskSensitiveValue(parts[0], parts[1]))
				}
			}
		}
	}

	// composeArgs := runtime.GetComposeFileArgs(configPath) // Removed as unused

	if len(args) == 0 {
		fmt.Println("Entering Construct interactive shell...")
	} else {
		fmt.Printf("Running in Construct: %v\n", args)
	}

	var cmd *exec.Cmd
	loginForward, loginPorts := shouldEnableLoginForward(args)

	// Construct common arguments for 'run' command
	runFlags := []string{"--rm"}

	if loginForward {
		for _, port := range loginPorts {
			listenPort := port + loginForwardListenOffset
			runFlags = append(runFlags, "-p", fmt.Sprintf("127.0.0.1:%d:%d", port, listenPort))
		}
	}

	// Add platform-specific flags (e.g. --add-host on Linux)
	runFlags = append(runFlags, runtime.GetPlatformRunFlags()...)

	// Inject clipboard env vars
	if cbServer != nil {
		runFlags = append(runFlags, "-e", "CONSTRUCT_CLIPBOARD_URL="+cbServer.URL)
		runFlags = append(runFlags, "-e", "CONSTRUCT_CLIPBOARD_TOKEN="+cbServer.Token)
		runFlags = append(runFlags, "-e", "CONSTRUCT_FILE_PASTE_AGENTS="+constants.FileBasedPasteAgents)
	}

	// Inject agent name for clipboard behavior tuning.
	if len(args) > 0 {
		runFlags = append(runFlags, "-e", "CONSTRUCT_AGENT_NAME="+args[0])

		// For codex: Set WSL env vars to trigger clipboard fallback
		// Codex will think it's in WSL and use our fake powershell.exe
		if args[0] == "codex" {
			runFlags = append(runFlags, "-e", "WSL_DISTRO_NAME=Ubuntu")
			runFlags = append(runFlags, "-e", "WSL_INTEROP=/run/WSL/8_interop")
			// Unset DISPLAY so arboard fails and triggers WSL fallback
			runFlags = append(runFlags, "-e", "DISPLAY=")

			// Pass CONSTRUCT_DEBUG to codex container
			if os.Getenv("CONSTRUCT_DEBUG") == "1" {
				runFlags = append(runFlags, "-e", "CONSTRUCT_DEBUG=1")
			}
		}
	}

	// Inject SSH bridge port
	for _, e := range osEnv {
		if strings.HasPrefix(e, "CONSTRUCT_SSH_BRIDGE_PORT=") {
			runFlags = append(runFlags, "-e", e)
			break
		}
	}

	if loginForward {
		runFlags = append(runFlags, "-e", "CONSTRUCT_LOGIN_FORWARD=1")
		runFlags = append(runFlags, "-e", "CONSTRUCT_LOGIN_FORWARD_PORTS="+formatPorts(loginPorts))
		runFlags = append(runFlags, "-e", fmt.Sprintf("CONSTRUCT_LOGIN_FORWARD_LISTEN_OFFSET=%d", loginForwardListenOffset))
	}

	// Inject provider env vars
	for _, envVar := range providerEnv {
		runFlags = append(runFlags, "-e", envVar)
	}

	// Inject PATH explicitly to ensure it's available in container and all subprocesses
	for _, e := range osEnv {
		if strings.HasPrefix(e, "PATH=") {
			runFlags = append(runFlags, "-e", e)
			if ui.CurrentLogLevel >= ui.LogLevelDebug {
				fmt.Printf("Debug: Injecting PATH to container: %s\n", e[:100]+"...")
			}
			break
		}
	}

	// Add image/service name and arguments
	runFlags = append(runFlags, "construct-box")
	runFlags = append(runFlags, args...)

	// Build the command using runtime abstraction
	cmd, err = runtime.BuildComposeCommand(containerRuntime, configPath, "run", runFlags)
	if err != nil {
		ui.LogError(&cerrors.ConstructError{
			Category:  cerrors.ErrorCategoryRuntime,
			Operation: "build command",
			Runtime:   containerRuntime,
			Err:       err,
		})
		os.Exit(1)
	}

	cmd.Dir = config.GetContainerDir()
	cmd.Env = osEnv
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		ui.LogError(&cerrors.ConstructError{
			Category:   cerrors.ErrorCategoryContainer,
			Operation:  "execute agent in container",
			Command:    fmt.Sprintf("%s shell", containerRuntime),
			Runtime:    containerRuntime,
			Path:       cwd,
			Err:        err,
			Suggestion: "Run 'construct doctor' or check image with 'construct init'",
		})
		os.Exit(1)
	}
}

func shouldEnableLoginForward(args []string) (bool, []int) {
	if ports, ok := readLoginBridgePorts(); ok {
		return true, ports
	}
	for _, arg := range args {
		if arg == "login" || arg == "auth" {
			ports := parsePorts(defaultLoginForwardPorts)
			if len(ports) == 0 {
				return true, []int{1455}
			}
			return true, ports
		}
	}
	return false, nil
}

func readLoginBridgePorts() ([]int, bool) {
	path := filepath.Join(config.GetConfigDir(), loginBridgeFlagFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	raw := strings.TrimSpace(string(data))
	ports := parsePorts(raw)
	if len(ports) == 0 {
		ports = parsePorts(defaultLoginForwardPorts)
	}
	if len(ports) == 0 {
		return []int{1455}, true
	}
	return ports, true
}

func parsePorts(raw string) []int {
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n'
	})
	seen := make(map[int]struct{})
	ports := make([]int, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			continue
		}
		port, err := strconv.Atoi(part)
		if err != nil || port <= 0 {
			continue
		}
		if _, exists := seen[port]; exists {
			continue
		}
		seen[port] = struct{}{}
		ports = append(ports, port)
	}
	sort.Ints(ports)
	return ports
}

func formatPorts(ports []int) string {
	if len(ports) == 0 {
		return ""
	}
	parts := make([]string, 0, len(ports))
	for _, port := range ports {
		parts = append(parts, strconv.Itoa(port))
	}
	return strings.Join(parts, ",")
}

package agent

import (
	"fmt"
	"os"
	"os/exec"
	stdruntime "runtime"
	"strings"

	"github.com/EstebanForge/construct-cli/internal/clipboard"
	"github.com/EstebanForge/construct-cli/internal/config"
	"github.com/EstebanForge/construct-cli/internal/env"
	"github.com/EstebanForge/construct-cli/internal/errors"
	"github.com/EstebanForge/construct-cli/internal/network"
	"github.com/EstebanForge/construct-cli/internal/runtime"
	"github.com/EstebanForge/construct-cli/internal/ui"
)

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
		ui.LogError(&errors.ConstructError{
			Category:   errors.ErrorCategoryRuntime,
			Operation:  "prepare runtime environment",
			Runtime:    containerRuntime,
			Err:        err,
			Suggestion: "Run 'construct doctor' to diagnose",
		})
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
		ui.LogError(&errors.ConstructError{
			Category:   errors.ErrorCategoryRuntime,
			Operation:  "prepare runtime environment",
			Runtime:    containerRuntime,
			Err:        err,
			Suggestion: "Run 'construct doctor' to diagnose",
		})
		os.Exit(1)
	}

	// Continue with container execution, passing provider env vars
	runWithProviderEnv(args, cfg, containerRuntime, configPath, providerEnv)
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

	// Start Clipboard Server
	cbServer, err := clipboard.StartServer()
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

	composeArgs := runtime.GetComposeFileArgs(configPath)

	fmt.Printf("Running in Construct: %v\n", args)

	var cmd *exec.Cmd

	// Build the run command based on runtime
	if containerRuntime == "docker" {
		if _, err := exec.LookPath("docker-compose"); err == nil {
			runArgs := append(composeArgs, "run", "--rm")
			// Add host.docker.internal for Linux
			if stdruntime.GOOS == "linux" {
				runArgs = append(runArgs, "--add-host", "host.docker.internal:host-gateway")
			}
			// Inject clipboard env vars
			if cbServer != nil {
				runArgs = append(runArgs, "-e", "CONSTRUCT_CLIPBOARD_URL="+cbServer.URL)
				runArgs = append(runArgs, "-e", "CONSTRUCT_CLIPBOARD_TOKEN="+cbServer.Token)
			}
			// Inject provider env vars
			for _, envVar := range providerEnv {
				runArgs = append(runArgs, "-e", envVar)
			}
			runArgs = append(runArgs, "construct-box")
			runArgs = append(runArgs, args...)
			cmd = exec.Command("docker-compose", runArgs...)
		} else {
			runArgs := []string{"compose"}
			runArgs = append(runArgs, composeArgs...)
			runArgs = append(runArgs, "run", "--rm")
			// Add host.docker.internal for Linux
			if stdruntime.GOOS == "linux" {
				runArgs = append(runArgs, "--add-host", "host.docker.internal:host-gateway")
			}
			// Inject clipboard env vars
			if cbServer != nil {
				runArgs = append(runArgs, "-e", "CONSTRUCT_CLIPBOARD_URL="+cbServer.URL)
				runArgs = append(runArgs, "-e", "CONSTRUCT_CLIPBOARD_TOKEN="+cbServer.Token)
			}
			// Inject provider env vars
			for _, envVar := range providerEnv {
				runArgs = append(runArgs, "-e", envVar)
			}
			runArgs = append(runArgs, "construct-box")
			runArgs = append(runArgs, args...)
			cmd = exec.Command("docker", runArgs...)
		}
	} else if containerRuntime == "podman" {
		runArgs := append(composeArgs, "run", "--rm")
		// Add host.docker.internal for Linux
		if stdruntime.GOOS == "linux" {
			runArgs = append(runArgs, "--add-host", "host.docker.internal:host-gateway")
		}
		// Inject clipboard env vars
		if cbServer != nil {
			runArgs = append(runArgs, "-e", "CONSTRUCT_CLIPBOARD_URL="+cbServer.URL)
			runArgs = append(runArgs, "-e", "CONSTRUCT_CLIPBOARD_TOKEN="+cbServer.Token)
		}
		// Inject provider env vars
		for _, envVar := range providerEnv {
			runArgs = append(runArgs, "-e", envVar)
		}
		runArgs = append(runArgs, "construct-box")
		runArgs = append(runArgs, args...)
		cmd = exec.Command("podman-compose", runArgs...)
	} else if containerRuntime == "container" {
		runArgs := []string{"compose"}
		runArgs = append(runArgs, composeArgs...)
		runArgs = append(runArgs, "run", "--rm")
		// Add host.docker.internal for Linux
		if stdruntime.GOOS == "linux" {
			runArgs = append(runArgs, "--add-host", "host.docker.internal:host-gateway")
		}
		// Inject clipboard env vars
		if cbServer != nil {
			runArgs = append(runArgs, "-e", "CONSTRUCT_CLIPBOARD_URL="+cbServer.URL)
			runArgs = append(runArgs, "-e", "CONSTRUCT_CLIPBOARD_TOKEN="+cbServer.Token)
		}
		// Inject provider env vars
		for _, envVar := range providerEnv {
			runArgs = append(runArgs, "-e", envVar)
		}
		runArgs = append(runArgs, "construct-box")
		runArgs = append(runArgs, args...)
		cmd = exec.Command("docker", runArgs...)
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
		ui.LogError(&errors.ConstructError{
			Category:   errors.ErrorCategoryContainer,
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

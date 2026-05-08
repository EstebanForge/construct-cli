package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/EstebanForge/construct-cli/internal/cerrors"
	"github.com/EstebanForge/construct-cli/internal/config"
	"github.com/EstebanForge/construct-cli/internal/constants"
	"github.com/EstebanForge/construct-cli/internal/env"
	"github.com/EstebanForge/construct-cli/internal/network"
	"github.com/EstebanForge/construct-cli/internal/runtime"
	"github.com/EstebanForge/construct-cli/internal/templates"
	"github.com/EstebanForge/construct-cli/internal/ui"
)

var getImageEntrypointHashFn = getImageEntrypointHash

// RunWithArgs executes an agent inside the container with optional network override.
func RunWithArgs(args []string, networkFlag string) {
	cfg, _, err := config.Load()
	if err != nil {
		ui.LogError(err)
		os.Exit(1)
	}

	applyNetworkFlag(cfg, networkFlag)

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
		os.Exit(1)
	}

	if shouldPromptGooseConfigure(args, configPath) {
		fmt.Println("Goose CLI needs initial setup.")
		fmt.Println("Run: ct goose configure")
		os.Exit(1)
	}

	// Continue with container execution (no provider env vars)
	exitCode := runWithProviderEnv(args, cfg, containerRuntime, configPath, nil)

	if isGooseConfigure(args) {
		if err := markGooseConfigured(configPath); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to save Goose configure state: %v\n", err)
		}
	}

	os.Exit(exitCode)
}

func applyNetworkFlag(cfg *config.Config, networkFlag string) {
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

	applyNetworkFlag(cfg, networkFlag)

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
		os.Exit(1)
	}

	// Continue with container execution, passing provider env vars
	exitCode := runWithProviderEnv(args, cfg, containerRuntime, configPath, providerEnv)
	os.Exit(exitCode)
}

func ensureSetupComplete(cfg *config.Config, containerRuntime, configPath string) error {
	// Paths
	containerDir := filepath.Join(configPath, "container")
	homeDir := filepath.Join(configPath, "home")
	userScriptPath := filepath.Join(containerDir, "install_user_packages.sh")
	hashFile := filepath.Join(homeDir, ".local", ".entrypoint_hash")
	forceFile := filepath.Join(homeDir, ".local", ".force_entrypoint")

	// Calculate expected hash
	// Use embedded template as the source of truth for entrypoint
	h := sha256.Sum256([]byte(templates.Entrypoint))
	entrypointHash := hex.EncodeToString(h[:])

	imageHash, imageHashErr := getImageEntrypointHashFn(containerRuntime, configPath)

	if reason, ok := config.GetRebuildRequired(); ok {
		if imageHashErr == nil && imageHash == entrypointHash {
			if err := config.ClearRebuildRequired(); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: Failed to clear stale rebuild marker: %v\n", err)
			} else if ui.CurrentLogLevel >= ui.LogLevelDebug {
				fmt.Println("Debug: Cleared stale rebuild marker; image entrypoint hash is current")
			}
		} else {
			if reason != "" {
				fmt.Printf("⚠️  Rebuild required: %s\n", reason)
			} else {
				fmt.Println("⚠️  Rebuild required.")
			}
			fmt.Println("Run 'construct sys rebuild' to refresh the container image.")
			return fmt.Errorf("rebuild required")
		}
	}

	expectedHash := entrypointHash
	if _, err := os.Stat(userScriptPath); err == nil {
		scriptHash, err := getFileHash(userScriptPath)
		if err == nil {
			expectedHash = fmt.Sprintf("%s-%s", expectedHash, scriptHash)
		}
	}

	if imageHashErr == nil && imageHash != entrypointHash {
		if err := config.SetRebuildRequired("container image entrypoint is stale"); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Failed to mark rebuild required: %v\n", err)
		}
		fmt.Println("⚠️  Container image entrypoint is stale.")
		fmt.Println("Run 'construct sys rebuild' to refresh the container image.")
		return fmt.Errorf("rebuild required")
	}

	if _, err := os.Stat(forceFile); err == nil {
		if ui.CurrentLogLevel >= ui.LogLevelDebug {
			fmt.Println("Debug: Force setup flag detected")
		}
		return runSetup(cfg, containerRuntime, configPath)
	}

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

	runArgs := []string{"--rm", "-T", "construct-box", "true"}

	cmd, err := runtime.BuildComposeCommand(containerRuntime, configPath, "run", runArgs)
	if err != nil {
		return fmt.Errorf("failed to build setup command: %w", err)
	}

	cmd.Dir = config.GetContainerDir()
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	osEnv := os.Environ()
	osEnv = append(osEnv, "PWD="+cwd)
	osEnv = runtime.AppendProjectPathEnv(osEnv)
	osEnv = runtime.AppendRuntimeIdentityEnv(osEnv, containerRuntime)

	env.EnsureConstructPath(&osEnv, "/home/construct")
	// Note: engine.go handles construct path internally now, but for runSetup we still need it.
	constructPath := env.BuildConstructPath("/home/construct")
	env.SetEnvVar(&osEnv, "PATH", constructPath)
	env.SetEnvVar(&osEnv, "CONSTRUCT_PATH", constructPath)

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

func runWithProviderEnv(args []string, cfg *config.Config, containerRuntime, configPath string, providerEnv []string) int {
	engine := NewRuntimeEngine(cfg, args, containerRuntime, configPath, providerEnv)
	defer engine.Teardown()

	if err := engine.Prepare(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to prepare runtime: %v\n", err)
		return 1
	}

	exitCode, err := engine.Execute()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	return exitCode
}

func shouldPromptGooseConfigure(args []string, configPath string) bool {
	if !isGooseCommand(args) || isGooseConfigure(args) {
		return false
	}
	return !isGooseConfigured(configPath)
}

func isGooseCommand(args []string) bool {
	return len(args) > 0 && strings.EqualFold(args[0], "goose")
}

func isGooseConfigure(args []string) bool {
	if !isGooseCommand(args) {
		return false
	}
	for _, arg := range args[1:] {
		if arg == "configure" {
			return true
		}
	}
	return false
}

func gooseConfiguredMarkerPath(configPath string) string {
	return filepath.Join(configPath, "home", ".config", "goose", ".construct_configured")
}

func isGooseConfigured(configPath string) bool {
	if _, err := os.Stat(gooseConfiguredMarkerPath(configPath)); err == nil {
		return true
	}
	return false
}

func markGooseConfigured(configPath string) error {
	markerPath := gooseConfiguredMarkerPath(configPath)
	if err := os.MkdirAll(filepath.Dir(markerPath), 0755); err != nil {
		return err
	}
	return os.WriteFile(markerPath, []byte("configured\n"), 0644)
}

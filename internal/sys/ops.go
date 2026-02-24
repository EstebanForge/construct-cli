package sys

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	cerrors "github.com/EstebanForge/construct-cli/internal/cerrors"
	"github.com/EstebanForge/construct-cli/internal/config"
	"github.com/EstebanForge/construct-cli/internal/runtime"
	"github.com/EstebanForge/construct-cli/internal/ui"
)

// UpdateAgents runs the update-all script inside the container.
func UpdateAgents(cfg *config.Config) {
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

	// Create log file for update output
	logFile, err := config.CreateLogFile("update")
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
			fmt.Printf("%sUpdate log: %s%s\n", ui.ColorGrey, logPath, ui.ColorReset)
		} else {
			fmt.Printf("Update log: %s\n", logPath)
		}
	}

	fmt.Println()

	// Get current working directory
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to get current directory: %v\n", err)
		os.Exit(1)
	}

	// Prepare environment variables
	env := os.Environ()
	env = append(env, "PWD="+cwd)
	env = runtime.AppendProjectPathEnv(env)
	env = runtime.AppendRuntimeIdentityEnv(env, containerRuntime)

	var cmd *exec.Cmd

	// Construct run args for update
	updateScript := "/home/construct/.config/construct-cli/container/update-all.sh"
	hostUpdateScript := filepath.Join(config.GetConfigDir(), "container", "update-all.sh")
	if _, err := os.Stat(hostUpdateScript); err != nil {
		updateScript = "/usr/local/bin/update-all.sh"
		if ui.GumAvailable() {
			fmt.Printf("%sWarning: Host update script missing, using container fallback. Run 'construct sys refresh' to sync templates.%s\n", ui.ColorGrey, ui.ColorReset)
		} else {
			fmt.Println("Warning: Host update script missing, using container fallback. Run 'construct sys refresh' to sync templates.")
		}
	}

	clipboardPatchValue := "1"
	if cfg != nil && !cfg.Agents.ClipboardImagePatch {
		clipboardPatchValue = "0"
	}
	runFlags := []string{
		"--rm",
		"-T",
		"--entrypoint", updateScript,
		"-e", "CONSTRUCT_CLIPBOARD_IMAGE_PATCH=" + clipboardPatchValue,
		"construct-box",
	}

	// Build command
	cmd, err = runtime.BuildComposeCommand(containerRuntime, configPath, "run", runFlags)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to build update command: %v\n", err)
		os.Exit(1)
	}

	cmd.Dir = config.GetContainerDir()
	cmd.Env = env

	// Use helper to run with spinner
	if err := ui.RunCommandWithSpinner(cmd, "Updating all agents, packages & tools...", logFile); err != nil {
		os.Exit(1)
	}

	fmt.Println()
	if ui.GumAvailable() {
		ui.GumSuccess("All agents updated successfully!")
	} else {
		fmt.Println("✅ All agents updated successfully!")
	}
}

// ResetVolumes deletes persistent volumes to force agent reinstall on next run.
func ResetVolumes(cfg *config.Config) {
	containerRuntime := runtime.DetectRuntime(cfg.Runtime.Engine)

	if ui.GumAvailable() {
		// Use Gum for warning and confirmation
		cmd := ui.GetGumCommand("style", "--foreground", "214", "--border", "rounded", "--padding", "1 2",
			"⚠️  This will delete all installed agents from persistent volumes.",
			"They will be reinstalled on next run (takes 5-10 minutes).")
		cmd.Stdout = os.Stdout
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to render warning: %v\n", err)
		}

		fmt.Println()
		if !ui.GumConfirm("Continue with reset?") {
			fmt.Println("Reset canceled.")
			return
		}
	} else {
		// Fallback text prompt
		fmt.Println("⚠️  This will delete all installed agents from persistent volumes.")
		fmt.Println("   They will be reinstalled on next run (takes 5-10 minutes).")
		fmt.Print("\nContinue? [y/N]: ")

		var response string
		if _, err := fmt.Scanln(&response); err != nil {
			fmt.Fprintln(os.Stderr, "Error: Failed to read response")
			os.Exit(1)
		}

		if response != "y" && response != "Y" {
			fmt.Println("Reset canceled.")
			return
		}
	}

	if ui.GumAvailable() {
		cmd := ui.GetGumCommand("style", "--foreground", "242", "Deleting volumes...")
		cmd.Stdout = os.Stdout
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to render status: %v\n", err)
		}
	} else {
		fmt.Println("\nDeleting volumes...")
	}

	var cmd *exec.Cmd

	// Delete named volumes
	switch containerRuntime {
	case "docker":
		cmd = exec.Command("docker", "volume", "rm", "construct-agents", "construct-packages")
	case "podman":
		cmd = exec.Command("podman", "volume", "rm", "construct-agents", "construct-packages")
	case "container":
		cmd = exec.Command("docker", "volume", "rm", "construct-agents", "construct-packages")
	}

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		if ui.GumAvailable() {
			ui.GumWarning(fmt.Sprintf("Failed to delete volumes: %v", err))
			fmt.Println("Volumes may not exist yet (this is normal on first run).")
		} else {
			fmt.Fprintf(os.Stderr, "Warning: Failed to delete volumes: %v\n", err)
			fmt.Fprintf(os.Stderr, "Volumes may not exist yet (this is normal on first run).\n")
		}
	} else {
		if ui.GumAvailable() {
			ui.GumSuccess("Volumes deleted successfully!")
			cmd := ui.GetGumCommand("style", "--foreground", "242", "Agents will be reinstalled on next run.")
			cmd.Stdout = os.Stdout
			if err := cmd.Run(); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to render reinstall notice: %v\n", err)
			}
		} else {
			fmt.Println()
			fmt.Println("✅ Volumes deleted successfully!")
			fmt.Println("   Agents will be reinstalled on next run.")
		}
	}
}

// InstallPackages regenerates and runs the user-defined installation script.
func InstallPackages(cfg *config.Config) {
	containerRuntime := runtime.DetectRuntime(cfg.Runtime.Engine)
	configPath := config.GetConfigDir()

	// 1. Prepare (regenerates script and override)
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

	// Create log file for installation output
	logFile, err := config.CreateLogFile("install_packages")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to create log file: %v\n", err)
	}
	if logFile != nil {
		defer func() {
			if err := logFile.Close(); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: Failed to close log file: %v\n", err)
			}
		}()
		if ui.GumAvailable() {
			fmt.Printf("%sInstall log: %s%s\n", ui.ColorGrey, logFile.Name(), ui.ColorReset)
		} else {
			fmt.Printf("Install log: %s\n", logFile.Name())
		}
	}

	fmt.Println()

	// 2. Execute script using 'run --rm' to ensure it works whether a container is running or not
	scriptPath := "/home/construct/.config/construct-cli/container/install_user_packages.sh"
	runFlags := []string{"--rm", "-T", "--entrypoint", "bash", "construct-box", scriptPath}

	cmd, err := runtime.BuildComposeCommand(containerRuntime, configPath, "run", runFlags)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to build install command: %v\n", err)
		os.Exit(1)
	}

	cmd.Dir = config.GetContainerDir()

	// Use helper to run with spinner
	if err := ui.RunCommandWithSpinner(cmd, "Installing user-defined packages...", logFile); err != nil {
		fmt.Fprintf(os.Stderr, "Error: Package installation failed: %v\n", err)
		os.Exit(1)
	}

	if ui.GumAvailable() {
		ui.GumSuccess("User packages installed successfully!")
	} else {
		fmt.Println("✅ User packages installed successfully!")
	}
}

// ReinstallPackages clears the packages volume and reapplies packages.toml.
func ReinstallPackages(cfg *config.Config) {
	containerRuntime := runtime.DetectRuntime(cfg.Runtime.Engine)

	if ui.GumAvailable() {
		ui.GumInfo("Reinstalling packages: removing construct-packages volume first")
	} else {
		fmt.Println("Reinstalling packages: removing construct-packages volume first")
	}

	var cmd *exec.Cmd
	switch containerRuntime {
	case "docker":
		cmd = exec.Command("docker", "volume", "rm", "-f", "construct-packages")
	case "podman":
		cmd = exec.Command("podman", "volume", "rm", "-f", "construct-packages")
	case "container":
		cmd = exec.Command("docker", "volume", "rm", "-f", "construct-packages")
	default:
		fmt.Fprintf(os.Stderr, "Warning: unsupported runtime for package volume cleanup: %s\n", containerRuntime)
		InstallPackages(cfg)
		return
	}

	if output, err := cmd.CombinedOutput(); err != nil {
		if ui.GumAvailable() {
			ui.GumWarning(fmt.Sprintf("Failed to remove construct-packages volume: %v", err))
		} else {
			fmt.Fprintf(os.Stderr, "Warning: Failed to remove construct-packages volume: %v\n", err)
		}
		if trimmed := strings.TrimSpace(string(output)); trimmed != "" {
			fmt.Fprintln(os.Stderr, trimmed)
		}
	}

	InstallPackages(cfg)
}

package sys

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/EstebanForge/construct-cli/internal/config"
	"github.com/EstebanForge/construct-cli/internal/errors"
	"github.com/EstebanForge/construct-cli/internal/runtime"
	"github.com/EstebanForge/construct-cli/internal/ui"
)

// UpdateAgents runs the update-all script inside the container.
func UpdateAgents(cfg *config.Config) {
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

	var cmd *exec.Cmd

	composeArgs := runtime.GetComposeFileArgs(configPath)

	// Build the command to run update script
	if containerRuntime == "docker" {
		if _, err := exec.LookPath("docker-compose"); err == nil {
			args := append(composeArgs, "run", "--rm", "--entrypoint", "/usr/local/bin/update-all.sh", "construct-box")
			cmd = exec.Command("docker-compose", args...)
		} else {
			args := []string{"compose"}
			args = append(args, composeArgs...)
			args = append(args, "run", "--rm", "--entrypoint", "/usr/local/bin/update-all.sh", "construct-box")
			cmd = exec.Command("docker", args...)
		}
	} else if containerRuntime == "podman" {
		args := append(composeArgs, "run", "--rm", "--entrypoint", "/usr/local/bin/update-all.sh", "construct-box")
		cmd = exec.Command("podman-compose", args...)
	} else if containerRuntime == "container" {
		args := []string{"compose"}
		args = append(args, composeArgs...)
		args = append(args, "run", "--rm", "--entrypoint", "/usr/local/bin/update-all.sh", "construct-box")
		cmd = exec.Command("docker", args...)
	}

	cmd.Dir = config.GetContainerDir()
	cmd.Env = env

	// Use helper to run with spinner
	if err := ui.RunCommandWithSpinner(cmd, "Updating all AI agents and tools...", logFile); err != nil {
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
	if containerRuntime == "docker" {
		cmd = exec.Command("docker", "volume", "rm", "construct-agents", "construct-packages")
	} else if containerRuntime == "podman" {
		cmd = exec.Command("podman", "volume", "rm", "construct-agents", "construct-packages")
	} else if containerRuntime == "container" {
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

package sys

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/EstebanForge/construct-cli/internal/config"
	"github.com/EstebanForge/construct-cli/internal/runtime"
	"github.com/EstebanForge/construct-cli/internal/ui"
)

// SetPassword allows users to change the construct user password inside the container.
func SetPassword(cfg *config.Config) {
	containerRuntime := runtime.DetectRuntime(cfg.Runtime.Engine)
	configPath := config.GetConfigDir()

	// Prepare runtime environment
	if err := runtime.Prepare(cfg, containerRuntime, configPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to prepare runtime: %v\n", err)
		os.Exit(1)
	}

	// Check if container is running
	checkCmd, err := runtime.BuildComposeCommand(containerRuntime, configPath, "ps", []string{"-q", "construct-box"})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to build command: %v\n", err)
		os.Exit(1)
	}
	output, err := checkCmd.CombinedOutput()
	if err != nil || len(output) == 0 {
		fmt.Fprintf(os.Stderr, "Error: The Construct container is not running.\n")
		fmt.Fprintf(os.Stderr, "Start it first with: construct sys shell\n")
		os.Exit(1)
	}

	if ui.GumAvailable() {
		ui.GumInfo("Changing password for construct user inside the container...")
	} else {
		fmt.Println("Changing password for construct user inside the container...")
	}
	fmt.Println()

	// Get new password
	var password string
	if ui.GumAvailable() {
		// Use gum for password input
		password, err = getPasswordWithGum("New password")
		if err != nil {
			if ui.GumAvailable() {
				ui.GumError("Failed to read password")
			}
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		// Confirm password
		confirm, err := getPasswordWithGum("Confirm password")
		if err != nil {
			if ui.GumAvailable() {
				ui.GumError("Failed to read password confirmation")
			}
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		// Validate passwords match
		if password != confirm {
			ui.GumError("Passwords do not match")
			os.Exit(1)
		}
	} else {
		// Fallback: use passwd command inside container
		execCmd, err := runtime.BuildComposeCommand(containerRuntime, configPath, "exec", []string{"-it", "construct-box", "passwd", "construct"})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: Failed to build exec command: %v\n", err)
			os.Exit(1)
		}

		execCmd.Stdin = os.Stdin
		execCmd.Stdout = os.Stdout
		execCmd.Stderr = os.Stderr

		if err := execCmd.Run(); err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				os.Exit(exitErr.ExitCode())
			}
			fmt.Fprintf(os.Stderr, "Error: Failed to change password: %v\n", err)
			os.Exit(1)
		}

		fmt.Println()
		fmt.Println("✅ Password changed successfully!")
		return
	}

	// Validate password is not empty
	if password == "" {
		ui.GumError("Password cannot be empty")
		os.Exit(1)
	}

	// Set password using chpasswd
	chpasswdCmd, err := runtime.BuildComposeCommand(containerRuntime, configPath, "exec", []string{"-T", "construct-box", "bash", "-c", "echo 'construct:" + password + "' | chpasswd"})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to build chpasswd command: %v\n", err)
		os.Exit(1)
	}

	if err := chpasswdCmd.Run(); err != nil {
		ui.GumError("Failed to set password")
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println()
	ui.GumSuccess("Password changed successfully!")
}

// getPasswordWithGum prompts for a password using gum input --password
func getPasswordWithGum(prompt string) (string, error) {
	cmd := ui.GetGumCommand("input", "--password", "--placeholder", prompt)
	cmd.Stdin = os.Stdin
	cmd.Stderr = os.Stderr

	output, err := cmd.Output()
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(output)), nil
}

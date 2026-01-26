// Package daemon manages the optional background daemon container.
package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/EstebanForge/construct-cli/internal/config"
	"github.com/EstebanForge/construct-cli/internal/network"
	"github.com/EstebanForge/construct-cli/internal/runtime"
	"github.com/EstebanForge/construct-cli/internal/ui"
)

// Start starts a background daemon container
func Start() {
	cfg, _, err := config.Load()
	if err != nil {
		ui.GumError(fmt.Sprintf("Failed to load config: %v", err))
		os.Exit(1)
	}
	containerRuntime := runtime.DetectRuntime(cfg.Runtime.Engine)
	configPath := config.GetConfigDir()
	containerName := "construct-cli-daemon"

	// Check if daemon already running
	state := runtime.GetContainerState(containerRuntime, containerName)

	switch state {
	case runtime.ContainerStateRunning:
		ui.GumWarning("Daemon is already running")
		fmt.Println("Use 'construct sys daemon attach' to connect")
		os.Exit(1)
	case runtime.ContainerStateExited:
		ui.GumInfo("Removing stopped daemon container...")
		if err := runtime.CleanupExitedContainer(containerRuntime, containerName); err != nil {
			ui.GumWarning(fmt.Sprintf("Failed to cleanup: %v", err))
		}
	}

	// Prepare runtime environment
	if err := runtime.Prepare(cfg, containerRuntime, configPath); err != nil {
		ui.GumError(fmt.Sprintf("Failed to prepare runtime: %v", err))
		os.Exit(1)
	}

	// Check if image exists
	checkCmdArgs := runtime.GetCheckImageCommand(containerRuntime)
	checkCmd := exec.Command(checkCmdArgs[0], checkCmdArgs[1:]...)
	checkCmd.Dir = config.GetContainerDir()
	if err := checkCmd.Run(); err != nil {
		ui.GumError("Construct image not found. Run 'construct sys init' first")
		os.Exit(1)
	}

	// Prepare environment variables
	cwd, err := os.Getwd()
	if err != nil {
		ui.GumError(fmt.Sprintf("Failed to get working directory: %v", err))
		os.Exit(1)
	}
	env := os.Environ()
	env = append(env, "PWD="+cwd)

	// Network configuration
	env = network.InjectEnv(env, cfg)

	ui.GumInfo("Starting daemon container...")

	var cmd *exec.Cmd

	composeArgs := runtime.GetComposeFileArgs(configPath)

	// Build the run command with -d for detached and custom name
	switch containerRuntime {
	case "docker":
		if _, err := exec.LookPath("docker-compose"); err == nil {
			args := append(composeArgs, "run", "-d", "--name", containerName, "construct-box")
			cmd = exec.Command("docker-compose", args...)
		} else {
			args := make([]string, 0, 1+len(composeArgs)+5)
			args = append(args, "compose")
			args = append(args, composeArgs...)
			args = append(args, "run", "-d", "--name", containerName, "construct-box")
			cmd = exec.Command("docker", args...)
		}
	case "podman":
		args := append(composeArgs, "run", "-d", "--name", containerName, "construct-box")
		cmd = exec.Command("podman-compose", args...)
	case "container":
		args := make([]string, 0, 1+len(composeArgs)+5)
		args = append(args, "compose")
		args = append(args, composeArgs...)
		args = append(args, "run", "-d", "--name", containerName, "construct-box")
		cmd = exec.Command("docker", args...)
	}

	cmd.Dir = config.GetContainerDir()
	cmd.Env = env

	if err := cmd.Run(); err != nil {
		ui.GumError(fmt.Sprintf("Failed to start daemon: %v", err))
		os.Exit(1)
	}

	ui.GumSuccess("Daemon started")
	fmt.Println()
	ui.GumInfo("Use 'construct sys daemon attach' to connect")
	ui.GumInfo("Use Ctrl+P Ctrl+Q to detach without stopping")
}

// Stop stops the daemon container
func Stop() {
	cfg, _, err := config.Load()
	if err != nil {
		ui.GumError(fmt.Sprintf("Failed to load config: %v", err))
		os.Exit(1)
	}
	containerRuntime := runtime.DetectRuntime(cfg.Runtime.Engine)
	containerName := "construct-cli-daemon"

	state := runtime.GetContainerState(containerRuntime, containerName)

	switch state {
	case runtime.ContainerStateMissing:
		ui.GumWarning("Daemon is not running")
		os.Exit(1)
	case runtime.ContainerStateExited:
		ui.GumInfo("Daemon is already stopped")
		ui.GumInfo("Cleaning up stopped container...")
		if err := runtime.CleanupExitedContainer(containerRuntime, containerName); err != nil {
			ui.GumError(fmt.Sprintf("Failed to cleanup: %v", err))
			os.Exit(1)
		}
		ui.GumSuccess("Cleaned up")
		return
	}

	ui.GumInfo("Stopping daemon...")

	if err := runtime.StopContainer(containerRuntime, containerName); err != nil {
		ui.GumError(fmt.Sprintf("Failed to stop daemon: %v", err))
		os.Exit(1)
	}

	ui.GumSuccess("Daemon stopped")
}

// Attach attaches to the running daemon
func Attach() {
	cfg, _, err := config.Load()
	if err != nil {
		ui.GumError(fmt.Sprintf("Failed to load config: %v", err))
		os.Exit(1)
	}
	containerRuntime := runtime.DetectRuntime(cfg.Runtime.Engine)
	containerName := "construct-cli-daemon"

	state := runtime.GetContainerState(containerRuntime, containerName)

	if state != runtime.ContainerStateRunning {
		ui.GumWarning("Daemon is not running")
		fmt.Println("Use 'construct sys daemon start' to start it")
		os.Exit(1)
	}

	fmt.Println()
	ui.GumInfo("Attaching to daemon... (Ctrl+P Ctrl+Q to detach)")
	fmt.Println()

	var cmd *exec.Cmd
	if containerRuntime == "docker" || containerRuntime == "container" {
		cmd = exec.Command("docker", "attach", containerName)
	} else {
		cmd = exec.Command("podman", "attach", containerName)
	}

	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		// Attach exits with error when container stops
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		ui.GumError(fmt.Sprintf("Failed to attach: %v", err))
		os.Exit(1)
	}
}

// Status shows the status of the daemon
func Status() {
	cfg, _, err := config.Load()
	if err != nil {
		ui.GumError(fmt.Sprintf("Failed to load config: %v", err))
		os.Exit(1)
	}
	containerRuntime := runtime.DetectRuntime(cfg.Runtime.Engine)
	containerName := "construct-cli-daemon"

	state := runtime.GetContainerState(containerRuntime, containerName)

	if !ui.GumAvailable() {
		statusBasic(state, containerRuntime, containerName)
		return
	}

	fmt.Println()
	cmd := exec.Command("gum", "style", "--border", "rounded",
		"--padding", "1 2", "--bold", "Daemon Status")
	cmd.Stdout = os.Stdout
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to render status header: %v\n", err)
	}
	fmt.Println()

	cmd = exec.Command("gum", "style", "--foreground", "242",
		fmt.Sprintf("Container: %s", containerName))
	cmd.Stdout = os.Stdout
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to render container label: %v\n", err)
	}

	switch state {
	case runtime.ContainerStateRunning:
		cmd = exec.Command("gum", "style", "--foreground", "212",
			"Status: Running ✓")
		cmd.Stdout = os.Stdout
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to render status: %v\n", err)
		}

		// Get uptime
		var uptimeCmd *exec.Cmd
		if containerRuntime == "docker" || containerRuntime == "container" {
			uptimeCmd = exec.Command("docker", "ps", "--filter",
				fmt.Sprintf("name=^%s$", containerName),
				"--format", "{{.RunningFor}}")
		} else {
			uptimeCmd = exec.Command("podman", "ps", "--filter",
				fmt.Sprintf("name=^%s$", containerName),
				"--format", "{{.RunningFor}}")
		}

		output, err := uptimeCmd.Output()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to get uptime: %v\n", err)
			output = []byte("unknown")
		}
		cmd = exec.Command("gum", "style", "--foreground", "242",
			fmt.Sprintf("Uptime: %s", strings.TrimSpace(string(output))))
		cmd.Stdout = os.Stdout
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to render uptime: %v\n", err)
		}

		fmt.Println()
		cmd = exec.Command("gum", "style", "--foreground", "86",
			"💡 Use 'construct sys daemon attach' to connect")
		cmd.Stdout = os.Stdout
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to render hint: %v\n", err)
		}

	case runtime.ContainerStateExited:
		cmd = exec.Command("gum", "style", "--foreground", "214",
			"Status: Stopped")
		cmd.Stdout = os.Stdout
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to render status: %v\n", err)
		}
		fmt.Println()
		cmd = exec.Command("gum", "style", "--foreground", "86",
			"💡 Use 'construct sys daemon start' to start")
		cmd.Stdout = os.Stdout
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to render hint: %v\n", err)
		}

	case runtime.ContainerStateMissing:
		cmd = exec.Command("gum", "style", "--foreground", "196",
			"Status: Not created")
		cmd.Stdout = os.Stdout
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to render status: %v\n", err)
		}
		fmt.Println()
		cmd = exec.Command("gum", "style", "--foreground", "86",
			"💡 Use 'construct sys daemon start' to create")
		cmd.Stdout = os.Stdout
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to render hint: %v\n", err)
		}
	}

	fmt.Println()
}

// statusBasic shows daemon status without Gum (fallback)
func statusBasic(state runtime.ContainerState, containerRuntime, containerName string) {
	fmt.Println("\n=== Daemon Status ===")
	fmt.Printf("Container: %s\n", containerName)

	switch state {
	case runtime.ContainerStateRunning:
		fmt.Println("Status: Running ✓")

		// Get uptime
		var uptimeCmd *exec.Cmd
		if containerRuntime == "docker" || containerRuntime == "container" {
			uptimeCmd = exec.Command("docker", "ps", "--filter",
				fmt.Sprintf("name=^%s$", containerName),
				"--format", "{{.RunningFor}}")
		} else {
			uptimeCmd = exec.Command("podman", "ps", "--filter",
				fmt.Sprintf("name=^%s$", containerName),
				"--format", "{{.RunningFor}}")
		}

		output, err := uptimeCmd.Output()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to get uptime: %v\n", err)
			output = []byte("unknown")
		}
		fmt.Printf("Uptime: %s\n", strings.TrimSpace(string(output)))
		fmt.Println()
		fmt.Println("💡 Use 'construct sys daemon attach' to connect")

	case runtime.ContainerStateExited:
		fmt.Println("Status: Stopped")
		fmt.Println()
		fmt.Println("💡 Use 'construct sys daemon start' to start")

	case runtime.ContainerStateMissing:
		fmt.Println("Status: Not created")
		fmt.Println()
		fmt.Println("💡 Use 'construct sys daemon start' to create")
	}

	fmt.Println()
}

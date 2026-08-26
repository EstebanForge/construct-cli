// Package daemon manages the optional background daemon container.
package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"golang.org/x/term"

	msb "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/EstebanForge/construct-cli/internal/config"
	"github.com/EstebanForge/construct-cli/internal/network"
	"github.com/EstebanForge/construct-cli/internal/runtime"
	"github.com/EstebanForge/construct-cli/internal/ui"
)

// Start starts a background daemon container or sandbox
func Start() {
	cfg, _, err := config.Load()
	if err != nil {
		ui.GumError(fmt.Sprintf("Failed to load config: %v", err))
		os.Exit(1)
	}
	if cfg.Runtime.Backend == "microvm" {
		startMsb(cfg)
		return
	}
	if err := runtime.ValidateBackendSelected(cfg); err != nil {
		ui.GumError(err.Error())
		os.Exit(1)
	}
	containerRuntime := runtime.ResolveContainerRuntime(cfg)
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
			ui.GumError(fmt.Sprintf("Failed to cleanup stopped container: %v", err))
			os.Exit(1)
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
	env = runtime.AppendProjectPathEnv(env)
	env = runtime.AppendRuntimeIdentityEnv(env, containerRuntime)

	// Network configuration
	env = network.InjectEnv(env, cfg)

	ui.GumInfo("Starting daemon container...")

	cmd, err := runtime.BuildComposeCommand(containerRuntime, configPath, "run", []string{"-d", "--name", containerName, "construct-box"})
	if err != nil {
		ui.GumError(fmt.Sprintf("Failed to build daemon start command: %v", err))
		os.Exit(1)
	}

	cmd.Dir = config.GetContainerDir()
	cmd.Env = env

	// Capture combined output so docker/compose failures surface their actual
	// error message instead of a bare "exit status 1".
	out, err := cmd.CombinedOutput()
	if err != nil {
		ui.GumError(fmt.Sprintf("Failed to start daemon: %v\n%s", err, strings.TrimSpace(string(out))))
		os.Exit(1)
	}

	ui.GumSuccess("Daemon started")
	fmt.Println()
	ui.GumInfo("Use 'construct sys daemon attach' to connect")
	ui.GumInfo("Use Ctrl+P Ctrl+Q to detach without stopping")
}

// startMsb boots the persistent msb daemon sandbox (docs/VMs.md §7 Step 7).
func startMsb(cfg *config.Config) {
	cwd, err := os.Getwd()
	if err != nil {
		ui.GumError(fmt.Sprintf("Failed to get working directory: %v", err))
		os.Exit(1)
	}

	var allowHome bool
	var maxEntries int
	if cfg != nil {
		allowHome = cfg.Sandbox.AllowHomeWorkspace
		maxEntries = cfg.Sandbox.WorkspaceMaxEntries
	}
	verdict := runtime.EvaluateWorkspace(cwd, maxEntries)
	if err := runtime.EnforceWorkspace(verdict, runtime.WorkspacePolicy{
		AllowHome:   allowHome,
		Interactive: term.IsTerminal(int(os.Stdin.Fd())),
		Confirm:     ui.GumConfirm,
	}); err != nil {
		ui.GumError(fmt.Sprintf("MicroVM workspace refused: %v", err))
		os.Exit(1)
	}

	ui.GumInfo("Starting daemon sandbox...")
	sb, err := runtime.EnsureMsbDaemon(context.Background(), cfg, cwd)
	if err != nil {
		if errors.Is(err, runtime.ErrMsbDaemonWorkdirUnmapped) {
			ui.GumError("Current directory is outside the configured daemon mount paths")
			fmt.Println("Add its root to [daemon] mount_paths (config.toml) or disable daemon.multi_paths_enabled")
			os.Exit(1)
		}
		ui.GumError(fmt.Sprintf("Failed to start microvm daemon: %v", err))
		os.Exit(1)
	}
	// Detached sandbox: Detach releases the handle without stopping the VM.
	_ = sb.Detach(context.Background()) //nolint:errcheck // sandbox keeps running detached
	ui.GumSuccess("Daemon started")
	fmt.Println()
	ui.GumInfo("Use 'construct sys daemon attach' to connect")
	ui.GumInfo("Use 'construct sys daemon stop' to stop (state persists)")
}

// stopMsb stops the msb daemon sandbox; the root disk persists. Waits for
// the stopped state so a subsequent start does not race the draining VM.
func stopMsb() error {
	ctx := context.Background()
	h, err := msb.GetSandbox(ctx, "construct-cli-daemon")
	if err != nil {
		return fmt.Errorf("daemon is not running: %w", err)
	}
	if h.Status() == msb.SandboxStatusStopped {
		ui.GumInfo("Daemon is already stopped")
		return nil
	}
	ui.GumInfo("Stopping microVM daemon...")
	if err := h.Stop(ctx); err != nil {
		return fmt.Errorf("failed to stop microvm daemon: %w", err)
	}
	for i := 0; i < 30; i++ {
		fresh, err := h.Refresh(ctx)
		if err != nil || fresh.Status() == msb.SandboxStatusStopped {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	ui.GumSuccess("Daemon stopped")
	return nil
}

// attachMsb opens an interactive shell in the running daemon sandbox.
func attachMsb(cfg *config.Config) {
	m := runtime.NewMsbBackend()
	state, err := m.State(context.Background(), "construct-cli-daemon")
	if err != nil || state != runtime.ContainerStateRunning {
		ui.GumWarning("Daemon sandbox is not running")
		fmt.Println("Use 'construct sys daemon start' to start it")
		os.Exit(1)
	}
	shell := "/bin/bash"
	if cfg != nil && cfg.Sandbox.Shell != "" {
		shell = cfg.Sandbox.Shell
	}
	ctx := context.Background()
	workdir := "/workspaces"
	if dm := runtime.ResolveDaemonMounts(cfg); dm.Enabled {
		// Multi-path mode: land the shell in the creating cwd mapped under
		// the configured mount set (hash-validated by the daemon path).
		if projectRoot := runtime.GetMsbDaemonProjectDir(ctx); projectRoot != "" {
			if mapped, ok := runtime.MapDaemonWorkdirFromMounts(projectRoot, dm.Mounts); ok {
				workdir = mapped
			}
		}
	} else {
		projectRoot := runtime.GetMsbDaemonProjectDir(ctx)
		workdir = runtime.GetMsbWorkspaceMountDest(projectRoot)
	}

	code, err := m.ExecInteractive(ctx, runtime.ExecOptions{
		Name:    "construct-cli-daemon",
		Command: []string{shell},
		Workdir: workdir,
		User:    "construct",
	})
	if err != nil {
		ui.GumError(fmt.Sprintf("Failed to attach: %v", err))
		os.Exit(1)
	}
	os.Exit(code)
}

// statusMsb reports the msb daemon sandbox state.
func statusMsb() {
	state, err := runtime.NewMsbBackend().State(context.Background(), "construct-cli-daemon")
	fmt.Println("\n=== Daemon Status (microvm) ===")
	fmt.Println("Sandbox: construct-cli-daemon")
	if err != nil {
		fmt.Printf("Status: unknown (%v)\n", err)
		return
	}
	switch state {
	case runtime.ContainerStateRunning:
		fmt.Println("Status: Running ✓")
		fmt.Println("Use 'construct sys daemon attach' to connect")
	case runtime.ContainerStateExited:
		fmt.Println("Status: Stopped")
		fmt.Println("Use 'construct sys daemon start' to start")
	default:
		fmt.Println("Status: Not created")
		fmt.Println("Use 'construct sys daemon start' to create")
	}
}

// Stop stops the daemon container
func Stop() {
	cfg, _, err := config.Load()
	if err != nil {
		ui.GumError(fmt.Sprintf("Failed to load config: %v", err))
		os.Exit(1)
	}
	if cfg.Runtime.Backend == "microvm" {
		if err := stopMsb(); err != nil {
			ui.GumWarning("Daemon is not running")
			os.Exit(1)
		}
		return
	}
	if err := runtime.ValidateBackendSelected(cfg); err != nil {
		ui.GumError(err.Error())
		os.Exit(1)
	}
	containerRuntime := runtime.ResolveContainerRuntime(cfg)
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

// Restart restarts the daemon container
func Restart() {
	cfg, _, err := config.Load()
	if err != nil {
		ui.GumError(fmt.Sprintf("Failed to load config: %v", err))
		os.Exit(1)
	}
	if cfg.Runtime.Backend == "microvm" {
		_ = stopMsb() //nolint:errcheck // best-effort stop before start on restart
		startMsb(cfg)
		return
	}
	if err := runtime.ValidateBackendSelected(cfg); err != nil {
		ui.GumError(err.Error())
		os.Exit(1)
	}
	containerRuntime := runtime.ResolveContainerRuntime(cfg)
	containerName := "construct-cli-daemon"

	state := runtime.GetContainerState(containerRuntime, containerName)

	switch state {
	case runtime.ContainerStateMissing:
		ui.GumInfo("Daemon is not running, starting...")
		Start()
		return
	case runtime.ContainerStateExited:
		ui.GumInfo("Daemon is stopped, starting...")
		Start()
		return
	}

	ui.GumInfo("Restarting daemon...")

	if err := runtime.StopContainer(containerRuntime, containerName); err != nil {
		ui.GumError(fmt.Sprintf("Failed to stop daemon: %v", err))
		os.Exit(1)
	}

	Start()
}

// Attach attaches to the running daemon
func Attach() {
	cfg, _, err := config.Load()
	if err != nil {
		ui.GumError(fmt.Sprintf("Failed to load config: %v", err))
		os.Exit(1)
	}
	if cfg.Runtime.Backend == "microvm" {
		attachMsb(cfg)
		return
	}
	if err := runtime.ValidateBackendSelected(cfg); err != nil {
		ui.GumError(err.Error())
		os.Exit(1)
	}
	containerRuntime := runtime.ResolveContainerRuntime(cfg)
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
	if cfg.Runtime.Backend == "microvm" {
		statusMsb()
		ServiceStatus()
		return
	}
	if err := runtime.ValidateBackendSelected(cfg); err != nil {
		ui.GumError(err.Error())
		os.Exit(1)
	}
	containerRuntime := runtime.ResolveContainerRuntime(cfg)
	containerName := "construct-cli-daemon"

	state := runtime.GetContainerState(containerRuntime, containerName)

	if !ui.GumAvailable() {
		statusBasic(state, containerRuntime, containerName)
		ServiceStatus()
		return
	}

	fmt.Println()
	cmd := ui.GetGumCommand("style", "--border", "rounded",
		"--padding", "1 2", "--bold", "Daemon Status")
	cmd.Stdout = os.Stdout
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to render status header: %v\n", err)
	}
	fmt.Println()

	cmd = ui.GetGumCommand("style", "--foreground", "242",
		fmt.Sprintf("Container: %s", containerName))
	cmd.Stdout = os.Stdout
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to render container label: %v\n", err)
	}

	switch state {
	case runtime.ContainerStateRunning:
		cmd = ui.GetGumCommand("style", "--foreground", "212",
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
		cmd = ui.GetGumCommand("style", "--foreground", "242",
			fmt.Sprintf("Uptime: %s", strings.TrimSpace(string(output))))
		cmd.Stdout = os.Stdout
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to render uptime: %v\n", err)
		}

		fmt.Println()
		cmd = ui.GetGumCommand("style", "--foreground", "86",
			"💡 Use 'construct sys daemon attach' to connect")
		cmd.Stdout = os.Stdout
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to render hint: %v\n", err)
		}

	case runtime.ContainerStateExited:
		cmd = ui.GetGumCommand("style", "--foreground", "214",
			"Status: Stopped")
		cmd.Stdout = os.Stdout
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to render status: %v\n", err)
		}
		fmt.Println()
		cmd = ui.GetGumCommand("style", "--foreground", "86",
			"💡 Use 'construct sys daemon start' to start")
		cmd.Stdout = os.Stdout
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to render hint: %v\n", err)
		}

	case runtime.ContainerStateMissing:
		cmd = ui.GetGumCommand("style", "--foreground", "196",
			"Status: Not created")
		cmd.Stdout = os.Stdout
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to render status: %v\n", err)
		}
		fmt.Println()
		cmd = ui.GetGumCommand("style", "--foreground", "86",
			"💡 Use 'construct sys daemon start' to create")
		cmd.Stdout = os.Stdout
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to render hint: %v\n", err)
		}
	}

	fmt.Println()
	ServiceStatus()
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

// RootsList prints the daemon's learned roots (host dirs the daemon
// learned to mount under single-path mode, phase 2). Pinned configured
// paths from daemon.mount_paths are shown alongside, marked "configured".
// "remember to forget" the path with `construct sys daemon roots forget
// <path>` once the project is done.
func RootsList() {
	cfg, _, err := config.Load()
	if err != nil {
		ui.GumError(fmt.Sprintf("Failed to load config: %v", err))
		os.Exit(1)
	}
	runtime.DaemonRootsList(cfg)
}

// RootsForget removes a learned root by exact path. Configured
// daemon.mount_paths entries are pinned and CANNOT be forgotten here;
// remove them from config.toml instead. Refuses paths not in the learned
// set so a typo does not silently succeed.
func RootsForget(path string) {
	cfg, _, err := config.Load()
	if err != nil {
		ui.GumError(fmt.Sprintf("Failed to load config: %v", err))
		os.Exit(1)
	}
	runtime.DaemonRootsForget(cfg, path)
}

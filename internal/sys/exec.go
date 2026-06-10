package sys

import (
	"fmt"
	"os"
	stdruntime "runtime"

	"github.com/EstebanForge/construct-cli/internal/agent"
	"github.com/EstebanForge/construct-cli/internal/config"
	"github.com/EstebanForge/construct-cli/internal/constants"
	"github.com/EstebanForge/construct-cli/internal/env"
	"github.com/EstebanForge/construct-cli/internal/runtime"
)

// ExecCommand runs a single command inside a running Construct container
// (daemon or CWD-scoped) and streams stdout/stderr to the host. Returns the
// container process exit code.
func ExecCommand(cfg *config.Config, cmdArgs []string) int {
	if len(cmdArgs) == 0 {
		fmt.Fprintf(os.Stderr, "Usage: construct sys exec -- <command> [args...]\n")
		fmt.Fprintf(os.Stderr, "\nExample:\n")
		fmt.Fprintf(os.Stderr, "  construct sys exec -- npm update -g @anthropic-ai/claude-code\n")
		fmt.Fprintf(os.Stderr, "  construct sys exec -- cat /etc/os-release\n")
		return 1
	}

	containerRuntime := runtime.DetectRuntime(func() string {
		if cfg != nil {
			return cfg.Runtime.Engine
		}
		return ""
	}())

	// Try daemon first, then CWD container.
	containerName, workdir, ok := resolveExecTarget(cfg, containerRuntime)
	if !ok {
		return 1
	}

	// Build environment (PATH, HOME, CONSTRUCT_PATH).
	osEnv := buildExecEnv()

	// Determine exec user.
	execUser := resolveExecUser(cfg, containerRuntime)

	exitCode, err := runtime.ExecNonInteractiveStream(
		containerRuntime, containerName, cmdArgs, osEnv, workdir, execUser,
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	return exitCode
}

// resolveExecTarget finds a running container and computes the workdir.
// Returns (containerName, workdir, ok).
func resolveExecTarget(cfg *config.Config, containerRuntime string) (string, string, bool) {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to get working directory: %v\n", err)
		return "", "", false
	}

	// 1. Try daemon.
	daemonState := runtime.GetContainerState(containerRuntime, constants.DaemonName)
	if daemonState == runtime.ContainerStateRunning {
		name, workdir, ok := tryDaemonTarget(cfg, containerRuntime, cwd)
		if ok {
			return name, workdir, true
		}
	}

	// 2. Try CWD-scoped container.
	ctName := runtime.CwdContainerName(cwd)
	ctState := runtime.GetContainerState(containerRuntime, ctName)
	if ctState == runtime.ContainerStateRunning {
		// CWD container mounts the project at the host CWD path.
		// Use the same path inside the container as the workdir.
		return ctName, cwd, true
	}

	// Nothing running. Print actionable error.
	fmt.Fprintf(os.Stderr, "Error: no running container for this directory.\n")
	fmt.Fprintf(os.Stderr, "\nStart one first:\n")
	fmt.Fprintf(os.Stderr, "  construct sys shell              # interactive shell\n")
	fmt.Fprintf(os.Stderr, "  construct sys daemon start       # background daemon\n")
	return "", "", false
}

// tryDaemonTarget validates the daemon is usable and maps the CWD to a
// container workdir. Returns (daemonName, workdir, ok).
func tryDaemonTarget(cfg *config.Config, containerRuntime, cwd string) (string, string, bool) {
	imageName := constants.ImageName + ":latest"
	if runtime.IsContainerStale(containerRuntime, constants.DaemonName, imageName) {
		return "", "", false
	}

	daemonMounts := runtime.ResolveDaemonMounts(cfg)
	if daemonMounts.Enabled {
		label, err := runtime.GetContainerLabel(containerRuntime, constants.DaemonName, runtime.DaemonMountsLabelKey)
		if err != nil {
			return "", "", false
		}
		if label != daemonMounts.Hash {
			return "", "", false
		}
		workdir, ok := runtime.MapDaemonWorkdirFromMounts(cwd, daemonMounts.Mounts)
		if !ok {
			return "", "", false
		}
		return constants.DaemonName, workdir, true
	}

	// Single-mount path.
	daemonWorkdir, err := runtime.GetContainerWorkingDir(containerRuntime, constants.DaemonName)
	if err != nil {
		return "", "", false
	}
	mountSource, err := runtime.GetContainerMountSource(containerRuntime, constants.DaemonName, daemonWorkdir)
	if err != nil {
		return "", "", false
	}
	workdir, ok := agent.MapDaemonWorkdir(cwd, mountSource, daemonWorkdir)
	if !ok {
		return "", "", false
	}
	return constants.DaemonName, workdir, true
}

// buildExecEnv constructs the environment variables for the exec session.
// Container home is always /home/construct (invariant set by Dockerfile).
func buildExecEnv() []string {
	var envVars []string

	constructPath := env.BuildConstructPath("/home/construct")
	env.SetEnvVar(&envVars, "PATH", constructPath)
	env.SetEnvVar(&envVars, "CONSTRUCT_PATH", constructPath)
	env.SetEnvVar(&envVars, "HOME", "/home/construct")

	// Inject keyring env (same as shell). Reuse agent package implementation.
	keyringEnv := agent.ReadKeyringEnv()
	for k, v := range keyringEnv {
		env.SetEnvVar(&envVars, k, v)
	}

	return envVars
}

// resolveExecUser returns the user to exec as inside the container.
// Note: matches agent.resolveExecUserForRunningContainer which only handles
// "docker" for the UID-mapping path. The "container" runtime alias routes to
// the docker binary but does not need UID remapping (same as engine.go).
func resolveExecUser(cfg *config.Config, containerRuntime string) string {
	if containerRuntime != "docker" || stdruntime.GOOS != "linux" {
		return "construct"
	}
	if cfg == nil || !cfg.Sandbox.ExecAsHostUser {
		return "construct"
	}
	if runtime.UsesUserNamespaceRemap(containerRuntime) {
		return "construct"
	}
	uid := os.Getuid()
	if uid == 0 {
		return "construct"
	}
	return fmt.Sprintf("%d:%d", uid, os.Getgid())
}

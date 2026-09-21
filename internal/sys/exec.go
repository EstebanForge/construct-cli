package sys

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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

	if cfg != nil && cfg.Runtime.Backend == "microvm" {
		return execMsbCommand(cfg, cmdArgs)
	}

	if err := runtime.ValidateBackendSelected(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	containerRuntime := runtime.ResolveContainerRuntime(cfg)

	// Try daemon first, then CWD container.
	containerName, workdir, ok := resolveExecTarget(cfg, containerRuntime)
	if !ok {
		return 1
	}

	// Build environment (PATH, HOME, CONSTRUCT_PATH).
	osEnv := buildExecEnv()

	// Determine exec user.
	execUser := runtime.ResolveExecUser(cfg, containerRuntime)

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

// msbExecWorkdir maps the host cwd to the guest directory the msb daemon
// serves, mirroring the daemon's mount layout:
//   - multi-path mode: every configured root lives under /workspaces/<sha8>.
//     A cwd outside the set fails closed with the same error the daemon
//     start path uses — silently execing from the wrong place is worse.
//   - single-path mode: the boot-time project dir is mounted at
//     /workspaces/<name>; same-project execs land exactly there.
//
// The msb daemon never mounts /workspace (that is the docker-compose
// layout), which is why this must not reuse the container path.
func msbExecWorkdir(ctx context.Context, cfg *config.Config, cwd string) (string, error) {
	dm := runtime.ResolveDaemonMounts(cfg)
	if dm.Enabled {
		mapped, ok := runtime.MapDaemonWorkdirFromMounts(cwd, dm.Mounts)
		if !ok {
			return "", fmt.Errorf("%w: %s (add the root to [daemon] mount_paths or disable daemon.multi_paths_enabled)", runtime.ErrMsbDaemonWorkdirUnmapped, cwd)
		}
		return mapped, nil
	}
	// Single-path: the daemon mounts its boot-time project dir under
	// /workspaces/<name>. A cross-project cwd would chdir into an absent
	// guest dir and die on an unexplained ENOENT — enforce the boot-time
	// label when it is readable, degrade to the blind mapping when not.
	if bootDir := runtime.GetMsbDaemonProjectDir(ctx); bootDir != "" {
		resolved := cwd
		if r, err := filepath.EvalSymlinks(cwd); err == nil {
			resolved = r
		}
		if resolved != bootDir && !strings.HasPrefix(resolved+string(os.PathSeparator), bootDir+string(os.PathSeparator)) {
			return "", fmt.Errorf("%w: the daemon serves %s, current directory is %s", runtime.ErrMsbDaemonWorkdirUnmapped, bootDir, cwd)
		}
	}
	return runtime.GetMsbWorkspaceMountDest(cwd), nil
}

// execMsbCommand streams a non-interactive command inside the running msb
// daemon sandbox.
func execMsbCommand(cfg *config.Config, cmdArgs []string) int {
	ctx := context.Background()
	m := runtime.NewMsbBackend()
	state, err := m.State(ctx, "construct-cli-daemon")
	if err != nil || state != runtime.ContainerStateRunning {
		fmt.Fprintf(os.Stderr, "Error: no running sandbox for this directory.\n")
		fmt.Fprintf(os.Stderr, "\nStart one first:\n")
		fmt.Fprintf(os.Stderr, "  construct sys daemon start       # background daemon\n")
		return 1
	}

	osEnv := buildExecEnv()
	env.SetEnvVar(&osEnv, "CONSTRUCT_HOST_ALIAS", "host.microsandbox.internal")

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to get working directory: %v\n", err)
		return 1
	}
	workdir, werr := msbExecWorkdir(ctx, cfg, cwd)
	if werr != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", werr)
		return 1
	}

	execUser := runtime.ResolveExecUserMsb(cfg)

	exitCode, err := m.ExecStream(ctx, runtime.ExecOptions{
		Name:    "construct-cli-daemon",
		Command: cmdArgs,
		Env:     osEnv,
		Workdir: workdir,
		User:    execUser,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	return exitCode
}

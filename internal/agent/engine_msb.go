package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/EstebanForge/construct-cli/internal/env"
	"github.com/EstebanForge/construct-cli/internal/runtime"
	"github.com/EstebanForge/construct-cli/internal/ui"
)

// execViaMsbDaemon is the msb run path (docs/VMs.md §7 Step 7): guarantee
// the persistent daemon sandbox, install agents on first use, then exec the
// agent interactively with the same env contract the Docker daemon path
// builds (buildDaemonExecEnv + MaskEnv + exit-code fidelity).
func (e *RuntimeEngine) execViaMsbDaemon(args []string, providerEnv []string) (int, error) {
	ctx := context.Background()

	var allowHome bool
	var maxEntries int
	if e.cfg != nil {
		if strings.EqualFold(e.cfg.Network.Mode, "strict") {
			return 1, errors.New("network mode 'strict' is not yet supported under the microvm backend (use backend = \"docker\" or network mode = \"permissive\")")
		}
		allowHome = e.cfg.Sandbox.AllowHomeWorkspace
		maxEntries = e.cfg.Sandbox.WorkspaceMaxEntries
	}
	verdict := runtime.EvaluateWorkspace(e.cwd, maxEntries)
	if err := runtime.EnforceWorkspace(verdict, runtime.WorkspacePolicy{
		AllowHome:   allowHome,
		Interactive: term.IsTerminal(int(os.Stdin.Fd())),
		Confirm:     ui.GumConfirm,
	}); err != nil {
		return 1, err
	}

	if !runtime.AreAgentsInstalled() {
		if err := runtime.MsbInstallAgents(ctx, e.cfg); err != nil {
			return 1, fmt.Errorf("msb agent install: %w", err)
		}
	}

	sb, err := runtime.EnsureMsbDaemon(ctx, e.cfg, e.cwd)
	if err != nil {
		return 1, fmt.Errorf("msb daemon: %w", err)
	}
	defer func() {
		_ = sb.Detach(context.Background()) //nolint:errcheck // daemon keeps running detached
	}()

	args = applyYoloArgs(args, e.cfg)
	if len(args) == 0 {
		shell := "/bin/bash"
		if e.cfg != nil && e.cfg.Sandbox.Shell != "" {
			shell = e.cfg.Sandbox.Shell
		}
		args = []string{shell}
		ui.InfoLn("Entering Construct daemon shell...")
	} else {
		ui.InfoF("Running in Construct daemon (microvm): %v\n", args)
	}

	envVars := buildDaemonExecEnv(args, providerEnv, e.cbServer, e.execServer, e.cfg)
	applyConstructPath(&envVars)
	env.SetEnvVar(&envVars, "HOME", "/home/construct")
	env.SetEnvVar(&envVars, "CONSTRUCT_HOST_ALIAS", "host.microsandbox.internal")
	// Pin xterm-256color to guarantee terminfo compatibility inside the guest image
	env.SetEnvVar(&envVars, "TERM", "xterm-256color")

	stdTTY := term.IsTerminal(int(os.Stdin.Fd()))
	if stdTTY {
		if w, h, err := term.GetSize(int(os.Stdin.Fd())); err == nil && w > 0 && h > 0 {
			env.SetEnvVar(&envVars, "COLUMNS", fmt.Sprintf("%d", w))
			env.SetEnvVar(&envVars, "LINES", fmt.Sprintf("%d", h))
		}
	}

	execUser := runtime.ResolveExecUserMsb(e.cfg)

	// SSH agent proxy (docs/VMs.md §7): same per-session socket + socat model
	// as the Docker daemon path, but the socat target is the msb host alias
	// (§3.1) and it runs through the SDK exec instead of docker exec.
	// In msb, the socket lives in /tmp because virtiofs host mounts reject AF_UNIX bind.
	if e.sshBridge != nil {
		e.sshProxySock = fmt.Sprintf("/tmp/construct-ssh-agent.%d.sock", os.Getpid())
		if err := msbEnsureSSHProxy(e.sshBridge.Port, e.sshProxySock, execUser); err != nil {
			ui.InfoF("⚠️  SSH agent proxy not ready (microvm): %v\n", err)
		} else {
			env.SetEnvVar(&envVars, "SSH_AUTH_SOCK", e.sshProxySock)
			e.sshProxyContainer = "construct-cli-daemon" // Teardown cleans the socat via the msb exec path
			ui.InfoLn("✓ Started SSH Agent proxy (microvm)")
		}
	}
	envVars = e.sec.MaskEnv(envVars)

	workdir := "/workspaces"
	if e.cwd != "" {
		projectRoot := runtime.GetMsbDaemonProjectDir(ctx)
		if projectRoot == "" {
			projectRoot = e.cwd
		}
		dest := runtime.GetMsbWorkspaceMountDest(projectRoot)
		if mapped, ok := runtime.MapDaemonWorkdir(e.cwd, projectRoot, dest); ok {
			workdir = mapped
		}
	}

	code, err := runtime.NewMsbBackend().ExecInteractive(ctx, runtime.ExecOptions{
		Name:    "construct-cli-daemon",
		Command: args,
		Env:     envVars,
		Workdir: workdir,
		User:    execUser,
	})
	if err == nil && len(args) > 0 && (code == 126 || code == 127) {
		ui.InfoF("Hint: command '%s' may be missing from daemon PATH.\n", args[0])
		ui.InfoLn("Run 'construct sys doctor' and review Setup/Update logs for package installation errors.")
	}
	return code, err
}

// msbEnsureSSHProxy (re)starts the per-session guest socat that bridges the
// proxy UNIX socket to the host SSH bridge over the msb host alias (§3.1).
// Same script shape as the Docker path's ensureDaemonSSHProxy; the port is
// passed via env to keep it out of the shell string.
func msbEnsureSSHProxy(port int, sockPath, execUser string) error {
	script := `if ! command -v socat >/dev/null; then echo "socat not found" >&2; exit 1; fi; PROXY_SOCK="` + sockPath + `"; PROXY_DIR="$(dirname "$PROXY_SOCK")"; mkdir -p "$PROXY_DIR" 2>/dev/null || true; pkill -f "socat UNIX-LISTEN:$PROXY_SOCK" 2>/dev/null || true; rm -f "$PROXY_SOCK"; nohup socat UNIX-LISTEN:"$PROXY_SOCK",fork,mode=600 TCP:host.microsandbox.internal:"$CONSTRUCT_SSH_BRIDGE_PORT" </dev/null >/tmp/socat.log 2>&1 &`
	_, code, err := runtime.NewMsbBackend().Exec(context.Background(), runtime.ExecOptions{
		Name:    "construct-cli-daemon",
		Command: []string{"bash", "-lc", script},
		Env:     []string{fmt.Sprintf("CONSTRUCT_SSH_BRIDGE_PORT=%d", port)},
		User:    execUser,
	})
	if err != nil {
		return fmt.Errorf("start ssh proxy socat (port %d): %w", port, err)
	}
	if code != 0 {
		return fmt.Errorf("ssh proxy socat exited with code %d", code)
	}
	// Wait for the socket to appear (same budget as the Docker path).
	return msbWaitForSSHProxy(sockPath, execUser)
}

func msbWaitForSSHProxy(sockPath, execUser string) error {
	probe := `for i in $(seq 1 10); do [ -S "` + sockPath + `" ] && exit 0; sleep 0.2; done; exit 1`
	_, code, err := runtime.NewMsbBackend().Exec(context.Background(), runtime.ExecOptions{
		Name:    "construct-cli-daemon",
		Command: []string{"bash", "-c", probe},
		User:    execUser,
	})
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("ssh proxy socket %s never appeared", sockPath)
	}
	return nil
}

// msbBackendSelected reports whether the engine should take the microvm path.
func (e *RuntimeEngine) msbBackendSelected() bool {
	return e.cfg != nil && strings.EqualFold(e.cfg.Runtime.Backend, "microvm")
}

// msbClipboardHost returns the guest-visible host alias for bridge servers
// under the msb backend; empty means Docker (caller keeps its default).
func msbClipboardHost() string { return "host.microsandbox.internal" }

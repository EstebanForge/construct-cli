package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"golang.org/x/term"

	"github.com/EstebanForge/construct-cli/internal/clipboard"
	"github.com/EstebanForge/construct-cli/internal/config"
	"github.com/EstebanForge/construct-cli/internal/env"
	"github.com/EstebanForge/construct-cli/internal/runtime"
	"github.com/EstebanForge/construct-cli/internal/ui"
)

// signalRunContext wraps the msb exec lifetime in a context that cancels
// on SIGINT/SIGTERM: cancellation kills the guest exec (msbDrain), Execute
// returns, and the deferred Teardown in runWithProviderEnv runs the same
// cleanup as a normal exit. Without it a signal skipped unregister and
// copy-back entirely.
func signalRunContext() (context.Context, func()) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
}

// execViaMsbDaemon is the msb run path (docs/VMsv2.md): guarantee
// the persistent daemon sandbox, install agents on first use, then exec the
// agent interactively with the same env contract the Docker daemon path
// builds (buildDaemonExecEnv + MaskEnv + exit-code fidelity).
func (e *RuntimeEngine) execViaMsbDaemon(args []string, providerEnv []string) (int, error) {
	ctx, stopSignals := signalRunContext()
	defer stopSignals()

	var allowHome bool
	var maxEntries int
	if e.cfg != nil {
		if strings.EqualFold(e.cfg.Network.Mode, "strict") {
			return 1, errors.New("network mode 'strict' is not yet supported under the msb backend (use backend = \"containers\" or network mode = \"permissive\")")
		}
		allowHome = e.cfg.Sandbox.AllowHomeWorkspace
		maxEntries = e.cfg.Sandbox.WorkspaceMaxEntries
	}
	if err := runtime.EnforceWorkspaceRemembered(e.cwd, maxEntries, allowHome, term.IsTerminal(int(os.Stdin.Fd()))); err != nil {
		return 1, err
	}

	if !runtime.AreAgentsInstalled() {
		if err := runtime.MsbInstallAgents(ctx, e.cfg); err != nil {
			return 1, fmt.Errorf("msb agent install: %w", err)
		}
	}

	sb, err := runtime.EnsureMsbDaemonAndRegister(ctx, e.cfg, e.cwd)
	if err != nil {
		if errors.Is(err, runtime.ErrMsbDaemonWorkdirUnmapped) {
			// Docker parity ends here: msb has no per-invocation sandbox path,
			// so an unmapped cwd aborts instead of falling back to compose run.
			ui.InfoLn("Tip: add this directory's root to [daemon] mount_paths (config.toml) or disable daemon.multi_paths_enabled.")
			return 1, err
		}
		return 1, fmt.Errorf("msb daemon: %w", err)
	}
	// The ensure call registered this PID as a live session (phase 3 idle
	// stop) inside the daemon flock section. The companion Unregister +
	// MaybeSpawnIdleWatcher runs in Teardown, so when the last concurrent
	// ct invocation exits, the daemon stops after the configured idle
	// window.
	e.registeredSession = true // arm the Teardown idle-watch spawn
	defer func() {
		_ = sb.Detach(context.Background()) //nolint:errcheck // daemon keeps running detached
	}()

	args = applyYoloArgs(args, e.cfg)
	isInteractiveShell := false
	if len(args) == 0 {
		isInteractiveShell = true
		shell := "/bin/bash"
		if e.cfg != nil && e.cfg.Sandbox.Shell != "" {
			shell = e.cfg.Sandbox.Shell
		}
		args = []string{shell}
		ui.InfoLn("Entering Construct daemon shell...")
	} else {
		ui.InfoF("Running in Construct daemon (msb): %v\n", args)
	}

	envVars := buildDaemonExecEnv(args, providerEnv, e.cbServer, e.execServer, e.cfg)
	applyConstructPath(&envVars)
	env.SetEnvVar(&envVars, "HOME", "/home/construct")
	env.SetEnvVar(&envVars, "CONSTRUCT_HOST_ALIAS", runtime.MsbHostAlias)
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

	// SSH agent proxy (docs/VMsv2.md): same per-session socket + socat model
	// as the Docker daemon path, but the socat target is the msb host alias
	// (§3.1) and it runs through the SDK exec instead of docker exec.
	// In msb, the socket lives in /tmp because virtiofs host mounts reject AF_UNIX bind.
	if e.sshBridge != nil {
		e.sshProxySock = fmt.Sprintf("/tmp/construct-ssh-agent.%d.sock", os.Getpid())
		if err := msbEnsureSSHProxy(e.sshBridge.Port, e.sshProxySock, execUser); err != nil {
			ui.InfoF("⚠️  SSH agent proxy not ready (msb): %v\n", err)
		} else {
			env.SetEnvVar(&envVars, "SSH_AUTH_SOCK", e.sshProxySock)
			e.sshProxyContainer = "construct-cli-daemon" // Teardown cleans the socat via the msb exec path
			ui.InfoLn("✓ Started SSH Agent proxy (msb)")
		}
	}
	// Herdr bridge: same socat pattern as the SSH proxy; present only when
	// construct was launched from a Herdr pane.
	if e.herdrBridge != nil {
		if err := msbEnsureHerdrProxy(e.herdrBridge, e.herdrProxySock, execUser); err != nil {
			ui.LogDebug("Herdr proxy not started (msb): %v", err)
		} else {
			e.herdrProxyContainer = "construct-cli-daemon"
			e.herdrProxyUser = execUser
			envVars = append(envVars, e.herdrExecEnv()...)
		}
	}

	// X11 clipboard sync: the daemon entrypoint gets no clipboard env (msb
	// daemon env carries none) and the bridge URL/token are per-session, so
	// restart the sync here with the fresh values. Env-passed to keep the
	// token out of the guest process list. Best-effort: clipper-shim and
	// JS-patch agents read the exec env directly and need no X11 loop.
	if err := msbEnsureClipboardSync(e.cbServer, execUser); err != nil {
		ui.LogDebug("Clipboard X11 sync not started (msb): %v", err)
	}
	envVars = e.sec.MaskEnv(envVars)

	workdir := "/workspaces"
	if e.cwd != "" {
		// Longest-prefix over the daemon's actual mount set (multi-path
		// config and learned roots alike), failing closed: an unmapped cwd
		// must not silently start the agent in /workspaces, the parent of
		// every project.
		mapped, merr := runtime.MapHostWorkdirToGuest(runtime.MsbPathMaps(e.cfg, e.cwd), e.cwd)
		if merr != nil {
			return 1, fmt.Errorf("msb daemon: %w", merr)
		}
		workdir = mapped
	}

	execCmd := args
	// Human interactive shell (construct sys shell / bare ct) has unrestricted
	// visibility into all daemon mounts. Agent sessions targeting a workspace
	// execute inside an isolated mount namespace.
	if !isInteractiveShell {
		execCmd = wrapIsolatedCommand(workdir, args)
	}

	code, err := runtime.NewMsbBackend().ExecInteractive(ctx, runtime.ExecOptions{
		Name:    "construct-cli-daemon",
		Command: execCmd,
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
	script := `if ! command -v socat >/dev/null; then echo "socat not found" >&2; exit 1; fi; PROXY_SOCK="` + sockPath + `"; PROXY_DIR="$(dirname "$PROXY_SOCK")"; mkdir -p "$PROXY_DIR" 2>/dev/null || true; pkill -f "socat UNIX-LISTEN:$PROXY_SOCK" 2>/dev/null || true; rm -f "$PROXY_SOCK"; nohup socat UNIX-LISTEN:"$PROXY_SOCK",fork,mode=600 TCP:` + runtime.MsbHostAlias + `:"$CONSTRUCT_SSH_BRIDGE_PORT" </dev/null >/tmp/socat.log 2>&1 &`
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

// msbEnsureClipboardSync (re)starts the in-guest X11 clipboard sync with the
// current session's bridge URL and token. The sync script bootstraps its own
// Xvfb, so no DISPLAY setup is needed here.
func msbEnsureClipboardSync(cbServer *clipboard.Server, execUser string) error {
	if cbServer == nil {
		return nil
	}
	script := `pkill -f clipboard-x11-sync.sh 2>/dev/null || true; nohup /usr/local/bin/clipboard-x11-sync.sh </dev/null >/tmp/clipboard-x11-sync.log 2>&1 &`
	_, code, err := runtime.NewMsbBackend().Exec(context.Background(), runtime.ExecOptions{
		Name:    "construct-cli-daemon",
		Command: []string{"bash", "-lc", script},
		Env: []string{
			"CONSTRUCT_CLIPBOARD_URL=" + cbServer.URL,
			"CONSTRUCT_CLIPBOARD_TOKEN=" + cbServer.Token,
		},
		User: execUser,
	})
	if err != nil {
		return fmt.Errorf("start clipboard x11 sync: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("clipboard x11 sync exited with code %d", code)
	}
	return nil
}

// msbEnsureHerdrProxy (re)starts the in-guest socat bridging the per-session
// herdr unix socket to the host TCP bridge. Same shape as the docker proxy
// script; the msb alias is fixed and the port arrives via env like the SSH
// proxy.
func msbEnsureHerdrProxy(bridge *HerdrBridge, sockPath, execUser string) error {
	script := `if ! command -v socat >/dev/null; then echo "socat not found" >&2; exit 1; fi; PROXY_SOCK="` + sockPath + `"; PROXY_DIR="$(dirname "$PROXY_SOCK")"; mkdir -p "$PROXY_DIR" 2>/dev/null || true; chmod 700 "$PROXY_DIR" 2>/dev/null || true; pkill -f "socat UNIX-LISTEN:$PROXY_SOCK" 2>/dev/null || true; rm -f "$PROXY_SOCK"; nohup socat UNIX-LISTEN:"$PROXY_SOCK",fork,mode=600 TCP:` + runtime.MsbHostAlias + `:"$CONSTRUCT_HDR_BRIDGE_PORT" >/tmp/herdr-socat.log 2>&1 &`
	_, code, err := runtime.NewMsbBackend().Exec(context.Background(), runtime.ExecOptions{
		Name:    "construct-cli-daemon",
		Command: []string{"bash", "-lc", script},
		Env:     []string{fmt.Sprintf("CONSTRUCT_HDR_BRIDGE_PORT=%d", bridge.Port)},
		User:    execUser,
	})
	if err != nil {
		return fmt.Errorf("start herdr proxy socat (socket %s, port %d): %w", sockPath, bridge.Port, err)
	}
	if code != 0 {
		return fmt.Errorf("herdr proxy socat exited with code %d", code)
	}
	return nil
}

// msbBackendSelected reports whether the engine should take the msb path.
func (e *RuntimeEngine) msbBackendSelected() bool {
	return e.cfg != nil && strings.EqualFold(e.cfg.Runtime.Backend, config.BackendMsb)
}

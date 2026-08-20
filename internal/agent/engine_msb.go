package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/EstebanForge/construct-cli/internal/env"
	"github.com/EstebanForge/construct-cli/internal/runtime"
)

// execViaMsbDaemon is the msb run path (docs/VMs.md §7 Step 7): guarantee
// the persistent daemon sandbox, install agents on first use, then exec the
// agent interactively with the same env contract the Docker daemon path
// builds (buildDaemonExecEnv + MaskEnv + exit-code fidelity).
func (e *RuntimeEngine) execViaMsbDaemon(args []string, providerEnv []string) (int, error) {
	ctx := context.Background()

	if !runtime.AreAgentsInstalled() {
		fmt.Println("Installing agents inside the sandbox (first run)...")
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
		fmt.Println("Entering Construct daemon shell...")
	} else {
		fmt.Printf("Running in Construct daemon (msb): %v\n", args)
	}

	envVars := buildDaemonExecEnv(args, providerEnv, e.cbServer, e.execServer, e.cfg)
	applyConstructPath(&envVars)
	env.SetEnvVar(&envVars, "HOME", "/home/construct")
	env.SetEnvVar(&envVars, "CONSTRUCT_HOST_ALIAS", "host.microsandbox.internal")

	// SSH agent proxy (docs/VMs.md §7): same per-session socket + socat model
	// as the Docker daemon path, but the socat target is the msb host alias
	// (§3.1) and it runs through the SDK exec instead of docker exec.
	if e.sshBridge != nil {
		if err := msbEnsureSSHProxy(e.sshBridge.Port, e.sshProxySock); err != nil {
			fmt.Printf("⚠️  SSH agent proxy not ready (msb): %v\n", err)
		} else {
			env.SetEnvVar(&envVars, "SSH_AUTH_SOCK", e.sshProxySock)
			e.sshProxyContainer = "construct-cli-daemon" // Teardown cleans the socat via the msb exec path
			fmt.Println("✓ Started SSH Agent proxy (msb)")
		}
	}
	envVars = e.sec.MaskEnv(envVars)

	code, err := runtime.NewMsbBackend().ExecInteractive(ctx, runtime.ExecOptions{
		Name:    "construct-cli-daemon",
		Command: args,
		Env:     envVars,
		Workdir: "/workspace",
		User:    "construct",
	})
	if err == nil && len(args) > 0 && (code == 126 || code == 127) {
		fmt.Printf("Hint: command '%s' may be missing from daemon PATH.\n", args[0])
		fmt.Println("Run 'construct sys doctor' and review Setup/Update logs for package installation errors.")
	}
	return code, err
}

// msbEnsureSSHProxy (re)starts the per-session guest socat that bridges the
// proxy UNIX socket to the host SSH bridge over the msb host alias (§3.1).
// Same script shape as the Docker path's ensureDaemonSSHProxy; the port is
// passed via env to keep it out of the shell string.
func msbEnsureSSHProxy(port int, sockPath string) error {
	script := `if ! command -v socat >/dev/null; then echo "socat not found" >&2; exit 1; fi; PROXY_SOCK="` + sockPath + `"; PROXY_DIR="$(dirname "$PROXY_SOCK")"; mkdir -p "$PROXY_DIR" 2>/dev/null || true; chmod 700 "$PROXY_DIR" 2>/dev/null || true; pkill -f "socat UNIX-LISTEN:$PROXY_SOCK" 2>/dev/null || true; rm -f "$PROXY_SOCK"; nohup socat UNIX-LISTEN:"$PROXY_SOCK",fork,mode=600 TCP:host.microsandbox.internal:"$CONSTRUCT_SSH_BRIDGE_PORT" >/tmp/socat.log 2>&1 &`
	_, code, err := runtime.NewMsbBackend().Exec(context.Background(), runtime.ExecOptions{
		Name:    "construct-cli-daemon",
		Command: []string{"bash", "-lc", script},
		Env:     []string{fmt.Sprintf("CONSTRUCT_SSH_BRIDGE_PORT=%d", port)},
		User:    "construct",
	})
	if err != nil {
		return fmt.Errorf("start ssh proxy socat (port %d): %w", port, err)
	}
	if code != 0 {
		return fmt.Errorf("ssh proxy socat exited with code %d", code)
	}
	// Wait for the socket to appear (same budget as the Docker path).
	return msbWaitForSSHProxy(sockPath)
}

func msbWaitForSSHProxy(sockPath string) error {
	probe := `for i in $(seq 1 10); do [ -S "` + sockPath + `" ] && exit 0; sleep 0.2; done; exit 1`
	_, code, err := runtime.NewMsbBackend().Exec(context.Background(), runtime.ExecOptions{
		Name:    "construct-cli-daemon",
		Command: []string{"bash", "-c", probe},
		User:    "construct",
	})
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("ssh proxy socket %s never appeared", sockPath)
	}
	return nil
}

// msbBackendSelected reports whether the engine should take the msb path.
func (e *RuntimeEngine) msbBackendSelected() bool {
	return e.cfg != nil && strings.EqualFold(e.cfg.Runtime.Backend, "msb")
}

// msbClipboardHost returns the guest-visible host alias for bridge servers
// under the msb backend; empty means Docker (caller keeps its default).
func msbClipboardHost() string { return "host.microsandbox.internal" }

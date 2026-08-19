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

// msbBackendSelected reports whether the engine should take the msb path.
func (e *RuntimeEngine) msbBackendSelected() bool {
	return e.cfg != nil && strings.EqualFold(e.cfg.Runtime.Backend, "msb")
}

// msbClipboardHost returns the guest-visible host alias for bridge servers
// under the msb backend; empty means Docker (caller keeps its default).
func msbClipboardHost() string { return "host.microsandbox.internal" }

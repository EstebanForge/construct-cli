package runtime

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	msb "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/EstebanForge/construct-cli/internal/config"
)

func msbLiveEnabled(t *testing.T) {
	t.Helper()
	if os.Getenv("CONSTRUCT_MSB_LIVE") != "1" {
		t.Skip("CONSTRUCT_MSB_LIVE not set; skipping live msb test")
	}
	if _, err := exec.LookPath("msb"); err != nil {
		t.Skipf("msb binary not found: %v", err)
	}
}

// TestMsbLiveVolumesSpecSandboxExec is the Step 6 plumbing-gate: boot a
// construct-box sandbox via the Go SDK and exec inside it. No packages
// volume, no network policy, no host construct home — just the bare
// primitives proven end-to-end.
func TestMsbLiveVolumesSpecSandboxExec(t *testing.T) {
	msbLiveEnabled(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	cfg := config.DefaultConfig()
	cfg.Network.Mode = "permissive"

	if err := EnsureMsbVolumes(ctx); err != nil {
		t.Fatalf("EnsureMsbVolumes: %v", err)
	}

	name := "construct-msb-live-test"
	m := NewMsbBackend()
	if err := m.Cleanup(ctx, name); err != nil {
		t.Fatalf("pre-run Cleanup: %v", err)
	}

	proj := t.TempDir()
	marker := filepath.Join(proj, "marker.txt")
	if err := os.WriteFile(marker, []byte("construct-live"), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	spec := BuildMsbRunSpec(&cfg, name, proj, nil)
	sb, err := CreateMsbSandbox(ctx, spec)
	if err != nil {
		t.Fatalf("CreateMsbSandbox: %v", err)
	}
	defer func() {
		_ = sb.RequestStop(ctx)  //nolint:errcheck // best-effort teardown
		_, _ = sb.WaitUntilStopped(ctx)
	}()

	out, code, err := m.Exec(ctx, ExecOptions{Name: name, Command: []string{"echo", "hi"}})
	if err != nil || code != 0 || !strings.Contains(out, "hi") {
		t.Fatalf("Exec echo: code=%d out=%q err=%v", code, out, err)
	}

	out, code, err = m.Exec(ctx, ExecOptions{Name: name, Command: []string{"sh", "-c", "exit 7"}})
	if err != nil || code != 7 {
		t.Fatalf("exit-code fidelity: code=%d err=%v", code, err)
	}

	out, code, err = m.Exec(ctx, ExecOptions{Name: name, Command: []string{"cat", "/workspace/marker.txt"}})
	if err != nil || code != 0 || !strings.Contains(out, "construct-live") {
		t.Fatalf("Exec cat marker: code=%d out=%q err=%v", code, out, err)
	}

	out, code, err = m.Exec(ctx, ExecOptions{Name: name, User: "construct", Command: []string{"sh", "-c", "echo $PATH"}})
	if err != nil || code != 0 || !strings.Contains(out, "/home/linuxbrew/.linuxbrew/bin") {
		t.Fatalf("Exec PATH: code=%d out=%q err=%v", code, out, err)
	}

	// Default workload runs: entrypoint must be alive in the guest (SDK
	// never auto-runs it at create — CreateMsbSandbox does, in background).
	out, code, err = m.Exec(ctx, ExecOptions{Name: name, Command: []string{"sh", "-c", "ps aux | grep -v grep | grep entrypoint.sh | wc -l"}})
	if err != nil || code != 0 || strings.TrimSpace(out) == "0" {
		t.Fatalf("entrypoint not running in guest: code=%d out=%q err=%v", code, out, err)
	}

	if _, err := m.State(ctx, name); err != nil {
		t.Fatalf("State: %v", err)
	}
	if err := m.Stop(ctx, name); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := m.Cleanup(ctx, name); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
}

// TestMsbLiveAgentInstall is the Step 6 project gate (docs/VMs.md §7):
// runs the first-run install inside the VM and verifies a generated agent
// binary lands on the host.
//
// Scope choice: this gate runs the entrypoint's installer end-to-end with
// a generated install_user_packages.sh, but uses a canned "install one npm
// package into the home" payload instead of the full GenerateInstallScript
// output. Reason: the full packages-generated script contains an
// unguarded `curl -fsSL https://bun.sh/install | bash` plus `set -e`, which
// aborts the script on the first network blip (msb's default egress
// policy makes any intermittent 5xx terminal). The Docker path benefits
// from a pre-warmed construct-packages volume so partial installs are
// retried across runs; the msb one-shot model has no such retry. The full
// install is therefore Step 7's persistent-sandbox retry loop's problem,
// not Step 6's. This gate proves the install PATH works (mounts, network,
// env, entrypoint, npm prefix, host-side persistence) on a tractable
// single-package payload; hardening the full script is tracked separately.
func TestMsbLiveAgentInstall(t *testing.T) {
	msbLiveEnabled(t)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	cfg := config.DefaultConfig()
	if err := EnsureMsbVolumes(ctx); err != nil {
		t.Fatalf("EnsureMsbVolumes: %v", err)
	}

	// Fresh home under ~/tmp (durable: t.TempDir is wiped on failure, but
	// inspecting the home after a timeout is how failures get diagnosed).
	home := filepath.Join(os.Getenv("HOME"), "tmp", "construct-msb-gate-home")
	_ = os.RemoveAll(home) //nolint:errcheck // scratch dir, ours
	// Minimal install script: mirrors the entrypoint's first-run shape
	// (npm prefix, PATH, npm install) without the fragile Bun step.
	script := `#!/bin/bash
set -e
mkdir -p ~/.local/bin ~/.npm-global
npm config set prefix "$HOME/.npm-global"
export PATH="$HOME/.npm-global/bin:$PATH"
npm install -g http-server@14 || echo "npm install failed"
`
	guestContainer := filepath.Join(home, ".config", "construct-cli", "container")
	if err := os.MkdirAll(guestContainer, 0o755); err != nil {
		t.Fatalf("mkdir guest container dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(guestContainer, "install_user_packages.sh"), []byte(script), 0o755); err != nil {
		t.Fatalf("write install script: %v", err)
	}
	if resolved, err := filepath.EvalSymlinks(home); err == nil {
		home = resolved
	}

	name := "construct-msb-install-test"
	m := NewMsbBackend()
	if err := m.Cleanup(ctx, name); err != nil {
		t.Fatalf("pre-run Cleanup: %v", err)
	}

	spec := BuildMsbRunSpec(&cfg, name, "", nil)
	spec.Mounts[msbHomeMountDest] = msb.Mount.Bind(home, msb.MountOptions{})
	spec.Cmd = []string{"echo", "Installation complete"}

	// CreateMsbSandbox blocks through the one-shot default workload (the
	// entrypoint install) and fails on non-zero exit.
	if _, err := CreateMsbSandbox(ctx, spec); err != nil {
		t.Fatalf("CreateMsbSandbox: %v", err)
	}

	// Gate criteria: an npm-installed binary landed in the host home,
	// proving entrypoint + mounts + npm + host-side persistence all work.
	binDir := filepath.Join(home, ".npm-global", "bin")
	entries, err := os.ReadDir(binDir)
	if err != nil || len(entries) == 0 {
		t.Fatalf("no agent binaries in %s (err=%v) — install did not persist to host", binDir, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	t.Logf("installed agents: %v", names)

	if err := m.Cleanup(context.Background(), name); err != nil {
		t.Fatalf("post Cleanup: %v", err)
	}
}

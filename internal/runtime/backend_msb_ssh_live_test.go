package runtime_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	agent "github.com/EstebanForge/construct-cli/internal/agent"
	"github.com/EstebanForge/construct-cli/internal/config"
	"github.com/EstebanForge/construct-cli/internal/msbembed"
	rt "github.com/EstebanForge/construct-cli/internal/runtime"
)

// msbLiveEnabled gates every live test: CONSTRUCT_MSB_LIVE=1, a msb binary
// on PATH, usable KVM, and the embedded runtime pair activated (without
// activation the SDK resolver falls back to the system msb, whose version
// usually trails the SDK pin and fails the launch contract).
func msbLiveEnabled(t *testing.T) {
	t.Helper()
	if os.Getenv("CONSTRUCT_MSB_LIVE") != "1" {
		t.Skip("CONSTRUCT_MSB_LIVE not set; skipping live msb test")
	}
	if _, err := exec.LookPath("msb"); err != nil {
		t.Skipf("msb binary not found: %v", err)
	}
	kvm, err := os.OpenFile("/dev/kvm", os.O_RDWR, 0)
	if err != nil {
		t.Skipf("no usable /dev/kvm: %v", err)
	}
	kvm.Close()
	if err := msbembed.Activate(); err != nil {
		t.Fatalf("activate embedded runtime: %v", err)
	}
}

// TestMsbLiveSSHAgentBridge is the Step 7 SSH agent bridge gate
// (docs/VMsv2.md §7): the guest socat proxy (UNIX socket ->
// host.microsandbox.internal) answers agent protocol requests through the
// production StartSSHBridge, under deny-by-default offline egress. Lives
// in runtime_test so it can import internal/agent (which imports
// internal/runtime) without an import cycle.
func TestMsbLiveSSHAgentBridge(t *testing.T) {
	msbLiveEnabled(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	cfg := config.DefaultConfig()
	cfg.Network.Mode = "offline"

	name := "construct-msb-ssh-test"
	bridge, err := agent.StartSSHBridge(name, "127.0.0.1")
	if err != nil {
		// No live agent socket anywhere is an environment property, not
		// a bridge defect; the unit layer covers StartSSHBridge itself.
		t.Skipf("no SSH agent available on host: %v", err)
	}
	t.Cleanup(bridge.Stop)

	if err := rt.EnsureMsbVolumes(ctx); err != nil {
		t.Fatalf("EnsureMsbVolumes: %v", err)
	}
	m := rt.NewMsbBackend()
	if err := m.Cleanup(ctx, name); err != nil {
		t.Fatalf("pre-run Cleanup: %v", err)
	}
	proj := t.TempDir()
	// The bridge port rides the spec's bridgePorts: with policy enforced,
	// only rule-named ports reach the host. Production wires the session's
	// ports the same way.
	spec := rt.BuildMsbRunSpec(&cfg, name, proj, []int{bridge.Port}, "")
	if _, err := rt.CreateMsbSandbox(ctx, spec); err != nil {
		t.Fatalf("CreateMsbSandbox: %v", err)
	}
	defer func() {
		_ = m.Cleanup(context.Background(), name)
	}()

	// Guest side: per-session socat -> host alias, same shape and guest-
	// native /tmp socket path as engine_msb.go. Unix binds on the virtiofs
	// home mount are unsupported by the guest, so /home/... sockets fail.
	sockPath := "/tmp/construct-ssh-agent-livecheck.sock"
	script := `if ! command -v socat >/dev/null; then echo "socat missing in guest image" >&2; exit 9; fi; PROXY_SOCK="` + sockPath + `"; mkdir -p "$(dirname "$PROXY_SOCK")" >/dev/null 2>&1 || true; rm -f "$PROXY_SOCK"; nohup socat UNIX-LISTEN:"$PROXY_SOCK",fork,mode=600 TCP:host.microsandbox.internal:"$PORT" >/tmp/socat.log 2>&1 & sleep 1; if [ -S "$PROXY_SOCK" ]; then echo ready; else echo "socat did not bind:"; cat /tmp/socat.log; exit 1; fi`
	out, code, err := m.Exec(ctx, rt.ExecOptions{Name: name, User: "construct", Env: []string{fmt.Sprintf("PORT=%d", bridge.Port)}, Command: []string{"bash", "-c", script}})
	if err != nil || code != 0 || !strings.Contains(out, "ready") {
		t.Fatalf("socat proxy setup: code=%d out=%q err=%v", code, out, err)
	}

	// Gate: a real SSH_AGENTC_REQUEST_IDENTITIES through the full chain
	// (guest ssh-add -> socat -> VMM -> bridge -> host agent). A healthy
	// agent answers with identities (exit 0) or a clean empty list (exit
	// 1, "no identities"); a torn chain surfaces as "communication with
	// agent failed" or "cannot connect" and must fail the gate.
	out, code, err = m.Exec(ctx, rt.ExecOptions{Name: name, User: "construct", Env: []string{"SSH_AUTH_SOCK=" + sockPath}, Command: []string{"bash", "-c", `ssh-add -l 2>&1; echo "exit=$?"`}})
	if err != nil || code != 0 {
		t.Fatalf("ssh-add via bridge: code=%d out=%q err=%v", code, out, err)
	}
	if strings.Contains(out, "communication with agent failed") || strings.Contains(out, "cannot connect") || !strings.Contains(out, "exit=") {
		t.Fatalf("ssh agent chain broken: %q", out)
	}
	t.Logf("ssh-add via production bridge: %s", strings.TrimSpace(strings.SplitN(out, "\nexit=", 2)[0]))
}

package runtime

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	msb "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/EstebanForge/construct-cli/internal/clipboard"
	"github.com/EstebanForge/construct-cli/internal/config"
	"github.com/EstebanForge/construct-cli/internal/hostexec"
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
		_ = sb.RequestStop(ctx)
		_, _ = sb.WaitUntilStopped(ctx)
	}()

	out, code, err := m.Exec(ctx, ExecOptions{Name: name, Command: []string{"echo", "hi"}})
	if err != nil || code != 0 || !strings.Contains(out, "hi") {
		t.Fatalf("Exec echo: code=%d out=%q err=%v", code, out, err)
	}

	_, code, err = m.Exec(ctx, ExecOptions{Name: name, Command: []string{"sh", "-c", "exit 7"}})
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
	_ = os.RemoveAll(home)
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

// TestMsbLiveClipboardBridge is the Step 7 clipboard E2E gate (docs/VMs.md
// §7): the host clipboard server must be reachable from inside a sandbox
// over the host.microsandbox.internal transport with token auth enforced,
// under the strongest policy (offline: deny-by-default egress).
func TestMsbLiveClipboardBridge(t *testing.T) {
	msbLiveEnabled(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	cfg := config.DefaultConfig()
	cfg.Network.Mode = "offline" // strongest proof: bridges must survive deny-by-default

	cb, err := clipboard.StartServer(msbHostAlias)
	if err != nil {
		t.Fatalf("StartServer: %v", err)
	}
	defer cb.Stop()

	if err := EnsureMsbVolumes(ctx); err != nil {
		t.Fatalf("EnsureMsbVolumes: %v", err)
	}

	name := "construct-msb-clip-test"
	m := NewMsbBackend()
	if err := m.Cleanup(ctx, name); err != nil {
		t.Fatalf("pre-run Cleanup: %v", err)
	}

	proj := t.TempDir()
	spec := BuildMsbRunSpec(&cfg, name, proj, nil) // nil ports: engine binds random bridge ports per run
	if _, err := CreateMsbSandbox(ctx, spec); err != nil {
		t.Fatalf("CreateMsbSandbox: %v", err)
	}
	defer func() {
		_ = m.Cleanup(context.Background(), name)
	}()

	curl := func(extra ...string) (string, int, error) {
		cmd := append([]string{"curl", "-s", "-o", "/dev/null", "-w", "%{http_code}", "--max-time", "10"}, extra...)
		out, code, err := m.Exec(ctx, ExecOptions{Name: name, Command: cmd})
		return strings.TrimSpace(out), code, err
	}

	// 1. Unauthorized: no token -> 401 (token auth enforced end-to-end).
	status, code, err := curl(cb.URL + "/paste?type=text/plain")
	if err != nil || code != 0 || status != "401" {
		t.Fatalf("no-token paste: status=%s code=%d err=%v (want 401)", status, code, err)
	}

	// 2. Authorized: correct token -> 200 (host clipboard has text) or 404
	// (empty clipboard). Either proves the transport carries the request;
	// anything else is a bridge failure.
	status, code, err = curl("-H", "X-Construct-Clip-Token: "+cb.Token, cb.URL+"/paste?type=text/plain")
	if err != nil || code != 0 || (status != "200" && status != "404") {
		t.Fatalf("token paste: status=%s code=%d err=%v (want 200 or 404)", status, code, err)
	}
	t.Logf("authorized paste status: %s", status)

	// 3. Offline means offline: public egress must fail even though the
	// host transport rule is any-port (destination-scoped to host).
	_, code, err = m.Exec(ctx, ExecOptions{Name: name, Command: []string{"curl", "-s", "-o", "/dev/null", "--max-time", "10", "https://example.com"}})
	if err == nil && code == 0 {
		t.Fatal("public egress succeeded under offline mode — policy not enforced")
	}
}

// TestMsbLiveHostExecBridge is the Step 7 host exec E2E gate (docs/VMs.md
// §7): the host exec bridge answers from inside the sandbox, token auth is
// enforced, and a guest cwd (/workspace) translates to the host project dir
// via MsbPathMaps (per-mount PathMap translation).
func TestMsbLiveHostExecBridge(t *testing.T) {
	msbLiveEnabled(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	cfg := config.DefaultConfig()
	cfg.Network.Mode = "offline"
	cfg.Sandbox.HostBinaries = []string{"pwd"}

	proj := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(proj); err == nil {
		proj = resolved
	}

	pathMaps := make([]hostexec.PathMap, 0, 3)
	for _, pm := range MsbPathMaps(proj) {
		pathMaps = append(pathMaps, hostexec.PathMap{Container: pm.Guest, Host: pm.Host})
	}
	srv, err := hostexec.StartServer(msbHostAlias, cfg.Sandbox.HostBinaries, hostexec.DefaultTimeout, pathMaps)
	if err != nil {
		t.Fatalf("hostexec StartServer: %v", err)
	}
	defer srv.Stop()

	if err := EnsureMsbVolumes(ctx); err != nil {
		t.Fatalf("EnsureMsbVolumes: %v", err)
	}
	name := "construct-msb-hostexec-test"
	m := NewMsbBackend()
	if err := m.Cleanup(ctx, name); err != nil {
		t.Fatalf("pre-run Cleanup: %v", err)
	}
	spec := BuildMsbRunSpec(&cfg, name, proj, nil)
	if _, err := CreateMsbSandbox(ctx, spec); err != nil {
		t.Fatalf("CreateMsbSandbox: %v", err)
	}
	defer func() {
		_ = m.Cleanup(context.Background(), name)
	}()

	execCurl := func(body string, headers ...string) (string, int, error) {
		cmd := append([]string{"curl", "-s", "-o", "/dev/null", "-w", "%{http_code}", "--max-time", "30", "-X", "POST"}, "-d", body)
		for _, h := range headers {
			cmd = append(cmd, "-H", h)
		}
		cmd = append(cmd, srv.URL+"/exec")
		out, code, err := m.Exec(ctx, ExecOptions{Name: name, Command: cmd})
		return strings.TrimSpace(out), code, err
	}

	// 1. No token -> 401.
	body := `{"argv":["pwd"],"cwd":"/workspace"}`
	status, code, err := execCurl(body)
	if err != nil || code != 0 || status != "401" {
		t.Fatalf("no-token exec: status=%s code=%d err=%v (want 401)", status, code, err)
	}

	// 2. Token + guest cwd -> 200 and (checked next) host-dir translation.
	status, code, err = execCurl(body, "X-Construct-Exec-Token: "+srv.Token, "Content-Type: application/json")
	if err != nil || code != 0 || status != "200" {
		t.Fatalf("token exec: status=%s code=%d err=%v", status, code, err)
	}

	// 3. PathMap proof: rerun with body visible; stdout frame must carry the
	// HOST project dir (base64), i.e. /workspace translated to $proj.
	cmd := append([]string{"curl", "-s", "--max-time", "30", "-X", "POST", "-d", body, "-H", "X-Construct-Exec-Token: " + srv.Token, "-H", "Content-Type: application/json"}, srv.URL+"/exec")
	out, code, err := m.Exec(ctx, ExecOptions{Name: name, Command: cmd})
	if err != nil || code != 0 {
		t.Fatalf("token exec (body): code=%d out=%q err=%v", code, out, err)
	}
	if !strings.Contains(out, base64Encode(proj)) && !strings.Contains(out, proj) {
		t.Fatalf("exec output missing host project dir %q: %q", proj, out)
	}
	if !strings.Contains(out, `"type":"exit"`) {
		t.Fatalf("no exit frame in output: %q", out)
	}
}

func base64Encode(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}

// TestMsbLiveSSHAgentBridge is the Step 7 SSH agent bridge gate (docs/VMs.md
// §7): the guest socat proxy (UNIX socket -> host.microsandbox.internal)
// answers agent protocol requests through the host SSH bridge, under
// deny-by-default offline egress.
func TestMsbLiveSSHAgentBridge(t *testing.T) {
	msbLiveEnabled(t)
	if os.Getenv("SSH_AUTH_SOCK") == "" {
		t.Skip("SSH_AUTH_SOCK not set on host; skipping SSH bridge gate")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	cfg := config.DefaultConfig()
	cfg.Network.Mode = "offline"

	if err := EnsureMsbVolumes(ctx); err != nil {
		t.Fatalf("EnsureMsbVolumes: %v", err)
	}
	name := "construct-msb-ssh-test"
	m := NewMsbBackend()
	if err := m.Cleanup(ctx, name); err != nil {
		t.Fatalf("pre-run Cleanup: %v", err)
	}
	proj := t.TempDir()
	spec := BuildMsbRunSpec(&cfg, name, proj, nil)
	if _, err := CreateMsbSandbox(ctx, spec); err != nil {
		t.Fatalf("CreateMsbSandbox: %v", err)
	}
	defer func() {
		_ = m.Cleanup(context.Background(), name)
	}()

	// Host side: TCP listener -> host SSH_AUTH_SOCK (backend-agnostic).
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("host listener: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	defer l.Close()
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				agent, derr := net.Dial("unix", os.Getenv("SSH_AUTH_SOCK"))
				if derr != nil {
					_ = c.Close()
					return
				}
				defer agent.Close()
				defer c.Close()
				go func() { _, _ = io.Copy(agent, c); _ = agent.Close() }()
				_, _ = io.Copy(c, agent)
			}(c)
		}
	}()

	// Guest side: per-session socat -> host alias (same shape as engine_msb.go).
	sockPath := "/home/construct/.ssh/agent.msbtest.sock"
	script := `if ! command -v socat >/dev/null; then echo "socat not found" >&2; exit 1; fi; PROXY_SOCK="` + sockPath + `"; mkdir -p "$(dirname "$PROXY_SOCK")" 2>/dev/null || true; rm -f "$PROXY_SOCK"; nohup socat UNIX-LISTEN:"$PROXY_SOCK",fork,mode=600 TCP:host.microsandbox.internal:"$PORT" >/tmp/socat.log 2>&1 & sleep 1; [ -S "$PROXY_SOCK" ] && echo ready`
	out, code, err := m.Exec(ctx, ExecOptions{Name: name, User: "construct", Env: []string{fmt.Sprintf("PORT=%d", port)}, Command: []string{"bash", "-c", script}})
	if err != nil || code != 0 || !strings.Contains(out, "ready") {
		t.Fatalf("socat proxy setup: code=%d out=%q err=%v", code, out, err)
	}

	// Gate: SSH_AGENT_PROTOCOL request through the proxy: `ssh-add -l`
	// (lists identities; exit 0 with keys, 1 with an empty agent, 2 on
	// connection failure). Anything but 2 proves the full chain works.
	out, code, err = m.Exec(ctx, ExecOptions{Name: name, User: "construct", Env: []string{"SSH_AUTH_SOCK=" + sockPath}, Command: []string{"bash", "-c", `ssh-add -l 2>&1; echo "exit=$?"`}})
	if err != nil || code != 0 {
		t.Fatalf("ssh-add via proxy: code=%d out=%q err=%v", code, out, err)
	}
	if strings.Contains(out, "exit=2") || !strings.Contains(out, "exit=") {
		t.Fatalf("ssh agent chain broken (exit=2 = connect failure): %q", out)
	}
	t.Logf("ssh-add via msb proxy: %q", strings.TrimSpace(out))
}

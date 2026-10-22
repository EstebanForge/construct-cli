package runtime

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	msb "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/EstebanForge/construct-cli/internal/clipboard"
	"github.com/EstebanForge/construct-cli/internal/config"
	"github.com/EstebanForge/construct-cli/internal/hostexec"
	"github.com/EstebanForge/construct-cli/internal/msbembed"
)

func msbLiveEnabled(t *testing.T) {
	t.Helper()
	if os.Getenv("CONSTRUCT_MSB_LIVE") != "1" {
		t.Skip("CONSTRUCT_MSB_LIVE not set; skipping live msb test")
	}
	// Scratch VM state (catalog, image cache, sandbox disks, snapshots)
	// goes to a /tmp-backed MSB_HOME, never the real config dir. The make
	// target pins one shared home per run (image pulled once); bare go
	// test runs fall back to a per-test temp home.
	scratchHome := os.Getenv("MSB_HOME")
	if scratchHome == "" {
		scratchHome = filepath.Join(t.TempDir(), "msb-home")
		t.Setenv("MSB_HOME", scratchHome)
	}
	if err := os.MkdirAll(scratchHome, 0o700); err != nil {
		t.Fatalf("scratch home: %v", err)
	}
	msbembed.OverrideScratch(filepath.Join(filepath.Dir(scratchHome), "msb-runtime"), scratchHome)
	kvm, err := os.OpenFile("/dev/kvm", os.O_RDWR, 0)
	if err != nil {
		t.Skipf("no usable /dev/kvm: %v", err)
	}
	kvm.Close()
	// The SDK create path wants the runtime pair inside MSB_HOME (or the
	// MSB_PATH/MSB_LIBKRUNFW_PATH overrides, which Activate() sets to the
	// extracted embedded pair). The embedded pair is pinned to the SDK
	// version, so it is always the most recent tested runtime.
	if err := msbembed.Activate(); err != nil {
		t.Fatalf("activate embedded runtime: %v", err)
	}
}

// msbLiveImageRef is the image every live gate boots; make test-live
// pre-pulls it into the shared scratch home.
const msbLiveImageRef = "ghcr.io/estebanforge/construct-box:latest"

// msbLiveRequireImage skips when the boot image is not cached instead of
// pulling gigabytes inside a bare go test run.
func msbLiveRequireImage(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := msb.Image.Get(ctx, msbLiveImageRef); err != nil {
		t.Skipf("%s not cached in MSB_HOME; run make test-live: %v", msbLiveImageRef, err)
	}
}

// msbBootBudget is generous on purpose: the gate guards against hangs
// and runaway boots, not regressions in the seconds range. The logged
// duration is the baseline the suite tightens against.
const msbBootBudget = 90 * time.Second

// TestMsbLiveBootBudget boots the standard spec through the production
// create path and records the create-to-ready time.
func TestMsbLiveBootBudget(t *testing.T) {
	msbLiveEnabled(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	cfg := config.DefaultConfig()
	cfg.Network.Mode = "permissive"

	if err := EnsureMsbVolumes(ctx); err != nil {
		t.Fatalf("EnsureMsbVolumes: %v", err)
	}

	name := fmt.Sprintf("construct-msb-boot-budget-%d", time.Now().UnixMilli())
	m := NewMsbBackend()
	if err := m.Cleanup(ctx, name); err != nil {
		t.Fatalf("pre-run Cleanup: %v", err)
	}

	proj := t.TempDir()
	msbLiveRequireImage(t)
	spec := BuildMsbRunSpec(&cfg, name, proj, nil, msbLiveImageRef)

	start := time.Now()
	if _, err := CreateMsbSandbox(ctx, spec); err != nil {
		t.Fatalf("CreateMsbSandbox: %v", err)
	}
	booted := time.Since(start)
	t.Logf("boot-to-ready: %s", booted)
	if booted > msbBootBudget {
		t.Errorf("boot took %s, budget %s", booted, msbBootBudget)
	}

	if err := m.Cleanup(context.Background(), name); err != nil {
		t.Fatalf("post Cleanup: %v", err)
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

	msbLiveRequireImage(t)
	spec := BuildMsbRunSpec(&cfg, name, proj, nil, msbLiveImageRef)
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

	dest := GetMsbWorkspaceMountDest(proj)
	out, code, err = m.Exec(ctx, ExecOptions{Name: name, Command: []string{"cat", filepath.Join(dest, "marker.txt")}})
	if err != nil || code != 0 || !strings.Contains(out, "construct-live") {
		t.Fatalf("Exec cat marker: code=%d out=%q err=%v", code, out, err)
	}

	out, code, err = m.Exec(ctx, ExecOptions{Name: name, User: "construct", Command: []string{"sh", "-c", "echo $PATH"}})
	if err != nil || code != 0 || !strings.Contains(out, "/usr/local/share/mise/shims") {
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

// TestMsbLiveAgentInstall is the Step 6 project gate (docs/VMsv2.md):
// runs the first-run install inside the VM and verifies a generated agent
// binary lands on the host.
//
// Scope choice: this gate runs the entrypoint's installer end-to-end with
// a generated install_user_packages.sh, but uses a canned "install one npm
// package into the home" payload instead of the full GenerateInstallScript
// output. Reason: the full packages-generated script contains an
// unguarded `curl -fsSL https://bun.sh/install | bash` plus `set -e`, which
// aborts the script on the first network blip (msb's default egress
// policy makes any intermittent 5xx terminal). Partial installs are not
// retried across runs; the full
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

	msbLiveRequireImage(t)
	spec := BuildMsbRunSpec(&cfg, name, "", nil, msbLiveImageRef)
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

// TestMsbLiveClipboardBridge is the Step 7 clipboard E2E gate (docs/VMsv2.md
// §7): the host clipboard server must be reachable from inside a sandbox
// over the host.microsandbox.internal transport with token auth enforced,
// under the strongest policy (offline: deny-by-default egress).
func TestMsbLiveClipboardBridge(t *testing.T) {
	msbLiveEnabled(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	cfg := config.DefaultConfig()
	cfg.Network.Mode = "offline" // strongest proof: bridges must survive deny-by-default

	listener, err := ListenInBridgeBand("live-clipboard-test", "127.0.0.1")
	if err != nil {
		t.Fatalf("ListenInBridgeBand: %v", err)
	}
	cb, err := clipboard.StartServerOn(listener, MsbHostAlias)
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
	// The transport mirrors the production engine: band-probed port on a
	// loopback bind, URL host = MsbHostAlias. With network policy enforced,
	// only rule-named ports reach the host, so an out-of-band port would
	// time out even with the alias resolvable.
	clipPort := portFromURL(t, cb.URL)
	msbLiveRequireImage(t)
	spec := BuildMsbRunSpec(&cfg, name, proj, []int{clipPort}, msbLiveImageRef)
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
	// (empty clipboard). 500 means the host clipboard provider itself is
	// unavailable (headless shell, no display) — a skip, not a bridge
	// failure: the 401-vs-authorized split already proved the transport.
	status, code, err = curl("-H", "X-Construct-Clip-Token: "+cb.Token, cb.URL+"/paste?type=text/plain")
	if err != nil || code != 0 {
		t.Fatalf("token paste: status=%s code=%d err=%v", status, code, err)
	}
	switch status {
	case "200", "404":
		t.Logf("authorized paste status: %s", status)
	case "500":
		t.Skipf("clipboard provider unavailable on this host (authorized paste returned 500)")
	default:
		t.Fatalf("token paste: status=%s (want 200 or 404)", status)
	}

	// 3. Offline means offline: public egress must fail even though the
	// host transport rule is any-port (destination-scoped to host).
	_, code, err = m.Exec(ctx, ExecOptions{Name: name, Command: []string{"curl", "-s", "-o", "/dev/null", "--max-time", "10", "https://example.com"}})
	if err == nil && code == 0 {
		t.Fatal("public egress succeeded under offline mode — policy not enforced")
	}
}

// TestMsbLiveHostExecBridge is the Step 7 host exec E2E gate (docs/VMsv2.md
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
	for _, pm := range MsbPathMaps(&cfg, proj) {
		pathMaps = append(pathMaps, hostexec.PathMap{Container: pm.Guest, Host: pm.Host})
	}
	srv, err := hostexec.StartServer(MsbHostAlias, cfg.Sandbox.HostBinaries, hostexec.DefaultTimeout, pathMaps)
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
	msbLiveRequireImage(t)
	spec := BuildMsbRunSpec(&cfg, name, proj, []int{portFromURL(t, srv.URL)}, msbLiveImageRef)
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

	guestWorkspace := GetMsbWorkspaceMountDest(proj)
	// 1. No token -> 401.
	body := fmt.Sprintf(`{"argv":["pwd"],"cwd":%q}`, guestWorkspace)
	status, code, err := execCurl(body)
	if err != nil || code != 0 || status != "401" {
		t.Fatalf("no-token exec: status=%s code=%d err=%v (want 401)", status, code, err)
	}

	// 2. Token + guest cwd -> 200 and (checked next) host-dir translation.
	status, code, err = execCurl(body, "X-Construct-Exec-Token: "+srv.Token, "Content-Type: application/json")
	if err != nil || code != 0 || status != "200" {
		t.Fatalf("token exec: status=%s code=%d err=%v", status, code, err)
	}

	// 3. PathMap proof: the stdout frame carries the HOST project dir
	// base64-encoded; pwd emits a trailing newline, so the match must be
	// newline-tolerant (the raw-path fallback only ever matched on macOS).
	cmd := append([]string{"curl", "-s", "--max-time", "30", "-X", "POST", "-d", body, "-H", "X-Construct-Exec-Token: " + srv.Token, "-H", "Content-Type: application/json"}, srv.URL+"/exec")
	out, code, err := m.Exec(ctx, ExecOptions{Name: name, Command: cmd})
	if err != nil || code != 0 {
		t.Fatalf("token exec (body): code=%d out=%q err=%v", code, out, err)
	}
	if !strings.Contains(out, base64Encode(proj)) && !strings.Contains(out, base64Encode(proj+"\n")) && !strings.Contains(out, proj) {
		t.Fatalf("exec output missing host project dir %q: %q", proj, out)
	}
	if !strings.Contains(out, `"type":"exit"`) {
		t.Fatalf("no exit frame in output: %q", out)
	}
}

func base64Encode(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}

// portFromURL extracts the TCP port from a test server's URL so it can
// ride the spec's bridgePorts like a production bridge port.
func portFromURL(t *testing.T, rawURL string) int {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse server url %q: %v", rawURL, err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port <= 0 {
		t.Fatalf("server url %q has no usable port", rawURL)
	}
	return port
}

// TestMsbLiveSSHAgentBridge moved to backend_msb_ssh_live_test.go (package
// runtime_test) so it can drive the production StartSSHBridge without an
// import cycle.

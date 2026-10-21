package agent

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/EstebanForge/construct-cli/internal/config"
)

func TestSSHBridgePortForSeed(t *testing.T) {
	// Deterministic: same seed -> same port.
	p1 := sshBridgePortForSeed("construct-cli-deadbeef")
	p2 := sshBridgePortForSeed("construct-cli-deadbeef")
	if p1 != p2 {
		t.Fatalf("deterministic port changed for same seed: %d vs %d", p1, p2)
	}

	// Stable across the box lifecycle: the entrypoint bakes this port at
	// container creation, so it must not vary between invocations.
	for i := 0; i < 10; i++ {
		if got := sshBridgePortForSeed("construct-cli-deadbeef"); got != p1 {
			t.Fatalf("port drifted across calls: %d vs %d", got, p1)
		}
	}

	// Within the advertised band.
	if p1 < sshBridgePortBase || p1 >= sshBridgePortBase+sshBridgePortSpan {
		t.Fatalf("port %d outside band [%d,%d)", p1, sshBridgePortBase, sshBridgePortBase+sshBridgePortSpan)
	}

	// Different boxes should not trivially collide on the same port.
	seen := make(map[int]string)
	collisions := 0
	for _, seed := range []string{
		"construct-cli-aaaaaaaa",
		"construct-cli-bbbbbbbb",
		"construct-cli-cccccccc",
		"construct-cli-dddddddd",
		"construct-cli-eeeeeeee",
		"construct-cli-ffffffff",
		"construct-cli-11111111",
		"construct-cli-22222222",
	} {
		port := sshBridgePortForSeed(seed)
		if other, dup := seen[port]; dup {
			t.Logf("collision: %q and %q -> %d (acceptable within a %d-wide band)", seed, other, port, sshBridgePortSpan)
			collisions++
		}
		seen[port] = seed
	}
	// With an 8-sample draw from a 10k-wide band, expecting near-zero collisions.
	if collisions > 1 {
		t.Fatalf("too many port collisions (%d) for 8 distinct seeds", collisions)
	}
}

// TestSSHPinIdentitiesEnv locks the config->env serialization that feeds the
// entrypoint's per-host identity pinning, including the malformed-entry guards.
func TestSSHPinIdentitiesEnv(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want string
	}{
		{"nil", nil, ""},
		{"empty", []string{}, ""},
		{"single", []string{"github.com=github"}, "github.com=github"},
		{"multiple hosts", []string{"github.com=github", "gitlab.com=work"}, "github.com=github,gitlab.com=work"},
		{"alias three-field", []string{"github-work=github.com=work"}, "github-work=github.com=work"},
		{"drops entry without equals", []string{"github.com=github", "bogus"}, "github.com=github"},
		{"drops entry with embedded comma", []string{"a=b,c=d"}, ""},
		{"trims surrounding whitespace", []string{"  github.com=github  "}, "github.com=github"},
		{"drops blank entries", []string{"", "github.com=github", "   "}, "github.com=github"},
		{"preserves order", []string{"b.com=b", "a.com=a"}, "b.com=b,a.com=a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{Sandbox: config.SandboxConfig{SSHPinIdentities: tt.in}}
			if got := sshPinIdentitiesEnv(cfg); got != tt.want {
				t.Fatalf("sshPinIdentitiesEnv(%v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}

	// nil config must not panic and must yield empty.
	if got := sshPinIdentitiesEnv(nil); got != "" {
		t.Fatalf("sshPinIdentitiesEnv(nil) = %q, want empty", got)
	}
}

// TestSSHProxySockForPID guards the per-session socket naming that isolates
// concurrent sessions sharing one daemon (cause #3 regression).
func TestSSHProxySockForPID(t *testing.T) {
	a := sshProxySockForPID(100)
	b := sshProxySockForPID(200)

	if a == b {
		t.Fatalf("distinct PIDs produced the same socket: %s", a)
	}
	if again := sshProxySockForPID(100); again != a {
		t.Fatalf("same PID produced different sockets: %s vs %s", a, again)
	}
	if want := "/home/construct/.ssh/agent.100.sock"; a != want {
		t.Fatalf("sshProxySockForPID(100) = %q, want %q", a, want)
	}
}

// TestBridgeBindHostBackendScoping: msb's VMM proxies allowed guest→host
// ports to host loopback (live-verified 2026-10), so msb bridges bind
// 127.0.0.1; docker's Linux host-gateway is the bridge IP, which needs a
// 0.0.0.0 bind.
func TestBridgeBindHostBackendScoping(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("backend scoping only diverges on Linux")
	}
	if got := bridgeBindHost(config.BackendMsb); got != "127.0.0.1" {
		t.Errorf("msb bind host = %q, want loopback", got)
	}
	for _, b := range []string{config.BackendContainers, config.BackendDocker, config.BackendPodman, ""} {
		if got := bridgeBindHost(b); got != "0.0.0.0" {
			t.Errorf("%q bind host = %q, want 0.0.0.0 (host-gateway is the bridge IP)", b, got)
		}
	}
}

func TestStartSSHBridgeRequiresAuthSock(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "")
	// Isolate HOME: a live ~/.ssh/agent.sock (bw-ssh bridge) would let the
	// bridge start via the fallback path and legitimately return no error.
	t.Setenv("HOME", t.TempDir())
	if _, err := StartSSHBridge("seed-noauth", "127.0.0.1"); err == nil {
		t.Fatal("expected error when no agent socket is reachable")
	}
}

// TestResolveAgentSocket pins the probe order: a live SSH_AUTH_SOCK wins,
// a dead one falls through to the bw-ssh bridge socket under $HOME, and
// absence everywhere yields an empty string.
func TestResolveAgentSocket(t *testing.T) {
	t.Run("live env socket wins", func(t *testing.T) {
		live := filepath.Join(shortTempDir(t), "live.sock")
		stop := startFakeAgent(t, live, "LIVE")
		defer stop()
		t.Setenv("SSH_AUTH_SOCK", live)
		t.Setenv("HOME", t.TempDir())
		if got := resolveAgentSocket(); got != live {
			t.Fatalf("resolveAgentSocket() = %q, want %q", got, live)
		}
	})

	t.Run("dead env socket falls back to bridge socket", func(t *testing.T) {
		dead := filepath.Join(shortTempDir(t), "dead.sock")
		home := t.TempDir()
		fb := filepath.Join(home, ".ssh", "agent.sock")
		if err := os.MkdirAll(filepath.Dir(fb), 0o700); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		stop := startFakeAgent(t, fb, "FALLBACK")
		defer stop()
		t.Setenv("SSH_AUTH_SOCK", dead)
		t.Setenv("HOME", home)
		if got := resolveAgentSocket(); got != fb {
			t.Fatalf("resolveAgentSocket() = %q, want %q", got, fb)
		}
	})

	t.Run("no live socket anywhere", func(t *testing.T) {
		t.Setenv("SSH_AUTH_SOCK", filepath.Join(shortTempDir(t), "dead.sock"))
		t.Setenv("HOME", t.TempDir())
		if got := resolveAgentSocket(); got != "" {
			t.Fatalf("resolveAgentSocket() = %q, want empty", got)
		}
	})
}

// TestStartSSHBridgeRelayFallsBackToBridgeSocket proves the end-to-end
// heal: with SSH_AUTH_SOCK pointing at a refusing socket (the Bitwarden
// desktop Flatpak exports its socket path globally, dead when the app is
// closed), the bridge relays through ~/.ssh/agent.sock instead.
func TestStartSSHBridgeRelayFallsBackToBridgeSocket(t *testing.T) {
	home := t.TempDir()
	fb := filepath.Join(home, ".ssh", "agent.sock")
	if err := os.MkdirAll(filepath.Dir(fb), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	stop := startFakeAgent(t, fb, "FALLBACK")
	defer stop()

	t.Setenv("HOME", home)
	t.Setenv("SSH_AUTH_SOCK", filepath.Join(shortTempDir(t), "dead.sock"))

	bridge, err := StartSSHBridge("seed-fallback", "127.0.0.1")
	if err != nil {
		t.Fatalf("StartSSHBridge: %v", err)
	}
	t.Cleanup(bridge.Stop)

	if got := dialAndRead(t, bridge.Port); got != "FALLBACK" {
		t.Fatalf("relayed payload = %q, want FALLBACK via ~/.ssh/agent.sock", got)
	}
}

// TestSSHBridgeProxiesToAgent exercises the full host bridge: a TCP listener
// proxying bytes to the unix-domain agent socket in SSH_AUTH_SOCK.
func TestSSHBridgeProxiesToAgent(t *testing.T) {
	dir := shortTempDir(t)
	sock := filepath.Join(dir, "a.sock")
	stop := startFakeAgent(t, sock, "AGENT1")
	defer stop()

	t.Setenv("SSH_AUTH_SOCK", sock)
	bridge, err := StartSSHBridge("seed-proxy", "127.0.0.1")
	if err != nil {
		t.Fatalf("StartSSHBridge: %v", err)
	}
	t.Cleanup(bridge.Stop)

	if got := dialAndRead(t, bridge.Port); got != "AGENT1" {
		t.Fatalf("proxied payload = %q, want AGENT1", got)
	}
}

// TestSSHBridgeRereadsAuthSockPerConnection covers the Bitwarden case: the agent
// socket path is re-read on each connection, so a recycled socket (vault
// lock/unlock) is picked up without restarting the bridge.
func TestSSHBridgeRereadsAuthSockPerConnection(t *testing.T) {
	dir := shortTempDir(t)
	s1 := filepath.Join(dir, "1.sock")
	s2 := filepath.Join(dir, "2.sock")
	stop1 := startFakeAgent(t, s1, "ONE")
	defer stop1()
	stop2 := startFakeAgent(t, s2, "TWO")
	defer stop2()

	t.Setenv("SSH_AUTH_SOCK", s1)
	bridge, err := StartSSHBridge("seed-reread", "127.0.0.1")
	if err != nil {
		t.Fatalf("StartSSHBridge: %v", err)
	}
	t.Cleanup(bridge.Stop)

	// First connection resolves to the original socket.
	if got := dialAndRead(t, bridge.Port); got != "ONE" {
		t.Fatalf("first connection = %q, want ONE", got)
	}

	// Point SSH_AUTH_SOCK at a fresh socket; the next connection must follow it.
	// (Connection above is fully closed, so there is no concurrent env access.)
	t.Setenv("SSH_AUTH_SOCK", s2)
	if got := dialAndRead(t, bridge.Port); got != "TWO" {
		t.Fatalf("second connection = %q, want TWO", got)
	}
}

// shortTempDir returns a temp dir short enough for a unix socket path, which is
// capped at ~104 bytes on macOS (t.TempDir's nested names can overflow it).
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "sshb")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// startFakeAgent listens on a unix socket and writes id to every connection,
// standing in for an SSH agent. Returns a stop func.
func startFakeAgent(t *testing.T, sockPath, id string) func() {
	t.Helper()
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen unix %s: %v", sockPath, err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				_, _ = c.Write([]byte(id))
				_ = c.Close()
			}(conn)
		}
	}()
	return func() { _ = ln.Close() }
}

// dialAndRead connects to the bridge's TCP port and returns the bytes the agent
// sent back through it.
func dialAndRead(t *testing.T, port int) string {
	t.Helper()
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 2*time.Second)
	if err != nil {
		t.Fatalf("dial bridge: %v", err)
	}
	defer func() { _ = conn.Close() }()

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 64)
	n, err := conn.Read(buf)
	if n == 0 && err != nil {
		t.Fatalf("read from bridge: %v", err)
	}
	return string(buf[:n])
}

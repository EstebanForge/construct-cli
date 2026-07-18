package agent

import (
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestHerdrBridgePortForSeed_StableAndBounded(t *testing.T) {
	a := herdrBridgePortForSeed("construct-box-myproject")
	b := herdrBridgePortForSeed("construct-box-myproject")
	if a != b {
		t.Fatalf("port not stable for same seed: %d != %d", a, b)
	}
	if a < herdrBridgePortBase || a >= herdrBridgePortBase+herdrBridgePortSpan {
		t.Fatalf("port %d outside band [%d,%d)", a, herdrBridgePortBase, herdrBridgePortBase+herdrBridgePortSpan)
	}
	// Different seeds should usually differ; same is possible but unlikely.
	c := herdrBridgePortForSeed("construct-box-other")
	if a == c {
		t.Logf("note: two distinct seeds collided on port %d (acceptable, rare)", a)
	}
}

func TestHerdrBridgePortForSeed_NoSSHOverlap(t *testing.T) {
	// Herdr band must not overlap the SSH bridge band (38500-48499).
	for _, seed := range []string{"a", "b", "construct-box-x", "long-seed-name-123"} {
		p := herdrBridgePortForSeed(seed)
		if p >= 38500 && p < 48500 {
			t.Fatalf("herdr port %d for seed %q overlaps SSH bridge band", p, seed)
		}
	}
}

func TestHerdrBridgeProxiesToUnixSocket(t *testing.T) {
	// Stand up a fake "herdr" unix socket that echoes a marker on connect.
	// Use /tmp directly: t.TempDir() can exceed the 104-char sun_path limit.
	sockPath := filepath.Join("/tmp", "herdr-test-"+shortID(t)+".sock")
	t.Cleanup(func() { _ = os.Remove(sockPath) })
	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			_, _ = conn.Write([]byte("hello-from-herdr"))
			_ = conn.Close()
		}
	}()

	bridge, err := StartHerdrBridge("test-seed", sockPath)
	if err != nil {
		t.Fatalf("StartHerdrBridge: %v", err)
	}
	t.Cleanup(bridge.Stop)

	// A TCP client (simulating the in-container socat) dials the bridge and
	// must receive the bytes the unix-socket server sent.
	conn, err := net.Dial("tcp", bridge.Listener.Addr().String())
	if err != nil {
		t.Fatalf("dial bridge: %v", err)
	}
	defer func() { _ = conn.Close() }()

	buf := make([]byte, 64)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(buf[:n]) != "hello-from-herdr" {
		t.Fatalf("proxied payload mismatch: got %q", buf[:n])
	}
}

func TestHerdrBridge_RejectsEmptySocket(t *testing.T) {
	if _, err := StartHerdrBridge("seed", ""); err == nil {
		t.Fatal("expected error for empty socket path")
	} else if !strings.Contains(err.Error(), "herdr socket path not set") {
		t.Fatalf("expected 'not set' error, got %v", err)
	}
}

func TestHerdrProxySockForPID_PerPID(t *testing.T) {
	a := herdrProxySockForPID(123)
	b := herdrProxySockForPID(456)
	if a == b {
		t.Fatalf("distinct PIDs must yield distinct sockets: %s", a)
	}
	if !strings.HasPrefix(a, "/tmp/herdr-agent.") {
		t.Fatalf("unexpected socket path: %s", a)
	}
}

func TestHerdrExecEnv_NilWhenNoBridge(t *testing.T) {
	e := &RuntimeEngine{}
	if got := e.herdrExecEnv(); got != nil {
		t.Fatalf("expected nil env without bridge, got %v", got)
	}
}

func TestHerdrExecEnv_InjectsThreeVars(t *testing.T) {
	t.Setenv("HERDR_PANE_ID", "pane-77")
	e := &RuntimeEngine{
		herdrBridge:    &HerdrBridge{}, // non-nil is enough; helper does not touch its fields
		herdrProxySock: "/tmp/herdr-agent.1.sock",
	}
	got := e.herdrExecEnv()
	want := []string{
		"HERDR_SOCKET_PATH=/tmp/herdr-agent.1.sock",
		"HERDR_ENV=1",
		"HERDR_PANE_ID=pane-77",
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d vars, got %d (%v)", len(want), len(got), got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Fatalf("var %d: got %q, want %q", i, got[i], w)
		}
	}
}

// Keep runtime import used on all platforms (the bridge uses runtime.GOOS).
var _ = runtime.GOOS

// Keep os import referenced for clarity in case future helpers need it.
var _ = os.Getenv

// shortID returns a short unique suffix for temp socket paths that must stay
// under the AF_UNIX sun_path limit (104 chars on macOS).
func shortID(t *testing.T) string {
	t.Helper()
	return strings.ReplaceAll(t.Name(), "_", "")[:8]
}

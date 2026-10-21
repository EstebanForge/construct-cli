package runtime

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	msb "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/EstebanForge/construct-cli/internal/config"
)

// TestMsbLiveTransportRulePorts pins the guest-to-host forwarding
// contract on Linux msb: the VMM proxies ONLY rule-named ports to the
// host, so a port listed in bridgePorts is reachable from the guest
// while a bound-but-unlisted port is not. Unnamed ports were proven
// dead on this platform (userspace networking, no TAP), so the control
// probe guards against a future any-port regression widening guest
// reach into unrelated host loopback services. After teardown the host
// must be able to rebind the proxied port (VMM released its side).
func TestMsbLiveTransportRulePorts(t *testing.T) {
	msbLiveEnabled(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	cfg := config.DefaultConfig()
	cfg.Network.Mode = "permissive"

	if err := EnsureMsbVolumes(ctx); err != nil {
		t.Fatalf("EnsureMsbVolumes: %v", err)
	}

	// Both listeners exist on the host; only the ruled port may be
	// reached from the guest.
	ruleLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("rule listener: %v", err)
	}
	defer ruleLn.Close()
	ctrlLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("control listener: %v", err)
	}
	defer ctrlLn.Close()
	rulePort := ruleLn.Addr().(*net.TCPAddr).Port
	ctrlPort := ctrlLn.Addr().(*net.TCPAddr).Port

	pingCh := make(chan string, 1)
	go func() {
		conn, err := ruleLn.Accept()
		if err != nil {
			return
		}
		line, _ := bufio.NewReader(conn).ReadString('\n')
		conn.Close()
		pingCh <- line
	}()

	name := fmt.Sprintf("construct-msb-transport-%d", time.Now().UnixMilli())
	m := NewMsbBackend()
	if err := m.Cleanup(ctx, name); err != nil {
		t.Fatalf("pre-run Cleanup: %v", err)
	}

	proj := t.TempDir()
	msbLiveRequireImage(t)
	spec := BuildMsbRunSpec(&cfg, name, proj, []int{rulePort}, msbLiveImageRef)
	if _, err := CreateMsbSandbox(ctx, spec); err != nil {
		t.Fatalf("CreateMsbSandbox: %v", err)
	}

	// Ruled port: guest dial lands on the host listener.
	if _, code, err := m.Exec(ctx, ExecOptions{
		Name:    name,
		Command: []string{"bash", "-c", fmt.Sprintf("echo ping > /dev/tcp/host.microsandbox.internal/%d", rulePort)},
	}); err != nil || code != 0 {
		t.Fatalf("ruled-port guest dial failed: err=%v code=%d", err, code)
	}
	select {
	case got := <-pingCh:
		if got != "ping\n" {
			t.Fatalf("host read %q, want ping", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("host listener never received the guest connection")
	}

	// Unlisted port: bound on the host but absent from the rules, the
	// guest must NOT reach it (timeout or refusal both count).
	out, code, err := m.Exec(ctx, ExecOptions{
		Name:    name,
		Command: []string{"bash", "-c", fmt.Sprintf("timeout 5 bash -c 'echo x > /dev/tcp/host.microsandbox.internal/%d'", ctrlPort)},
	})
	if err == nil && code == 0 {
		t.Fatalf("unlisted port forwarded from the guest (policy hole): out=%s", out)
	}

	if err := m.Cleanup(ctx, name); err != nil {
		t.Fatalf("post Cleanup: %v", err)
	}
	ruleLn.Close()
	ctrlLn.Close()

	// Post-teardown rebind: the VMM proxy must release the port.
	rebound, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", rulePort))
	if err != nil {
		t.Fatalf("port %d still held after teardown: %v", rulePort, err)
	}
	rebound.Close()
}

// TestMsbLiveMountCollision pins the hash-suffixed workspace dests: two
// same-basename roots must both be live mounts in the guest, each
// serving its own host content. Under the old bare-name dests the
// second bind silently replaced the first.
func TestMsbLiveMountCollision(t *testing.T) {
	msbLiveEnabled(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	cfg := config.DefaultConfig()
	cfg.Network.Mode = "permissive"

	if err := EnsureMsbVolumes(ctx); err != nil {
		t.Fatalf("EnsureMsbVolumes: %v", err)
	}

	mk := func(parent, marker string) string {
		dir := filepath.Join(parent, "proj")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "marker.txt"), []byte(marker), 0o644); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	tmpA := t.TempDir()
	tmpB := t.TempDir()
	dirA := mk(filepath.Join(tmpA, "a"), "a")
	dirB := mk(filepath.Join(tmpB, "b"), "b")
	if filepath.Base(dirA) != filepath.Base(dirB) {
		t.Fatalf("fixture broken: basenames diverge (%s vs %s)", dirA, dirB)
	}

	name := fmt.Sprintf("construct-msb-collision-%d", time.Now().UnixMilli())
	m := NewMsbBackend()
	if err := m.Cleanup(ctx, name); err != nil {
		t.Fatalf("pre-run Cleanup: %v", err)
	}

	proj := t.TempDir()
	msbLiveRequireImage(t)
	spec := BuildMsbRunSpec(&cfg, name, proj, nil, msbLiveImageRef)
	destA := GetMsbWorkspaceMountDest(dirA)
	destB := GetMsbWorkspaceMountDest(dirB)
	if destA == destB {
		t.Fatalf("same-basename roots collided on one dest: %s", destA)
	}
	spec.Mounts[destA] = msb.Mount.Bind(dirA, msb.MountOptions{
		StatVirtualization: msb.StatVirtualizationOff,
	})
	spec.Mounts[destB] = msb.Mount.Bind(dirB, msb.MountOptions{
		StatVirtualization: msb.StatVirtualizationOff,
	})
	if _, err := CreateMsbSandbox(ctx, spec); err != nil {
		t.Fatalf("CreateMsbSandbox: %v", err)
	}

	for _, tc := range []struct{ dest, want string }{{destA, "a"}, {destB, "b"}} {
		out, code, err := m.Exec(ctx, ExecOptions{
			Name:    name,
			Command: []string{"cat", tc.dest + "/marker.txt"},
		})
		if err != nil || code != 0 {
			t.Fatalf("mount %s not live in guest: err=%v code=%d", tc.dest, err, code)
		}
		if got := strings.TrimSpace(out); got != tc.want {
			t.Fatalf("mount %s serves %q, want %q (collision or stale bind)", tc.dest, got, tc.want)
		}
	}

	if err := m.Cleanup(ctx, name); err != nil {
		t.Fatalf("post Cleanup: %v", err)
	}
}

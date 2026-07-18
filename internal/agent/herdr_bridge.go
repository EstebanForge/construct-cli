package agent

import (
	"crypto/sha256"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"sync"
	"time"

	containerruntime "github.com/EstebanForge/construct-cli/internal/runtime"
	"github.com/EstebanForge/construct-cli/internal/ui"
)

// HerdrBridge manages a TCP-to-Unix bridge so processes inside a construct
// container can reach the host-only Herdr API socket. Containers cannot use a
// bind-mounted AF_UNIX socket (Docker Desktop surfaces a regular file, not a
// live socket), so a host TCP listener proxies each connection to the unix
// socket, and an in-container socat (per session, runtime-launched) bridges
// back to that TCP port. This mirrors the SSH agent bridge.
type HerdrBridge struct {
	Listener net.Listener
	Port     int
	socket   string // host unix socket being proxied
	Quit     chan struct{}
	wg       sync.WaitGroup
}

// herdrBridgePortBase/Span define a deterministic port band (must not overlap
// the SSH bridge's 38500-48499). Lives inside the Linux ephemeral range.
const (
	herdrBridgePortBase = 48600
	herdrBridgePortSpan = 10000
)

// herdrBridgePortForSeed derives a stable TCP port from a seed (container name)
// so the same box maps to the same port across invocations. Falls back to an
// OS-assigned ephemeral port on bind failure (correctness is still guaranteed
// by the per-exec socat restart).
func herdrBridgePortForSeed(seed string) int {
	h := sha256.Sum256([]byte(seed))
	v := int(h[0])<<8 | int(h[1])
	return herdrBridgePortBase + v%herdrBridgePortSpan
}

// StartHerdrBridge starts a local TCP server proxying to the host Herdr unix
// socket. The port is derived deterministically from seed; if that port is in
// use it falls back to an ephemeral port.
// On macOS it binds 127.0.0.1 (Docker Desktop routes it into containers).
// On Linux it binds 0.0.0.0 so containers reach it via host.docker.internal.
func StartHerdrBridge(seed, socket string) (*HerdrBridge, error) {
	if socket == "" {
		return nil, fmt.Errorf("herdr socket path not set")
	}

	bindAddr := func(port int) string {
		if runtime.GOOS == "linux" {
			return fmt.Sprintf("0.0.0.0:%d", port)
		}
		return fmt.Sprintf("127.0.0.1:%d", port)
	}

	port := herdrBridgePortForSeed(seed)
	l, err := net.Listen("tcp", bindAddr(port))
	if err != nil {
		ui.LogDebug("Herdr bridge: deterministic port %d unavailable (%v); using ephemeral", port, err)
		l, err = net.Listen("tcp", bindAddr(0))
		if err != nil {
			return nil, fmt.Errorf("failed to start Herdr bridge listener: %w", err)
		}
	}

	bridge := &HerdrBridge{
		Listener: l,
		Port:     l.Addr().(*net.TCPAddr).Port,
		socket:   socket,
		Quit:     make(chan struct{}),
	}

	bridge.wg.Add(1)
	go bridge.serve()

	return bridge, nil
}

// serve accepts connections and proxies each to the Herdr unix socket.
func (b *HerdrBridge) serve() {
	defer b.wg.Done()
	for {
		conn, err := b.Listener.Accept()
		if err != nil {
			select {
			case <-b.Quit:
				return
			default:
				ui.LogDebug("Herdr bridge accept error: %v", err)
				continue
			}
		}

		go b.handleConnection(conn)
	}
}

// handleConnection proxies a single connection to the Herdr socket.
func (b *HerdrBridge) handleConnection(localConn net.Conn) {
	defer func() {
		if err := localConn.Close(); err != nil {
			ui.LogDebug("Herdr bridge failed to close local connection: %v", err)
		}
	}()

	var remoteConn net.Conn
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		remoteConn, err = net.Dial("unix", b.socket)
		if err == nil {
			break
		}
		ui.LogDebug("Herdr bridge dial attempt %d failed: %v", attempt+1, err)
		if attempt < 2 {
			time.Sleep(100 * time.Millisecond)
		}
	}
	if err != nil {
		ui.LogDebug("Herdr bridge failed to dial socket after retries: %v", err)
		return
	}
	defer func() {
		if err := remoteConn.Close(); err != nil {
			ui.LogDebug("Herdr bridge failed to close remote connection: %v", err)
		}
	}()

	done := make(chan struct{}, 2)
	go func() {
		if _, err := io.Copy(remoteConn, localConn); err != nil {
			ui.LogDebug("Herdr bridge copy to socket error: %v", err)
		}
		done <- struct{}{}
	}()
	go func() {
		if _, err := io.Copy(localConn, remoteConn); err != nil {
			ui.LogDebug("Herdr bridge copy from socket error: %v", err)
		}
		done <- struct{}{}
	}()

	<-done
}

// Stop stops the bridge and waits for in-flight connections to close.
func (b *HerdrBridge) Stop() {
	close(b.Quit)
	if err := b.Listener.Close(); err != nil {
		ui.LogDebug("Herdr bridge failed to close listener: %v", err)
	}
	b.wg.Wait()
}

// herdrProxySockForPID returns the in-container unix socket path a session's
// socat listens on. Per-PID so concurrent sessions do not collide.
func herdrProxySockForPID(pid int) string {
	return fmt.Sprintf("/tmp/herdr-agent.%d.sock", pid)
}

// ensureDaemonHerdrProxy launches (or relaunches) the in-container socat that
// bridges the per-session unix socket to the host TCP port. Mirrors the SSH
// agent proxy: socat is already required in the image, so this is a runtime
// docker exec (no image rebuild). Best-effort; callers log on error.
func ensureDaemonHerdrProxy(containerRuntime, daemonName, sockPath string, port int, execUser string) error {
	envVars := []string{fmt.Sprintf("CONSTRUCT_HDR_BRIDGE_PORT=%d", port)}
	cmdArgs := []string{"bash", "-lc", `if ! command -v socat >/dev/null; then echo "socat not found" >&2; exit 1; fi; PROXY_SOCK="` + sockPath + `"; PROXY_DIR="$(dirname "$PROXY_SOCK")"; mkdir -p "$PROXY_DIR" 2>/dev/null || true; chmod 700 "$PROXY_DIR" 2>/dev/null || true; pkill -f "socat UNIX-LISTEN:$PROXY_SOCK" 2>/dev/null || true; rm -f "$PROXY_SOCK"; nohup socat UNIX-LISTEN:"$PROXY_SOCK",fork,mode=600 TCP:host.docker.internal:"$CONSTRUCT_HDR_BRIDGE_PORT" >/tmp/herdr-socat.log 2>&1 &`}
	if _, err := containerruntime.ExecInContainerWithEnv(containerRuntime, daemonName, cmdArgs, envVars, execUser); err != nil {
		return fmt.Errorf("start herdr proxy socat on %s (socket %s, port %d): %w", daemonName, sockPath, port, err)
	}
	return nil
}

// waitForDaemonHerdrProxy polls until the in-container socket accepts
// connections (or gives up after ~1.5s). test -S alone is not enough: a stale
// socket file passes that check.
func waitForDaemonHerdrProxy(containerRuntime, daemonName, sockPath, execUser string) error {
	for i := 0; i < 10; i++ {
		cmdArgs := []string{"bash", "-lc", `command -v socat >/dev/null || exit 1; socat -u OPEN:/dev/null UNIX-CONNECT:"` + sockPath + `"`}
		if _, err := containerruntime.ExecInContainerWithEnv(containerRuntime, daemonName, cmdArgs, nil, execUser); err == nil {
			return nil
		}
		time.Sleep(150 * time.Millisecond)
	}
	return fmt.Errorf("herdr proxy on %s (socket %s) not accepting connections after retries", daemonName, sockPath)
}

// herdrExecEnv returns the env vars construct must inject so a Herdr-aware
// agent integration inside the container fires: the in-container socket path,
// the pane id (forwarded verbatim from the host pane shell), and HERDR_ENV=1
// (the gate the integrations check). Returns nil when the bridge is not
// established, so callers can iterate without a nil guard.
func (e *RuntimeEngine) herdrExecEnv() []string {
	if e.herdrBridge == nil || e.herdrProxySock == "" {
		return nil
	}
	return []string{
		"HERDR_SOCKET_PATH=" + e.herdrProxySock,
		"HERDR_ENV=1",
		"HERDR_PANE_ID=" + os.Getenv("HERDR_PANE_ID"),
	}
}

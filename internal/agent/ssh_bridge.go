package agent

import (
	"crypto/sha256"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/EstebanForge/construct-cli/internal/config"
	runtimepkg "github.com/EstebanForge/construct-cli/internal/runtime"
	"github.com/EstebanForge/construct-cli/internal/ui"
)

// SSHBridge manages a TCP-to-Unix bridge for SSH agent forwarding.
type SSHBridge struct {
	Listener net.Listener
	Port     int
	Quit     chan struct{}
	wg       sync.WaitGroup
}

// sshBridgePortBase/Span define a deterministic port band used to give each
// box a stable host port across CLI invocations (so a socat baked into the
// container at creation keeps pointing at the right port). The band lives inside
// the Linux ephemeral range (default 32768-60999), so it can collide with an
// OS-allocated port; StartSSHBridge handles that by falling back to an
// OS-assigned ephemeral port on bind failure (correctness is still guaranteed by
// the per-exec socat restart).
// sshBridgePortBase/Span alias the deterministic bridge band owned by the
// runtime package: the msb network policy names the same band, so both
// sides must move together.
const (
	sshBridgePortBase = runtimepkg.MsbBridgePortBase
	sshBridgePortSpan = runtimepkg.MsbBridgePortSpan
)

// sshBridgePortForSeed derives a stable TCP port from a seed (typically the box
// container name). The same box always maps to the same port so a stale socat
// baked into the container at creation keeps pointing at the right host port
// across CLI invocations, instead of aging out as the random host port changes.
func sshBridgePortForSeed(seed string) int {
	h := sha256.Sum256([]byte(seed))
	v := int(h[0])<<8 | int(h[1])
	return sshBridgePortBase + v%sshBridgePortSpan
}

// bridgeBindHost returns the address bridges should bind for a backend:
// msb's VMM proxies allowed guest→host ports to host loopback
// (live-verified 2026-10), so 127.0.0.1 is reachable and the listener
// stops exposing every port LAN-wide; docker's Linux host-gateway is the
// bridge IP, which requires 0.0.0.0. macOS Docker Desktop routes
// loopback into containers on every backend.
func bridgeBindHost(backend string) string {
	if runtime.GOOS == "darwin" {
		return "127.0.0.1"
	}
	if backend == config.BackendMsb {
		return "127.0.0.1"
	}
	return "0.0.0.0"
}

// sshPinIdentitiesEnv serializes cfg.Sandbox.SSHPinIdentities into the
// comma-separated host=keyname form consumed by entrypoint.sh
// (CONSTRUCT_SSH_PIN_IDENTITIES). Malformed entries (empty, missing '=', or
// containing the ',' separator) are dropped. Returns "" when nothing valid is
// configured, so callers can skip injecting the env var entirely.
func sshPinIdentitiesEnv(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	valid := make([]string, 0, len(cfg.Sandbox.SSHPinIdentities))
	for _, entry := range cfg.Sandbox.SSHPinIdentities {
		entry = strings.TrimSpace(entry)
		if entry == "" || strings.Contains(entry, ",") || !strings.Contains(entry, "=") {
			continue
		}
		valid = append(valid, entry)
	}
	return strings.Join(valid, ",")
}

// StartSSHBridge starts a local TCP server that proxies to the local SSH agent.
// The port is derived deterministically from seed so it is stable per box across
// invocations; if that port is already in use it falls back to an ephemeral port
// (correctness is still guaranteed by the per-exec socat restart).
// bindHost decides exposure: msb's VMM proxies allowed guest→host ports to
// host loopback, so 127.0.0.1 is reachable; docker's Linux host-gateway is
// the bridge IP, which needs 0.0.0.0.
func StartSSHBridge(seed, bindHost string) (*SSHBridge, error) {
	if os.Getenv("SSH_AUTH_SOCK") == "" {
		return nil, fmt.Errorf("SSH_AUTH_SOCK not set on host")
	}

	bindAddr := func(port int) string {
		return fmt.Sprintf("%s:%d", bindHost, port)
	}

	port := sshBridgePortForSeed(seed)
	l, err := net.Listen("tcp", bindAddr(port))
	if err != nil {
		ui.LogDebug("SSH bridge: deterministic port %d unavailable (%v); using ephemeral", port, err)
		l, err = net.Listen("tcp", bindAddr(0))
		if err != nil {
			return nil, fmt.Errorf("failed to start SSH bridge listener: %w", err)
		}
	}

	bridge := &SSHBridge{
		Listener: l,
		Port:     l.Addr().(*net.TCPAddr).Port,
		Quit:     make(chan struct{}),
	}

	bridge.wg.Add(1)
	go bridge.serve()

	return bridge, nil
}

// serve accepts connections and proxies each to the SSH agent.
// SSH_AUTH_SOCK is re-read per connection to handle agents like Bitwarden
// that recycle socket paths on vault lock/unlock.
func (b *SSHBridge) serve() {
	defer b.wg.Done()
	for {
		conn, err := b.Listener.Accept()
		if err != nil {
			select {
			case <-b.Quit:
				return
			default:
				ui.LogDebug("SSH bridge accept error: %v", err)
				continue
			}
		}

		go b.handleConnection(conn)
	}
}

// handleConnection proxies a single connection to the SSH agent.
// Re-reads SSH_AUTH_SOCK on each connection to handle agents like Bitwarden
// that recycle socket paths on vault lock/unlock.
// Retries up to 3 times with 100ms backoff for transient socket availability.
func (b *SSHBridge) handleConnection(localConn net.Conn) {
	defer func() {
		if err := localConn.Close(); err != nil {
			ui.LogDebug("SSH bridge failed to close local connection: %v", err)
		}
	}()

	sshAuthSock := os.Getenv("SSH_AUTH_SOCK")
	if sshAuthSock == "" {
		ui.LogDebug("SSH bridge: SSH_AUTH_SOCK not set, dropping connection")
		return
	}

	var remoteConn net.Conn
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		remoteConn, err = net.Dial("unix", sshAuthSock)
		if err == nil {
			break
		}
		ui.LogDebug("SSH bridge dial attempt %d failed: %v", attempt+1, err)
		if attempt < 2 {
			time.Sleep(100 * time.Millisecond)
		}
	}
	if err != nil {
		ui.LogDebug("SSH bridge failed to dial agent after retries: %v", err)
		return
	}
	defer func() {
		if err := remoteConn.Close(); err != nil {
			ui.LogDebug("SSH bridge failed to close remote connection: %v", err)
		}
	}()

	// Proxy data bidirectionally
	done := make(chan struct{}, 2)
	go func() {
		if _, err := io.Copy(remoteConn, localConn); err != nil {
			ui.LogDebug("SSH bridge copy to agent error: %v", err)
		}
		done <- struct{}{}
	}()
	go func() {
		if _, err := io.Copy(localConn, remoteConn); err != nil {
			ui.LogDebug("SSH bridge copy from agent error: %v", err)
		}
		done <- struct{}{}
	}()

	<-done
}

// Stop stops the bridge and waits for connections to close.
func (b *SSHBridge) Stop() {
	close(b.Quit)
	if err := b.Listener.Close(); err != nil {
		ui.LogDebug("SSH bridge failed to close listener: %v", err)
	}
	b.wg.Wait()
}

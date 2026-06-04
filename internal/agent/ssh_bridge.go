package agent

import (
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/EstebanForge/construct-cli/internal/ui"
)

// SSHBridge manages a TCP-to-Unix bridge for SSH agent forwarding.
type SSHBridge struct {
	Listener net.Listener
	Port     int
	Quit     chan struct{}
	wg       sync.WaitGroup
}

// StartSSHBridge starts a local TCP server that proxies to the local SSH agent.
// On macOS it binds 127.0.0.1 (Docker Desktop handles routing).
// On Linux it binds 0.0.0.0 so containers can reach it via host.docker.internal.
func StartSSHBridge() (*SSHBridge, error) {
	if os.Getenv("SSH_AUTH_SOCK") == "" {
		return nil, fmt.Errorf("SSH_AUTH_SOCK not set on host")
	}

	bindAddr := "127.0.0.1:0"
	if runtime.GOOS == "linux" {
		bindAddr = "0.0.0.0:0"
	}

	l, err := net.Listen("tcp", bindAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to start SSH bridge listener: %w", err)
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

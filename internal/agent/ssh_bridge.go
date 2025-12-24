package agent

import (
	"fmt"
	"io"
	"net"
	"os"
	"sync"

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
func StartSSHBridge() (*SSHBridge, error) {
	sshAuthSock := os.Getenv("SSH_AUTH_SOCK")
	if sshAuthSock == "" {
		return nil, fmt.Errorf("SSH_AUTH_SOCK not set on host")
	}

	// Listen on localhost with a random port
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("failed to start SSH bridge listener: %w", err)
	}

	bridge := &SSHBridge{
		Listener: l,
		Port:     l.Addr().(*net.TCPAddr).Port,
		Quit:     make(chan struct{}),
	}

	bridge.wg.Add(1)
	go bridge.serve(sshAuthSock)

	return bridge, nil
}

func (b *SSHBridge) serve(sshAuthSock string) {
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

		go b.handleConnection(conn, sshAuthSock)
	}
}

func (b *SSHBridge) handleConnection(localConn net.Conn, sshAuthSock string) {
	defer func() {
		if err := localConn.Close(); err != nil {
			ui.LogDebug("SSH bridge failed to close local connection: %v", err)
		}
	}()

	// Connect to the actual SSH agent socket
	remoteConn, err := net.Dial("unix", sshAuthSock)
	if err != nil {
		ui.LogDebug("SSH bridge failed to dial agent: %v", err)
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

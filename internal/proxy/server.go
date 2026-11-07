package proxy

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Server is the host-side egress HTTP/CONNECT proxy.
type Server struct {
	policy       *Policy
	sessionToken string
	listener     net.Listener
	httpServer   *http.Server
	mu           sync.Mutex
	closed       bool
}

// Config configures the proxy server.
type Config struct {
	Tier          string
	AllowedHosts  []string
	LoopbackPorts []int
	HostGateway   string
	SessionToken  string
}

// NewServer initializes a proxy instance.
func NewServer(cfg Config) (*Server, error) {
	token := cfg.SessionToken
	if token == "" {
		token = generateRandomToken(24)
	}
	p := NewPolicy(cfg.Tier, cfg.AllowedHosts, cfg.LoopbackPorts, cfg.HostGateway)

	s := &Server{
		policy:       p,
		sessionToken: token,
	}

	s.httpServer = &http.Server{
		Handler:           http.HandlerFunc(s.handleProxy),
		ReadHeaderTimeout: 10 * time.Second,
	}

	return s, nil
}

// Start binds and starts the proxy server on addr (e.g. "127.0.0.1:0").
func (s *Server) Start(addr string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	l, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to bind proxy: %w", err)
	}
	s.listener = l

	go func() {
		if err := s.httpServer.Serve(l); err != nil && !errors.Is(err, http.ErrServerClosed) {
			// server closed or failed to bind
			return
		}
	}()

	return nil
}

// Port returns the bound port.
func (s *Server) Port() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener == nil {
		return 0
	}
	return s.listener.Addr().(*net.TCPAddr).Port
}

// SessionToken returns the active authentication token.
func (s *Server) SessionToken() string {
	return s.sessionToken
}

// Close terminates the proxy server.
func (s *Server) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return s.httpServer.Close()
}

func (s *Server) authenticate(r *http.Request) bool {
	auth := r.Header.Get("Proxy-Authorization")
	if auth == "" {
		return false
	}

	// Support Bearer <token>
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ") == s.sessionToken
	}

	// Support Basic <base64(user:token)>
	if strings.HasPrefix(auth, "Basic ") {
		payload, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(auth, "Basic "))
		if err == nil {
			parts := strings.SplitN(string(payload), ":", 2)
			if len(parts) == 2 && parts[1] == s.sessionToken {
				return true
			}
		}
	}

	return false
}

func (s *Server) handleProxy(w http.ResponseWriter, r *http.Request) {
	if !s.authenticate(r) {
		w.Header().Set("Proxy-Authenticate", `Basic realm="construct-proxy"`)
		http.Error(w, "Proxy authentication required", http.StatusProxyAuthRequired)
		return
	}

	if r.Method == http.MethodConnect {
		s.handleConnect(w, r)
		return
	}

	s.handleHTTP(w, r)
}

func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	targetIP, port, err := s.policy.CheckDestination(r.Context(), r.Host)
	if err != nil {
		w.Header().Set("X-Construct-Reason", err.Error())
		http.Error(w, fmt.Sprintf("Forbidden: %v", err), http.StatusForbidden)
		return
	}

	targetAddr := net.JoinHostPort(targetIP.String(), strconv.Itoa(port))
	destConn, err := net.DialTimeout("tcp", targetAddr, 10*time.Second)
	if err != nil {
		http.Error(w, fmt.Sprintf("Connection failed: %v", err), http.StatusBadGateway)
		return
	}

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		_ = destConn.Close() //nolint:errcheck // best-effort cleanup on hijack rejection
		http.Error(w, "Hijacking not supported", http.StatusInternalServerError)
		return
	}

	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		_ = destConn.Close() //nolint:errcheck // best-effort cleanup on hijack failure
		http.Error(w, fmt.Sprintf("Hijack failed: %v", err), http.StatusInternalServerError)
		return
	}

	// Acknowledge CONNECT tunnel establishment
	if _, err := clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		_ = clientConn.Close() //nolint:errcheck // best-effort cleanup
		_ = destConn.Close()   //nolint:errcheck // best-effort cleanup
		return
	}

	go pipe(clientConn, destConn)
}

func (s *Server) handleHTTP(w http.ResponseWriter, r *http.Request) {
	targetIP, port, err := s.policy.CheckDestination(r.Context(), r.URL.Host)
	if err != nil {
		w.Header().Set("X-Construct-Reason", err.Error())
		http.Error(w, fmt.Sprintf("Forbidden: %v", err), http.StatusForbidden)
		return
	}

	outReq, err := http.NewRequestWithContext(r.Context(), r.Method, r.URL.String(), r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	for k, vv := range r.Header {
		if strings.EqualFold(k, "Proxy-Authorization") {
			continue
		}
		for _, v := range vv {
			outReq.Header.Add(k, v)
		}
	}

	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, net.JoinHostPort(targetIP.String(), strconv.Itoa(port)))
		},
	}

	resp, err := transport.RoundTrip(outReq)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer func() {
		_ = resp.Body.Close() //nolint:errcheck // body drain on exit
	}()

	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body) //nolint:errcheck // client write errors handled by server
}

func pipe(conn1, conn2 net.Conn) {
	defer func() {
		_ = conn1.Close() //nolint:errcheck // pipe teardown
		_ = conn2.Close() //nolint:errcheck // pipe teardown
	}()

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		_, _ = io.Copy(conn1, conn2) //nolint:errcheck // tunnel pipe
	}()

	go func() {
		defer wg.Done()
		_, _ = io.Copy(conn2, conn1) //nolint:errcheck // tunnel pipe
	}()

	wg.Wait()
}

func generateRandomToken(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

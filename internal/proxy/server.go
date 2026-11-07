package proxy

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Server is the host-side egress HTTP/CONNECT proxy.
type Server struct {
	policy       *Policy
	sessionToken string
	ca           *CA
	upstreamCAs  *x509.CertPool
	secrets      *SecretRegistry
	listener     net.Listener
	httpServer   *http.Server
	logDir       string
	mu           sync.Mutex
	closed       bool
}

// Config configures the proxy server.
type Config struct {
	Tier           string
	AllowedHosts   []string
	AllowedIPs     []string
	BlockedDomains []string
	BlockedIPs     []string
	AllowedTCP     []string
	LoopbackPorts  []int
	HostGateway    string
	SessionToken   string
	CADir          string
	LogDir         string
	UpstreamCAs    *x509.CertPool
}

// NewServer initializes a proxy instance.
func NewServer(cfg Config) (*Server, error) {
	token := cfg.SessionToken
	if token == "" {
		token = generateRandomToken(24)
	}
	p := NewPolicy(cfg.Tier, cfg.AllowedHosts, cfg.LoopbackPorts, cfg.HostGateway, cfg.AllowedTCP)
	p.SetBlocklists(cfg.BlockedDomains, cfg.BlockedIPs)
	p.SetAllowedIPs(cfg.AllowedIPs)

	var caInstance *CA
	if cfg.CADir != "" {
		c, err := EnsureCA(cfg.CADir)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize CA: %w", err)
		}
		caInstance = c
	}

	if cfg.LogDir != "" {
		_ = os.MkdirAll(cfg.LogDir, 0755) //nolint:errcheck // best-effort log directory creation
	}

	s := &Server{
		policy:       p,
		sessionToken: token,
		ca:           caInstance,
		upstreamCAs:  cfg.UpstreamCAs,
		secrets:      NewSecretRegistry(),
		logDir:       cfg.LogDir,
	}

	s.httpServer = &http.Server{
		Handler:           http.HandlerFunc(s.handleProxy),
		ReadHeaderTimeout: 10 * time.Second,
	}

	return s, nil
}

// RegisterSecret registers a provider secret and returns its sentinel placeholder.
func (s *Server) RegisterSecret(varName, realValue, boundHost string) string {
	return s.secrets.Register(varName, realValue, boundHost)
}

// CA returns the proxy CA instance.
func (s *Server) CA() *CA {
	return s.ca
}

// Secrets returns the secret registry.
func (s *Server) Secrets() *SecretRegistry {
	return s.secrets
}

// IsSentinel reports whether val is a registered sentinel placeholder.
func (s *Server) IsSentinel(val string) bool {
	if s == nil || s.secrets == nil {
		return false
	}
	return s.secrets.IsSentinel(val)
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
		s.logf("AUTH_FAIL", r.RemoteAddr, "invalid session token")
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
		s.logf("BLOCK", r.Host, err.Error())
		w.Header().Set("X-Construct-Reason", err.Error())
		http.Error(w, fmt.Sprintf("Forbidden: %v", err), http.StatusForbidden)
		return
	}
	hostOnly, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		hostOnly = r.Host
	}

	isRaw := s.policy.IsRawTCP(hostOnly, port)
	if isRaw {
		s.logf("ALLOW_TCP", r.Host, fmt.Sprintf("ip=%s port=%d", targetIP.String(), port))
	} else {
		s.logf("ALLOW", r.Host, fmt.Sprintf("ip=%s port=%d", targetIP.String(), port))
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

	// Bypass MITM if host is pinned, destination is raw TCP, or CA is not configured.
	if s.ca == nil || s.policy.IsPinned(hostOnly) || isRaw {
		go pipe(clientConn, destConn)
		return
	}

	go s.interceptTLS(clientConn, destConn, hostOnly)
}

func (s *Server) interceptTLS(clientConn, destConn net.Conn, targetHost string) {
	defer func() {
		_ = clientConn.Close() //nolint:errcheck // teardown
		_ = destConn.Close()   //nolint:errcheck // teardown
	}()

	leafCert, err := s.ca.SignHostCert(targetHost)
	if err != nil {
		return
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{*leafCert},
		GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
			// Domain fronting guard: TLS SNI must match CONNECT host.
			if hello.ServerName != "" && !strings.EqualFold(hello.ServerName, targetHost) {
				return nil, fmt.Errorf("domain fronting detected: SNI %q != CONNECT %q", hello.ServerName, targetHost)
			}
			return nil, nil
		},
	}

	tlsClient := tls.Server(clientConn, tlsConfig)
	if err := tlsClient.Handshake(); err != nil {
		return
	}
	defer func() {
		_ = tlsClient.Close() //nolint:errcheck // teardown
	}()

	tlsDest := tls.Client(destConn, &tls.Config{
		ServerName: targetHost,
		RootCAs:    s.upstreamCAs,
	})
	if err := tlsDest.Handshake(); err != nil {
		return
	}
	defer func() {
		_ = tlsDest.Close() //nolint:errcheck // teardown
	}()

	clientReader := bufio.NewReader(tlsClient)
	destReader := bufio.NewReader(tlsDest)

	for {
		req, err := http.ReadRequest(clientReader)
		if err != nil {
			return
		}

		// Enforce domain fronting guard on HTTP Host header.
		reqHost := req.Host
		if h, _, err := net.SplitHostPort(reqHost); err == nil {
			reqHost = h
		}
		if reqHost != "" && !strings.EqualFold(reqHost, targetHost) {
			reason := fmt.Sprintf("domain fronting: Host %q != CONNECT %q", reqHost, targetHost)
			s.logf("FRONTING_DROP", targetHost, reason)
			resp := &http.Response{
				StatusCode: http.StatusForbidden,
				ProtoMajor: 1,
				ProtoMinor: 1,
				Header:     make(http.Header),
			}
			resp.Header.Set("X-Construct-Reason", reason)
			_ = resp.Write(tlsClient) //nolint:errcheck // client response
			return
		}

		// Perform outbound secret swap on request headers.
		if err := s.secrets.SwapHeaders(req.Header, targetHost); err != nil {
			s.logf("LEAK_DROP", targetHost, err.Error())
			resp := &http.Response{
				StatusCode: http.StatusForbidden,
				ProtoMajor: 1,
				ProtoMinor: 1,
				Header:     make(http.Header),
			}
			resp.Header.Set("X-Construct-Reason", err.Error())
			_ = resp.Write(tlsClient) //nolint:errcheck // client response
			return
		}

		if err := req.Write(tlsDest); err != nil {
			return
		}

		resp, err := http.ReadResponse(destReader, req)
		if err != nil {
			return
		}

		if err := resp.Write(tlsClient); err != nil {
			_ = resp.Body.Close() //nolint:errcheck // body drain
			return
		}
		_ = resp.Body.Close() //nolint:errcheck // body drain
	}
}

func (s *Server) handleHTTP(w http.ResponseWriter, r *http.Request) {
	targetIP, port, err := s.policy.CheckDestination(r.Context(), r.URL.Host)
	if err != nil {
		s.logf("BLOCK", r.URL.Host, err.Error())
		w.Header().Set("X-Construct-Reason", err.Error())
		http.Error(w, fmt.Sprintf("Forbidden: %v", err), http.StatusForbidden)
		return
	}
	s.logf("ALLOW", r.URL.Host, fmt.Sprintf("ip=%s port=%d", targetIP.String(), port))

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

	// Prohibit transmitting secrets or sentinels over unencrypted plaintext HTTP.
	for k, vv := range outReq.Header {
		for _, v := range vv {
			if s.secrets.ContainsRealSecret([]byte(v)) {
				reason := fmt.Sprintf("outbound leak detected in header %q: contains real secret over unencrypted http", k)
				s.logf("LEAK_DROP", r.URL.Host, reason)
				w.Header().Set("X-Construct-Reason", reason)
				http.Error(w, fmt.Sprintf("Forbidden: %s", reason), http.StatusForbidden)
				return
			}
			if strings.Contains(v, "cnstr_") {
				reason := "secret transmission forbidden over unencrypted plaintext HTTP"
				s.logf("LEAK_DROP", r.URL.Host, reason)
				w.Header().Set("X-Construct-Reason", reason)
				http.Error(w, fmt.Sprintf("Forbidden: %s", reason), http.StatusForbidden)
				return
			}
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

func (s *Server) logf(action, dest, reason string) {
	if s.logDir == "" {
		return
	}
	f, err := os.OpenFile(filepath.Join(s.logDir, "proxy.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close() //nolint:errcheck

	line := fmt.Sprintf("%s action=%s dest=%s", time.Now().UTC().Format(time.RFC3339), action, dest)
	if reason != "" {
		line += fmt.Sprintf(" reason=%q", reason)
	}
	line += "\n"
	_, _ = f.WriteString(line) //nolint:errcheck // best-effort audit write
}

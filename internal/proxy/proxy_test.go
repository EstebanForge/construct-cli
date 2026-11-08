package proxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestPolicyPrivateIPs(t *testing.T) {
	tests := []struct {
		ip      string
		blocked bool
	}{
		{"127.0.0.1", true},
		{"10.0.0.1", true},
		{"172.16.0.1", true},
		{"192.168.1.1", true},
		{"169.254.169.254", true},
		{"100.64.0.1", true},
		{"::1", true},
		{"fc00::1", true},
		{"fe80::1", true},
		{"8.8.8.8", false},
		{"1.1.1.1", false},
	}

	for _, tc := range tests {
		ip := net.ParseIP(tc.ip)
		if got := IsPrivateIP(ip); got != tc.blocked {
			t.Errorf("IsPrivateIP(%s) = %v, want %v", tc.ip, got, tc.blocked)
		}
	}
}

func TestPolicyAllowlistAndPresets(t *testing.T) {
	ctx := context.Background()
	p := NewPolicy(TierAllowlist, []string{"my-api.example.com", "*.custom.org"}, []int{8080}, "host.docker.internal")

	// Pinned host check
	p.AddPinnedHost("pinned.example.com")
	if !p.IsPinned("pinned.example.com") {
		t.Errorf("expected pinned.example.com to be pinned")
	}

	// Ecosystem presets should be allowed (e.g. github.com)
	if !p.isHostAllowed("github.com") {
		t.Errorf("expected github.com to be allowed via ecosystem presets")
	}
	if !p.isHostAllowed("registry.npmjs.org") {
		t.Errorf("expected registry.npmjs.org to be allowed via ecosystem presets")
	}
	if !p.isHostAllowed("my-api.example.com") {
		t.Errorf("expected my-api.example.com to be allowed")
	}
	if !p.isHostAllowed("sub.custom.org") {
		t.Errorf("expected sub.custom.org to be allowed via wildcard")
	}
	if p.isHostAllowed("evil.com") {
		t.Errorf("expected evil.com to be denied")
	}

	// Loopback relay check
	if !p.isLoopbackRelay("host.docker.internal", 8080) {
		t.Errorf("expected host.docker.internal:8080 to be allowed as loopback relay")
	}
	if p.isLoopbackRelay("host.docker.internal", 9000) {
		t.Errorf("expected unconfigured port on host gateway to be denied")
	}

	// TierNone should block everything
	pNone := NewPolicy(TierNone, []string{"github.com"}, nil, "")
	_, _, err := pNone.CheckDestination(ctx, "github.com:443")
	if err == nil {
		t.Errorf("expected TierNone to fail all destinations")
	}
}

func TestPolicyExplain(t *testing.T) {
	ctx := context.Background()
	p := NewPolicy(TierAllowlist, []string{"my-api.example.com", "*.custom.org"}, []int{8080}, "host.docker.internal")

	// Config allowlist
	d1 := p.Explain(ctx, "my-api.example.com:443")
	if !d1.Allowed && !strings.Contains(d1.Reason, "dns") {
		t.Errorf("expected allowed config host, got %+v", d1)
	}
	if d1.Source != "config" {
		t.Errorf("expected source 'config', got %q", d1.Source)
	}

	// Wildcard allowlist
	d2 := p.Explain(ctx, "sub.custom.org:443")
	if d2.Source != "config (*.custom.org)" {
		t.Errorf("expected wildcard source, got %q", d2.Source)
	}

	// Preset
	d3 := p.Explain(ctx, "registry.npmjs.org:443")
	if d3.Source != "preset:npm" {
		t.Errorf("expected preset:npm source, got %q", d3.Source)
	}

	// Loopback relay
	d4 := p.Explain(ctx, "host.docker.internal:8080")
	if !d4.Allowed || d4.Source != "loopback_relay" {
		t.Errorf("expected loopback_relay decision, got %+v", d4)
	}

	// Blocked host
	d5 := p.Explain(ctx, "evil.com:443")
	if d5.Allowed {
		t.Errorf("expected evil.com to be blocked")
	}
	if !strings.Contains(d5.Reason, "not in allowlist") {
		t.Errorf("expected allowlist reason, got %q", d5.Reason)
	}

	// Tier none
	pNone := NewPolicy(TierNone, nil, nil, "")
	dNone := pNone.Explain(ctx, "registry.npmjs.org:443")
	if dNone.Allowed || !strings.Contains(dNone.Reason, "tier 'none'") {
		t.Errorf("expected TierNone rejection, got %+v", dNone)
	}

	// Tier none must reject even loopback relay
	pNoneLoop := NewPolicy("offline", nil, []int{8080}, "localhost")
	dNoneLoop := pNoneLoop.Explain(ctx, "localhost:8080")
	if dNoneLoop.Allowed {
		t.Errorf("expected TierNone to reject loopback relay, got %+v", dNoneLoop)
	}

	// Tier normalization (H4)
	pOpen := NewPolicy("permissive", nil, nil, "")
	if pOpen.tier != TierOpen {
		t.Errorf("expected permissive normalized to open, got %q", pOpen.tier)
	}

	// Tier open must block private IPs and cloud metadata (H5)
	dMeta := pOpen.Explain(ctx, "169.254.169.254:80")
	if dMeta.Allowed {
		t.Errorf("expected 169.254.169.254 to be blocked in TierOpen")
	}
	dPriv := pOpen.Explain(ctx, "127.0.0.1:9999")
	if dPriv.Allowed {
		t.Errorf("expected 127.0.0.1 to be blocked in TierOpen")
	}

	// Blocklists (H6)
	pBlock := NewPolicy(TierOpen, nil, nil, "")
	pBlock.SetBlocklists([]string{"malicious.org", "*.evil.com"}, []string{"198.51.100.1"})
	dBlockedDomain := pBlock.Explain(ctx, "malicious.org:443")
	if dBlockedDomain.Allowed || !strings.Contains(dBlockedDomain.Reason, "blocked") {
		t.Errorf("expected malicious.org to be blocked, got %+v", dBlockedDomain)
	}
	dBlockedWild := pBlock.Explain(ctx, "sub.evil.com:443")
	if dBlockedWild.Allowed || !strings.Contains(dBlockedWild.Reason, "blocked") {
		t.Errorf("expected sub.evil.com to be blocked, got %+v", dBlockedWild)
	}
	dBlockedIP := pBlock.Explain(ctx, "198.51.100.1:443")
	if dBlockedIP.Allowed || !strings.Contains(dBlockedIP.Reason, "blocked") {
		t.Errorf("expected 198.51.100.1 to be blocked, got %+v", dBlockedIP)
	}
}

func TestProxyServerConnectFlow(t *testing.T) {
	// Start a dummy backend echo TCP server
	backendListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer backendListener.Close()
	backendPort := backendListener.Addr().(*net.TCPAddr).Port

	go func() {
		for {
			conn, err := backendListener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_, _ = io.Copy(c, c)
			}(conn)
		}
	}()

	// Start proxy allowing backend port as loopback relay
	proxyServer, err := NewServer(Config{
		Tier:          TierAllowlist,
		AllowedHosts:  []string{"allowed.internal"},
		LoopbackPorts: []int{backendPort},
		HostGateway:   "host.docker.internal",
		SessionToken:  "test-secret-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := proxyServer.Start("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	defer proxyServer.Close()

	proxyAddr := fmt.Sprintf("127.0.0.1:%d", proxyServer.Port())

	// 1. Unauthenticated request should fail with 407
	conn, err := net.Dial("tcp", proxyAddr)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fmt.Fprintf(conn, "CONNECT host.docker.internal:%d HTTP/1.1\r\nHost: host.docker.internal:%d\r\n\r\n", backendPort, backendPort)
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if resp.StatusCode != http.StatusProxyAuthRequired {
		t.Fatalf("expected 407 Proxy Authentication Required, got %d", resp.StatusCode)
	}

	// 2. Disallowed host with valid auth should fail with 403
	conn, err = net.Dial("tcp", proxyAddr)
	if err != nil {
		t.Fatal(err)
	}
	authHeader := "Basic " + base64.StdEncoding.EncodeToString([]byte("construct:test-secret-token"))
	_, _ = fmt.Fprintf(conn, "CONNECT forbidden.internal:443 HTTP/1.1\r\nHost: forbidden.internal:443\r\nProxy-Authorization: %s\r\n\r\n", authHeader)
	resp, err = http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden, got %d", resp.StatusCode)
	}
	if reason := resp.Header.Get("X-Construct-Reason"); reason == "" {
		t.Errorf("expected X-Construct-Reason header on 403 response")
	}

	// 3. Allowed loopback destination should succeed with 200 Connection Established
	conn, err = net.Dial("tcp", proxyAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _ = fmt.Fprintf(conn, "CONNECT host.docker.internal:%d HTTP/1.1\r\nHost: host.docker.internal:%d\r\nProxy-Authorization: %s\r\n\r\n", backendPort, backendPort, authHeader)
	reader := bufio.NewReader(conn)
	resp, err = http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 Connection Established, got %d", resp.StatusCode)
	}

	// Verify bidirectional communication over tunnel
	testMessage := "Hello via proxy tunnel\n"
	_, _ = conn.Write([]byte(testMessage))
	reply, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("failed to read from tunnel: %v", err)
	}
	if reply != testMessage {
		t.Errorf("expected echo %q, got %q", testMessage, reply)
	}
}

func TestProxyHTTPClientWithProxy(t *testing.T) {
	// Start an echo HTTP server
	httpBackend := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("backend-ok"))
		}),
	}
	backendL, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer backendL.Close()
	go func() { _ = httpBackend.Serve(backendL) }()

	backendPort := backendL.Addr().(*net.TCPAddr).Port

	// Start proxy
	proxyServer, err := NewServer(Config{
		Tier:          TierAllowlist,
		AllowedHosts:  []string{"localhost"},
		LoopbackPorts: []int{backendPort},
		SessionToken:  "bearer-token-123",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := proxyServer.Start("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	defer proxyServer.Close()

	proxyURL, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", proxyServer.Port()))
	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
		},
	}

	req, _ := http.NewRequest("GET", fmt.Sprintf("http://localhost:%d/test", backendPort), nil)
	req.Header.Set("Proxy-Authorization", "Bearer bearer-token-123")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do failed: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "backend-ok" {
		t.Errorf("expected 'backend-ok', got %q", string(body))
	}

	// Plaintext HTTP request with sentinel must be dropped with 403 (H7)
	reqSentinel, _ := http.NewRequest("GET", fmt.Sprintf("http://localhost:%d/test", backendPort), nil)
	reqSentinel.Header.Set("Proxy-Authorization", "Bearer bearer-token-123")
	reqSentinel.Header.Set("Authorization", "Bearer cnstr_sentinel_auth_key")
	respSentinel, err := client.Do(reqSentinel)
	if err != nil {
		t.Fatalf("client.Do failed: %v", err)
	}
	defer respSentinel.Body.Close()
	if respSentinel.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden for plaintext HTTP sentinel transmission, got %d", respSentinel.StatusCode)
	}
}

func TestProxyCONNECTInterceptionAndSecretSwap(t *testing.T) {
	caDir := t.TempDir()
	ca, err := EnsureCA(caDir)
	if err != nil {
		t.Fatal(err)
	}

	certPool := x509.NewCertPool()
	certPool.AppendCertsFromPEM(ca.CertPEM())

	backendCert, err := ca.SignHostCert("api.example.com")
	if err != nil {
		t.Fatal(err)
	}

	var receivedAuthHeader string
	httpsBackend := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			receivedAuthHeader = r.Header.Get("Authorization")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("https-backend-ok"))
		}),
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{*backendCert},
			ClientCAs:    certPool,
		},
	}

	backendL, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer backendL.Close()
	tlsBackendL := tls.NewListener(backendL, httpsBackend.TLSConfig)
	go func() { _ = httpsBackend.Serve(tlsBackendL) }()

	backendPort := backendL.Addr().(*net.TCPAddr).Port

	// Start proxy with CA, UpstreamCAs, and loopback port
	proxyServer, err := NewServer(Config{
		Tier:          TierAllowlist,
		AllowedHosts:  []string{"api.example.com"},
		LoopbackPorts: []int{backendPort},
		HostGateway:   "api.example.com",
		SessionToken:  "test-mitm-token",
		CADir:         caDir,
		UpstreamCAs:   certPool,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := proxyServer.Start("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	defer proxyServer.Close()

	sentinel := proxyServer.RegisterSecret("ANTHROPIC_API_KEY", "real-secret-key-12345", "api.example.com")

	proxyURL, _ := url.Parse(fmt.Sprintf("http://construct:test-mitm-token@127.0.0.1:%d", proxyServer.Port()))
	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
			TLSClientConfig: &tls.Config{
				RootCAs: certPool,
			},
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				// Re-route api.example.com to our local listener port while keeping the host header
				if strings.HasPrefix(addr, "api.example.com:") {
					return (&net.Dialer{}).DialContext(ctx, network, fmt.Sprintf("127.0.0.1:%d", backendPort))
				}
				return (&net.Dialer{}).DialContext(ctx, network, addr)
			},
		},
	}

	req, _ := http.NewRequest("GET", fmt.Sprintf("https://api.example.com:%d/v1/messages", backendPort), nil)
	req.Header.Set("Authorization", "Bearer "+sentinel)

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("CONNECT MITM request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	if receivedAuthHeader != "Bearer real-secret-key-12345" {
		t.Errorf("expected upstream to receive swapped real secret, got %q", receivedAuthHeader)
	}
}

func TestProxyDomainFrontingDropped(t *testing.T) {
	caDir := t.TempDir()
	ca, err := EnsureCA(caDir)
	if err != nil {
		t.Fatal(err)
	}

	certPool := x509.NewCertPool()
	certPool.AppendCertsFromPEM(ca.CertPEM())

	backendCert, err := ca.SignHostCert("api.example.com")
	if err != nil {
		t.Fatal(err)
	}

	httpsBackend := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{*backendCert},
		},
	}

	backendL, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer backendL.Close()
	tlsBackendL := tls.NewListener(backendL, httpsBackend.TLSConfig)
	go func() { _ = httpsBackend.Serve(tlsBackendL) }()

	backendPort := backendL.Addr().(*net.TCPAddr).Port

	proxyServer, err := NewServer(Config{
		Tier:          TierAllowlist,
		AllowedHosts:  []string{"api.example.com"},
		LoopbackPorts: []int{backendPort},
		HostGateway:   "api.example.com",
		SessionToken:  "fronting-token",
		CADir:         caDir,
		UpstreamCAs:   certPool,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := proxyServer.Start("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	defer proxyServer.Close()

	// 1. TLS SNI fronting attempt: CONNECT specifies api.example.com, but ClientHello SNI is evil.com
	proxyAddr := fmt.Sprintf("127.0.0.1:%d", proxyServer.Port())
	conn, err := net.Dial("tcp", proxyAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	authHeader := "Basic " + base64.StdEncoding.EncodeToString([]byte("construct:fronting-token"))
	_, _ = fmt.Fprintf(conn, "CONNECT api.example.com:%d HTTP/1.1\r\nHost: api.example.com:%d\r\nProxy-Authorization: %s\r\n\r\n", backendPort, backendPort, authHeader)
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, nil)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 Connection Established, got %v", resp)
	}

	// Try TLS handshake with mismatching SNI evil.com
	tlsClient := tls.Client(conn, &tls.Config{
		ServerName: "evil.com",
		RootCAs:    certPool,
	})
	err = tlsClient.Handshake()
	if err == nil {
		t.Fatalf("expected TLS handshake to fail due to domain fronting SNI mismatch")
	}
}

func TestPolicyAllowedTCP(t *testing.T) {
	ctx := context.Background()
	p := NewPolicy(TierAllowlist, []string{"my-api.example.com"}, nil, "", []string{
		"postgres.internal:5432",
		"*.rds.amazonaws.com:5432",
		"127.0.0.1:6379",
	})

	if !p.IsTCPAllowed("postgres.internal", 5432) {
		t.Errorf("expected postgres.internal:5432 to be allowed")
	}
	if p.IsTCPAllowed("postgres.internal", 5433) {
		t.Errorf("expected postgres.internal:5433 to be denied")
	}
	if !p.IsTCPAllowed("mydb.rds.amazonaws.com", 5432) {
		t.Errorf("expected wildcard rds match to be allowed")
	}
	if p.IsTCPAllowed("other.example.com", 5432) {
		t.Errorf("expected other.example.com:5432 to be denied")
	}

	if !p.IsRawTCP("postgres.internal", 5432) {
		t.Errorf("expected postgres.internal:5432 to be raw TCP")
	}
	if p.IsRawTCP("my-api.example.com", 443) {
		t.Errorf("expected 443 on allowlisted host to not be raw TCP")
	}
	if p.IsRawTCP("my-api.example.com", 8080) {
		t.Errorf("expected allowlisted web host to not be raw TCP")
	}
	if !p.IsRawTCP("other-service.net", 8080) {
		t.Errorf("expected unlisted host on port != 443 to be raw TCP")
	}

	d := p.Explain(ctx, "127.0.0.1:6379")
	if !d.Allowed {
		t.Errorf("expected 127.0.0.1:6379 to be allowed, got %+v", d)
	}
	if d.Source != "allowed_tcp" {
		t.Errorf("expected source 'allowed_tcp', got %q", d.Source)
	}

	dBlocked := p.Explain(ctx, "unallowed.internal:5432")
	if dBlocked.Allowed {
		t.Errorf("expected unallowed.internal:5432 to be blocked")
	}
}

func TestProxyServerRawTCPTunnel(t *testing.T) {
	backendListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer backendListener.Close()
	backendPort := backendListener.Addr().(*net.TCPAddr).Port

	go func() {
		for {
			conn, err := backendListener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_, _ = io.Copy(c, c)
			}(conn)
		}
	}()

	logDir := t.TempDir()
	proxyServer, err := NewServer(Config{
		Tier:         TierAllowlist,
		AllowedTCP:   []string{fmt.Sprintf("127.0.0.1:%d", backendPort)},
		SessionToken: "tcp-test-token",
		LogDir:       logDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := proxyServer.Start("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	defer proxyServer.Close()

	proxyAddr := fmt.Sprintf("127.0.0.1:%d", proxyServer.Port())
	conn, err := net.Dial("tcp", proxyAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	authHeader := "Basic " + base64.StdEncoding.EncodeToString([]byte("construct:tcp-test-token"))
	target := fmt.Sprintf("127.0.0.1:%d", backendPort)
	_, _ = fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\nProxy-Authorization: %s\r\n\r\n", target, target, authHeader)
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, nil)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 Connection Established, got %v", resp)
	}

	msg := "PING RAW TCP STREAM\n"
	if _, err := conn.Write([]byte(msg)); err != nil {
		t.Fatal(err)
	}

	reply, err := reader.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if reply != msg {
		t.Fatalf("expected echo %q, got %q", msg, reply)
	}
}

package proxy

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
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
}

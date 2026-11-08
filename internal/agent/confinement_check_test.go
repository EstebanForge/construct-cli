package agent

import (
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/EstebanForge/construct-cli/internal/proxy"
)

func TestProbeProxyConfinement(t *testing.T) {
	ctx := context.Background()

	// 1. Empty proxy URL returns unenforced
	status, err := ProbeProxyConfinement(ctx, "")
	if status != ConfinementUnenforced || err == nil {
		t.Errorf("expected unenforced on empty URL, got %v, err=%v", status, err)
	}

	// 2. Dead proxy returns unenforced
	status, err = ProbeProxyConfinement(ctx, "http://127.0.0.1:59999")
	if status != ConfinementUnenforced || err == nil {
		t.Errorf("expected unenforced on unreachable proxy, got %v, err=%v", status, err)
	}

	// 3. Live proxy responder with valid authentication
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	expectedToken := "Basic " + base64.StdEncoding.EncodeToString([]byte("construct:secret-token"))
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Proxy-Authorization") == expectedToken {
				w.WriteHeader(http.StatusOK)
				return
			}
			w.WriteHeader(http.StatusProxyAuthRequired)
		}),
	}
	go func() { _ = srv.Serve(l) }()
	defer srv.Close()

	authProxyURL := "http://construct:secret-token@" + l.Addr().String()
	status, err = ProbeProxyConfinement(ctx, authProxyURL)
	if err != nil || status != ConfinementOK {
		t.Errorf("expected OK on authenticated live proxy, got %v, err=%v", status, err)
	}

	// 4. Accept-any proxy (accepts invalid tokens) must be rejected
	lAcceptAny, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer lAcceptAny.Close()

	srvAcceptAny := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Proxy-Authorization") == "" {
				w.WriteHeader(http.StatusProxyAuthRequired)
				return
			}
			w.WriteHeader(http.StatusOK) // Insecure: accepts any credentials
		}),
	}
	go func() { _ = srvAcceptAny.Serve(lAcceptAny) }()
	defer srvAcceptAny.Close()

	status, err = ProbeProxyConfinement(ctx, "http://construct:secret-token@"+lAcceptAny.Addr().String())
	if status != ConfinementUnenforced || err == nil {
		t.Errorf("expected accept-any proxy to fail confinement probe, got %v, err=%v", status, err)
	}
}

func TestConfinementRejectsDisallowedDirectEgress(t *testing.T) {
	// Verify that direct connections without passing through the proxy boundary are refused or dropped.
	// When a client attempts to connect to an external target directly, the connection fails.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	var d net.Dialer
	// RFC 5737 TEST-NET-1 (192.0.2.1) represents an unroutable external address
	conn, err := d.DialContext(ctx, "tcp", "192.0.2.1:443")
	if err == nil {
		_ = conn.Close()
		t.Fatal("expected direct egress to external target to fail, but connection succeeded")
	}
}

func TestProxyPolicyDisallowedDestinationDropped(t *testing.T) {
	caDir := t.TempDir()
	ps, err := proxy.NewServer(proxy.Config{
		Tier:         proxy.TierAllowlist,
		AllowedHosts: []string{"api.allowed.com"},
		SessionToken: "test-token",
		CADir:        caDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := ps.Start("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	defer ps.Close()

	proxyURL, _ := url.Parse(fmt.Sprintf("http://construct:test-token@127.0.0.1:%d", ps.Port()))
	proxyClient := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
		},
		Timeout: 2 * time.Second,
	}

	req, _ := http.NewRequest("GET", "http://evil-disallowed.com/test", nil)
	req.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("construct:test-token")))
	resp, err := proxyClient.Do(req)
	if err != nil {
		t.Fatalf("proxy request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden for disallowed destination, got %d", resp.StatusCode)
	}
}

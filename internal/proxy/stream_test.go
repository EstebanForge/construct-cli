package proxy

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestStreamResponsesNotTruncatedAt30Seconds(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping slow streaming test in short mode")
	}

	caDir := t.TempDir()
	ca, err := EnsureCA(caDir)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(ca.CertPEM())
	leaf, err := ca.SignHostCert("api.example.com")
	if err != nil {
		t.Fatal(err)
	}

	backend := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("chunk1-"))
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			// Sleep 32 seconds to exceed previous 30s monolithic write deadline
			time.Sleep(32 * time.Second)
			_, _ = w.Write([]byte("chunk2"))
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
		}),
		TLSConfig: &tls.Config{Certificates: []tls.Certificate{*leaf}},
	}

	bl, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer bl.Close()
	go func() { _ = backend.Serve(tls.NewListener(bl, backend.TLSConfig)) }()

	port := bl.Addr().(*net.TCPAddr).Port
	ps, err := NewServer(Config{
		Tier:          TierAllowlist,
		AllowedHosts:  []string{"api.example.com"},
		LoopbackPorts: []int{port},
		HostGateway:   "api.example.com",
		SessionToken:  "test-stream-token",
		CADir:         caDir,
		UpstreamCAs:   pool,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := ps.Start("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	defer ps.Close()

	pu, _ := url.Parse(fmt.Sprintf("http://construct:test-stream-token@127.0.0.1:%d", ps.Port()))
	c := &http.Client{
		Timeout: 60 * time.Second,
		Transport: &http.Transport{
			Proxy: http.ProxyURL(pu),
			TLSClientConfig: &tls.Config{
				RootCAs: pool,
			},
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				if strings.HasPrefix(addr, "api.example.com:") {
					return (&net.Dialer{}).DialContext(ctx, network, fmt.Sprintf("127.0.0.1:%d", port))
				}
				return (&net.Dialer{}).DialContext(ctx, network, addr)
			},
		},
	}

	resp, err := c.Get(fmt.Sprintf("https://api.example.com:%d/stream", port))
	if err != nil {
		t.Fatalf("get request failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading response body failed: %v", err)
	}

	if string(body) != "chunk1-chunk2" {
		t.Fatalf("expected 'chunk1-chunk2', got %q", string(body))
	}
}

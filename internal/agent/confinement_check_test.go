package agent

import (
	"context"
	"encoding/base64"
	"net"
	"net/http"
	"testing"
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

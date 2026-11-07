package agent

import (
	"context"
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

	// 3. Live proxy responder returns OK
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Proxy-Authorization") != "" {
				w.WriteHeader(http.StatusOK)
				return
			}
			w.WriteHeader(http.StatusProxyAuthRequired)
		}),
	}
	go func() { _ = srv.Serve(l) }()
	defer srv.Close()

	proxyURL := "http://" + l.Addr().String()
	status, err = ProbeProxyConfinement(ctx, proxyURL)
	if err != nil || status != ConfinementOK {
		t.Errorf("expected OK on live proxy, got %v, err=%v", status, err)
	}

	// 4. Authenticated probe URL
	authProxyURL := "http://construct:secret-token@" + l.Addr().String()
	status, err = ProbeProxyConfinement(ctx, authProxyURL)
	if err != nil || status != ConfinementOK {
		t.Errorf("expected OK on authenticated live proxy, got %v, err=%v", status, err)
	}
}

package clipboard

import (
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestStartServerOnServesGivenListener(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	srv, err := StartServerOn(listener, "host.microsandbox.internal")
	if err != nil {
		t.Fatalf("StartServerOn: %v", err)
	}
	defer srv.Stop()

	port := listener.Addr().(*net.TCPAddr).Port
	if srv.Port != port {
		t.Fatalf("Port = %d, want listener port %d", srv.Port, port)
	}
	if srv.URL != fmt.Sprintf("http://host.microsandbox.internal:%d", port) {
		t.Fatalf("URL = %q, want urlHost preserved with listener port", srv.URL)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	// srv.URL carries the guest-side hostname; request the listener directly.
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/paste?type=text/plain", port))
	if err != nil {
		t.Fatalf("get /paste: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 without token", resp.StatusCode)
	}
}

func TestStartServerOnRejectsNilListener(t *testing.T) {
	if _, err := StartServerOn(nil, "host.docker.internal"); err == nil {
		t.Fatal("StartServerOn(nil) succeeded, want error")
	}
}

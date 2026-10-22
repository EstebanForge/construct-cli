package hostexec

import (
	"fmt"
	"net"
	"testing"
)

func TestStartServerOnUsesGivenListener(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	srv, err := StartServerOn(listener, "host.microsandbox.internal", nil, DefaultTimeout, nil)
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
}

func TestStartServerOnRejectsNilListener(t *testing.T) {
	if _, err := StartServerOn(nil, "host.docker.internal", nil, DefaultTimeout, nil); err == nil {
		t.Fatal("StartServerOn(nil) succeeded, want error")
	}
}

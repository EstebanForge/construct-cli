package agent

import (
	"testing"

	"github.com/EstebanForge/construct-cli/internal/config"
	"github.com/EstebanForge/construct-cli/internal/runtime"
)

func TestBridgeURLHost(t *testing.T) {
	tests := []struct {
		name     string
		backend  string
		override string
		want     string
	}{
		{"msb ignores docker override", config.BackendMsb, "host.docker.internal", runtime.MsbHostAlias},
		{"msb without override", config.BackendMsb, "", runtime.MsbHostAlias},
		{"docker keeps override", config.BackendDocker, "host.orbstack.internal", "host.orbstack.internal"},
		{"docker default", config.BackendDocker, "", "host.docker.internal"},
		{"unknown backend default", "weird", "", "host.docker.internal"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := bridgeURLHost(tt.backend, tt.override); got != tt.want {
				t.Fatalf("bridgeURLHost(%q, %q) = %q, want %q", tt.backend, tt.override, got, tt.want)
			}
		})
	}
}

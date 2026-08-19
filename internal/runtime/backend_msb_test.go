package runtime

import (
	"context"
	"strings"
	"testing"

	"github.com/EstebanForge/construct-cli/internal/config"
)

func TestDetectBackendFailClosed(t *testing.T) {
	cfg := config.DefaultConfig()
	_ = &cfg
	cfg.Runtime.Backend = "bogus"
	if _, err := DetectBackend(&cfg); err == nil || !strings.Contains(err.Error(), "unknown runtime backend") {
		t.Fatalf("want unknown-backend error, got %v", err)
	}
	cfg.Runtime.Backend = "docker"
	b, err := DetectBackend(&cfg)
	if err != nil {
		t.Skipf("no container runtime on this host: %v", err)
	}
	if _, ok := b.(*DockerBackend); !ok {
		t.Fatalf("docker backend expected, got %T", b)
	}
}

func TestValidateBackendSelected(t *testing.T) {
	cfg := config.DefaultConfig()
	if err := ValidateBackendSelected(&cfg); err != nil {
		t.Fatalf("default backend should validate: %v", err)
	}
	cfg.Runtime.Backend = "docker"
	if err := ValidateBackendSelected(&cfg); err != nil {
		t.Fatalf("docker backend should validate: %v", err)
	}
	cfg.Runtime.Backend = "msb"
	if err := ValidateBackendSelected(&cfg); err == nil || !strings.Contains(err.Error(), "not yet wired") {
		t.Fatalf("msb should fail closed during Step 6, got %v", err)
	}
	cfg.Runtime.Backend = "bogus"
	if err := ValidateBackendSelected(&cfg); err == nil {
		t.Fatal("unknown backend should fail")
	}
}

func TestMsbBackendAvailable(t *testing.T) {
	m := NewMsbBackend()
	if ok, err := m.Available(context.TODO()); err != nil {
		t.Fatalf("Available error: %v", err)
	} else if !ok {
		t.Skip("msb not installed")
	}
}

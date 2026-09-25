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
	cfg.Runtime.Backend = "microvm"
	if err := ValidateBackendSelected(&cfg); err == nil || !strings.Contains(err.Error(), "does not support") {
		t.Fatalf("microvm should fail closed on compose-only paths, got %v", err)
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

// TestManifestDigestForHost: the registry digest resolver must return the
// linux/<host-arch> platform manifest digest on multi-arch tags (msb
// caches the platform manifest, never the index), skip unknown-platform
// attestation entries, and fall back to "" (keep cached) on anything it
// cannot resolve — a wrong answer here re-downloads the image on every
// create.
func TestManifestDigestForHost(t *testing.T) {
	indexJSON := `{"manifests":[
		{"digest":"sha256:attest0000000000000000000000000000000000000000000000000000000000","platform":{"architecture":"unknown","os":"unknown"}},
		{"digest":"sha256:linuxamd640000000000000000000000000000000000000000000000000000000","platform":{"architecture":"amd64","os":"linux"}},
		{"digest":"sha256:linuxarm640000000000000000000000000000000000000000000000000000000","platform":{"architecture":"arm64","os":"linux","variant":"v8"}},
		{"digest":"sha256:macarm6400000000000000000000000000000000000000000000000000000000","platform":{"architecture":"arm64","os":"darwin"}}
	]}`
	const indexType = "application/vnd.oci.image.index.v1+json"
	const manifestType = "application/vnd.oci.image.manifest.v1+json"
	const topDigest = "sha256:indextop000000000000000000000000000000000000000000000000000000000"
	const amd64Digest = "sha256:linuxamd640000000000000000000000000000000000000000000000000000000"
	const arm64Digest = "sha256:linuxarm640000000000000000000000000000000000000000000000000000000"

	cases := []struct {
		name        string
		contentType string
		body        string
		goarch      string
		want        string
	}{
		{"amd64 host picks linux/amd64", indexType, indexJSON, "amd64", amd64Digest},
		{"arm64 host picks linux/arm64 ignoring darwin and variant", indexType, indexJSON, "arm64", arm64Digest},
		{"unknown arch resolves to nothing", indexType, indexJSON, "riscv64", ""},
		{"single-arch manifest keeps top digest", manifestType, indexJSON, "amd64", topDigest},
		{"docker manifest list also resolves", "application/vnd.docker.distribution.manifest.list.v2+json", indexJSON, "amd64", amd64Digest},
		{"content type match is case-insensitive", "Application/VND.OCI.Image.Index.V1+JSON", indexJSON, "amd64", amd64Digest},
		{"garbage index body keeps cached", indexType, "{not json", "amd64", ""},
		{"index without any linux entry keeps cached", indexType, `{"manifests":[{"digest":"sha256:x","platform":{"architecture":"amd64","os":"windows"}}]}`, "amd64", ""},
		{"empty body keeps cached", indexType, "", "amd64", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := manifestDigestForHost(tc.contentType, []byte(tc.body), topDigest, tc.goarch)
			if got != tc.want {
				t.Fatalf("manifestDigestForHost = %q, want %q", got, tc.want)
			}
		})
	}
}

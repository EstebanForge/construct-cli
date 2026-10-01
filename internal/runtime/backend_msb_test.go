package runtime

import (
	"context"
	"errors"
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

// TestEnsureImagePrefersLocalBuild: a localhost/ ref only lands via an
// explicit local build + transition (`construct sys rebuild` / `sys init`
// on this backend), so EnsureImage must adopt it WITHOUT consulting GHCR —
// even when a stale GHCR ref is ALSO cached (the normal state after any
// prior run pulled from the registry). The digest drift check would see a
// mismatch for a local build and re-pull the stale published image over
// the fresh one. Proven by the early nil return: without the preference
// the flow falls through to pull / fallback / build-confirm and fails
// closed on this exec-less test host.
func TestEnsureImagePrefersLocalBuild(t *testing.T) {
	origCached := msbImageCachedFn
	msbImageCachedFn = func(ref string) bool {
		return ref == "localhost/construct-box:latest" || ref == "ghcr.io/estebanforge/construct-box:latest"
	}
	t.Cleanup(func() { msbImageCachedFn = origCached })

	cfg := config.DefaultConfig()
	ref, err := (&MsbBackend{}).EnsureImage(&cfg)
	if err != nil {
		t.Fatalf("EnsureImage must adopt the cached local build, got: %v", err)
	}
	if ref != "localhost/construct-box:latest" {
		t.Fatalf("EnsureImage must return the local build ref for the boot spec, got %q", ref)
	}
}

// TestEnsureImageCachedDigestSkipsPull: when the GHCR ref is cached and the
// registry digest matches (or is unverifiable), msb pull is a guaranteed
// no-op — it never re-resolves a cached tag — so EnsureImage must print the
// ready line WITHOUT spawning a pull or the "Pulling" download line. Proven
// by the stubbed pull error: ANY reach of the pull block fails the test on
// every host, exec-less or not.
func TestEnsureImageCachedDigestSkipsPull(t *testing.T) {
	origDigest, origRemote, origCached := msbImageDigestFn, ghcrRemoteDigestFn, msbImageCachedFn
	origRun := runMsbCmd
	t.Cleanup(func() {
		msbImageDigestFn, ghcrRemoteDigestFn, msbImageCachedFn = origDigest, origRemote, origCached
		runMsbCmd = origRun
	})

	cases := []struct {
		name   string
		remote string
	}{
		{"digest matches registry", "sha256:same"},
		{"registry digest unverifiable (offline)", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msbImageDigestFn = func(string) string { return "sha256:same" }
			ghcrRemoteDigestFn = func(string) string { return tc.remote }
			msbImageCachedFn = func(ref string) bool { return ref == PrepullImageRef }
			runMsbCmd = func(string, ...string) ([]byte, error) {
				return nil, errors.New("stub: pull block must not run when the ref is cached")
			}

			cfg := config.DefaultConfig()
			ref, err := (&MsbBackend{}).EnsureImage(&cfg)
			if err != nil {
				t.Fatalf("cached ref must short-circuit to ready without a pull, got: %v", err)
			}
			if ref != PrepullImageRef {
				t.Fatalf("cached ref must return the verified GHCR ref for the boot spec, got %q", ref)
			}
		})
	}
}

// TestEnsureImageDigestDriftFallsThroughToPull: a digest mismatch must fall
// through to the pull (after the rm), never short-circuit to ready. Proven
// by the fail-closed error at the END of the flow: drift → rm → pull error
// (hermetic seam; the rm+pull execs hit a LIVE msb store on dev hosts if
// left real) → cached fallback stubbed empty → local image stubbed absent →
// build confirm declined by the stubbed prompt.
func TestEnsureImageDigestDriftFallsThroughToPull(t *testing.T) {
	origDigest, origRemote := msbImageDigestFn, ghcrRemoteDigestFn
	origCached, origLocalExists := msbImageCachedFn, localConstructImageExistsFn
	origTTY, origConfirm := stdinIsTerminal, confirmPrompt
	origRun := runMsbCmd
	t.Cleanup(func() {
		msbImageDigestFn, ghcrRemoteDigestFn = origDigest, origRemote
		msbImageCachedFn, localConstructImageExistsFn = origCached, origLocalExists
		stdinIsTerminal, confirmPrompt = origTTY, origConfirm
		runMsbCmd = origRun
	})

	msbImageDigestFn = func(string) string { return "sha256:old" }
	ghcrRemoteDigestFn = func(string) string { return "sha256:new" }
	msbImageCachedFn = func(string) bool { return false } // localhost check AND imageLoaded fallback
	localConstructImageExistsFn = func(*config.Config) bool { return false }
	stdinIsTerminal = func() bool { return true }
	confirmPrompt = func(string) bool { return false } // declined → fail-closed error
	runMsbCmd = func(string, ...string) ([]byte, error) { return nil, errors.New("stub: pull refused") }

	cfg := config.DefaultConfig()
	if _, err := (&MsbBackend{}).EnsureImage(&cfg); err == nil {
		t.Fatal("digest drift must fall through to the pull, not short-circuit to ready")
	}
}

// TestMsbConstructImageRefPrefersLocalBuildOverGhcr: when both the stale
// GHCR ref and a deliberate local build are cached, the boot image and
// daemon digest label must resolve to the LOCAL build. The candidates
// list used to order the registry ref first, so the sandbox kept booting
// the stale GHCR image even after a successful local build + transition.
func TestMsbConstructImageRefPrefersLocalBuildOverGhcr(t *testing.T) {
	origCached := msbImageCachedFn
	msbImageCachedFn = func(ref string) bool {
		return ref == "localhost/construct-box:latest" || ref == "ghcr.io/estebanforge/construct-box:latest"
	}
	t.Cleanup(func() { msbImageCachedFn = origCached })

	if got := MsbConstructImageRef(); got != "localhost/construct-box:latest" {
		t.Fatalf("MsbConstructImageRef() = %q, want the deliberate local build ref", got)
	}
}

// TestTransitionLocalConstructImageToMsbNoLocalImage: without a locally
// built docker image the transition is a no-op success — the GHCR pull
// remains the acquisition path and `sys rebuild` must not fail on a
// machine whose docker store has no construct-box image.
func TestTransitionLocalConstructImageToMsbNoLocalImage(t *testing.T) {
	orig := localConstructImageExistsFn
	localConstructImageExistsFn = func(*config.Config) bool { return false }
	t.Cleanup(func() { localConstructImageExistsFn = orig })

	cfg := config.DefaultConfig()
	if err := TransitionLocalConstructImageToMsb(&cfg); err != nil {
		t.Fatalf("no local image must no-op with nil, got: %v", err)
	}
}

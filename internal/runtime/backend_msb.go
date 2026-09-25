package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	msb "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/EstebanForge/construct-cli/internal/config"
	"github.com/EstebanForge/construct-cli/internal/ui"
)

// ErrMsbUnsupported marks primitives with no msb equivalent yet (Step 6
// MVP scope; docs/VMs.md §7). Callers surface these as clear errors, not
// silent fallbacks.
var ErrMsbUnsupported = errors.New("unsupported in the msb backend (experimental) — use the docker backend for this feature")

// MsbBackend implements Backend over microsandbox microVMs (opt-in,
// experimental; docs/VMs.md). Sandboxes are managed through the msb Go
// SDK; image transition reuses the Docker image via save+load.
type MsbBackend struct{}

// NewMsbBackend returns the microsandbox Backend.
func NewMsbBackend() *MsbBackend { return &MsbBackend{} }

// Name returns the backend identifier.
func (m *MsbBackend) Name() string { return "microvm" }

// Available reports whether the msb runtime is installed.
func (m *MsbBackend) Available(_ context.Context) (bool, error) {
	if _, err := exec.LookPath("msb"); err == nil {
		return true, nil
	}
	return msb.IsRuntimeInstalled(msb.RuntimeConfig{}), nil
}

// constructImageRefCandidates lists the refs the construct image may be
// cached under, in preference order. msb has no `image tag` subcommand
// (checked 0.6.15) and refs are stored as given: `msb pull` caches the
// full registry ref, `msb load -i` imports archives as localhost/<name>.
// The bare name stays first for msb builds that resolve short names.
var constructImageRefCandidates = []string{
	"construct-box:latest",
	"ghcr.io/estebanforge/construct-box:latest",
	"localhost/construct-box:latest",
}

// MsbConstructImageRef returns the first cached construct-box ref by
// probing `msb image inspect` per candidate. Falls back to the bare
// canonical ref (msb then reports its own "image not found", which is
// more actionable than a construct-side guess) when nothing is cached or
// msb is unavailable (unit tests).
func MsbConstructImageRef() string {
	for _, ref := range constructImageRefCandidates {
		if msbImageCached(ref) {
			return ref
		}
	}
	return "construct-box:latest"
}

func msbImageCached(ref string) bool {
	cmd := exec.Command("msb", "image", "inspect", ref)
	cmd.Stdin = nil // msb stdin trap: open pipe hangs (docs/VMs.md §7.1)
	return cmd.Run() == nil
}

// EnsureImage transitions the construct image into msb: always attempts
// pulling the published image (PrepullImageRef) first — msb pull no-ops
// cheaply when the cached digest matches, so this is also the refresh path —
// then reuses a local docker/podman image when present, and otherwise builds
// only after a user confirmation. It then transitions via container-runtime
// save + msb load.
func (m *MsbBackend) EnsureImage(cfg *config.Config) error {
	ui.InfoLn("Preparing microVM image (construct-box:latest)...")

	// Refresh on digest drift: msb pull no-ops whenever the ref is cached
	// at all — it never re-resolves the tag — so neither name-presence nor
	// a plain pull can ever refresh, and a stale image shadows every
	// republish (guests kept booting the pre-free-sudo image for days).
	// Compare the cached digest against the registry manifest digest
	// (anonymous HEAD, ~1s) and force a re-download only on drift; when
	// either side is unknown (offline, private repo) keep whatever is
	// cached. The pull caches the FULL registry ref (no `image tag`
	// subcommand exists to alias it down to the bare name); imageLoaded and
	// the run spec resolve cached refs via constructImageRefCandidates.
	if cached, remote := msbCachedImageDigest(), ghcrRemoteDigest(PrepullImageRef); cached != "" && remote != "" && cached != remote {
		ui.InfoF("→ construct-box image changed upstream (%s → %s); pulling update (~2 GiB)...\n", abbrevDigest(cached), abbrevDigest(remote))
		rm := exec.Command("msb", "image", "rm", PrepullImageRef)
		rm.Stdin = nil // msb stdin trap: caller stdin must not stay open (§7.1)
		_ = rm.Run()   //nolint:errcheck // best-effort clear before the pull below
	}
	ui.InfoLn("→ Attempting to pull construct-box image from GHCR...")
	pull := exec.Command("msb", "pull", PrepullImageRef)
	pull.Stdin = nil
	if _, err := pull.CombinedOutput(); err == nil && msbImageCached(PrepullImageRef) {
		ui.InfoLn("✓ MicroVM image ready (from GHCR)")
		return nil
	}

	// Pull failed (offline, rate limited): fall back to any cached ref,
	// flagging that it may be stale.
	if m.imageLoaded() {
		ui.InfoLn("⚠️  Pull failed; using cached construct-box image (may be stale)")
		return nil
	}

	// Reuse a local docker/podman image when present; otherwise the local
	// build only runs after an explicit user confirmation (engine-uniform
	// acquisition flow, see image_resolve.go).
	if !LocalConstructImageExists(cfg) {
		if err := ConfirmConstructImageBuild(); err != nil {
			return err
		}
		BuildImage(cfg)
	}

	ui.InfoLn("→ Transitioning local Docker image to microVM (docker save + msb load, ~3.5GB)...")
	tmp, err := os.CreateTemp("", "construct-box-*.tar")
	if err != nil {
		return fmt.Errorf("msb image transition: %w", err)
	}
	defer func() {
		_ = os.Remove(tmp.Name()) //nolint:errcheck // best-effort cleanup of the transition archive
	}()
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("msb image transition: %w", err)
	}

	save := exec.Command(imageCliBinary(ResolveContainerRuntime(cfg)), "save", "-o", tmp.Name(), "construct-box:latest")
	if out, err := save.CombinedOutput(); err != nil {
		return fmt.Errorf("docker save construct-box: %w: %s", err, out)
	}
	load := exec.Command("msb", "load", "-i", tmp.Name())
	load.Stdin = nil // msb stdin trap: caller stdin must not stay open (§7.1)
	if out, err := load.CombinedOutput(); err != nil {
		return fmt.Errorf("msb load: %w: %s", err, out)
	}
	// `msb load -i` imports under the localhost/ prefix
	// (localhost/construct-box:latest). Verify the import landed so the
	// next EnsureImage skips the transition; imageLoaded and the run spec
	// resolve the localhost ref via constructImageRefCandidates.
	if !msbImageCached("localhost/construct-box:latest") {
		return fmt.Errorf("msb load reported success but localhost/construct-box:latest is not cached; run `msb image ls` to inspect the store")
	}
	ui.InfoLn("✓ MicroVM image loaded")
	return nil
}

// imageLoaded probes msb for the construct image under any of its cached
// refs (bare, localhost/, full registry).
func (m *MsbBackend) imageLoaded() bool {
	for _, ref := range constructImageRefCandidates {
		if msbImageCached(ref) {
			return true
		}
	}
	return false
}

// msbCachedImageDigest returns the full manifest digest of the cached
// construct image (parsed from `msb image inspect`), or "" when no ref is
// cached or the output cannot be parsed.
func msbCachedImageDigest() string {
	for _, candidate := range constructImageRefCandidates {
		// One inspect per candidate doubles as the cached check: a missing
		// ref fails the command, so fall through to the next candidate form
		// instead of a separate msbImageCached subprocess.
		out, err := exec.Command("msb", "image", "inspect", candidate).CombinedOutput()
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(out), "\n") {
			if digest, ok := strings.CutPrefix(line, "Digest:"); ok {
				return strings.TrimSpace(digest)
			}
		}
		return ""
	}
	return ""
}

// ghcrRemoteDigest resolves the current registry digest for a ghcr.io ref
// via an anonymous token plus a GET, without downloading layers. For a
// multi-arch tag the top-level digest is an OCI index, but msb stores the
// PLATFORM manifest digest (image inspect reports Architecture), so the
// index is resolved down to this machine's GOOS/GOARCH entry — comparing
// index vs platform digests would report drift on every create and
// re-download the image each time. Returns "" on any failure so callers
// keep the cached image rather than forcing a pull they cannot verify.
func ghcrRemoteDigest(ref string) string {
	tail, ok := strings.CutPrefix(ref, "ghcr.io/")
	if !ok {
		return ""
	}
	repo, tag, found := strings.Cut(tail, ":")
	if !found {
		repo, tag = tail, "latest"
	}
	client := &http.Client{Timeout: 15 * time.Second}
	tr, err := client.Get("https://ghcr.io/token?scope=repository:" + repo + ":pull")
	if err != nil {
		return ""
	}
	defer tr.Body.Close() //nolint:errcheck // read-side close, nothing to handle
	var tok struct {
		Token string `json:"token"`
	}
	if json.NewDecoder(tr.Body).Decode(&tok) != nil || tok.Token == "" {
		return ""
	}
	req, err := http.NewRequest(http.MethodGet, "https://ghcr.io/v2/"+repo+"/manifests/"+tag, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Authorization", "Bearer "+tok.Token)
	req.Header.Set("Accept", strings.Join([]string{
		"application/vnd.oci.image.index.v1+json",
		"application/vnd.docker.distribution.manifest.list.v2+json",
		"application/vnd.oci.image.manifest.v1+json",
		"application/vnd.docker.distribution.manifest.v2+json",
	}, ", "))
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close() //nolint:errcheck // small manifest, close on return
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	digest := resp.Header.Get("Docker-Content-Digest")
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return ""
	}
	return manifestDigestForHost(resp.Header.Get("Content-Type"), body, digest, runtime.GOARCH)
}

// manifestDigestForHost resolves the digest msb actually caches for this
// machine: the construct-box guest image is ALWAYS linux, but the machine
// architecture follows the host (libkrun runs the VM on host arch), so a
// darwin/arm64 host caches the linux/arm64 platform manifest while the
// tag-level digest is the multi-arch index. On index content the top
// digest is the index, which NEVER equals the cached platform digest —
// returning it would report drift on every create — so any resolution
// failure returns "" (unknown, keep the cached image) instead. goarch is
// a parameter for testability; callers pass runtime.GOARCH.
func manifestDigestForHost(contentType string, body []byte, topDigest, goarch string) string {
	lower := strings.ToLower(contentType)
	if !strings.Contains(lower, "index") && !strings.Contains(lower, "manifest.list") {
		return topDigest // single-arch tag: the top digest is already the manifest
	}
	var idx struct {
		Manifests []struct {
			Digest   string `json:"digest"`
			Platform struct {
				OS           string `json:"os"`
				Architecture string `json:"architecture"`
			} `json:"platform"`
		} `json:"manifests"`
	}
	if json.Unmarshal(body, &idx) != nil {
		return ""
	}
	for _, m := range idx.Manifests {
		if m.Platform.OS == "linux" && m.Platform.Architecture == goarch {
			return m.Digest
		}
	}
	return ""
}

// abbrevDigest shortens a sha256:... digest for one-line notices.
func abbrevDigest(digest string) string {
	if len(digest) > 19 {
		return digest[:19]
	}
	return digest
}

// Exec runs a command inside a running sandbox. Exit-code fidelity: the SDK
// returns non-zero exit codes in ExecOutput, not as errors.
func (m *MsbBackend) Exec(ctx context.Context, opts ExecOptions) (string, int, error) {
	sb, err := m.connect(ctx, opts.Name)
	if err != nil {
		return "", 1, err
	}
	defer func() {
		_ = sb.Close() //nolint:errcheck // best-effort release of the SDK handle
	}()

	var eopts []msb.ExecOption
	if opts.User != "" {
		eopts = append(eopts, msb.WithExecUser(opts.User))
	}
	if opts.Workdir != "" {
		eopts = append(eopts, msb.WithExecCwd(opts.Workdir))
	}
	if len(opts.Env) > 0 {
		env := envSliceToMap(opts.Env)
		eopts = append(eopts, msb.WithExecEnv(env))
	}
	if len(opts.Command) == 0 {
		return "", 1, errors.New("msb exec: empty command")
	}
	out, err := sb.Exec(ctx, opts.Command[0], opts.Command[1:], eopts...)
	if err != nil {
		return "", 1, err
	}
	return out.Stdout(), out.ExitCode(), nil
}

// State reports the sandbox lifecycle state mapped onto ContainerState.
func (m *MsbBackend) State(ctx context.Context, name string) (ContainerState, error) {
	h, err := msb.GetSandbox(ctx, name)
	if err != nil {
		return ContainerStateMissing, nil // not found == missing (probe contract)
	}
	switch h.Status() {
	case msb.SandboxStatusRunning:
		return ContainerStateRunning, nil
	case msb.SandboxStatusStopped:
		return ContainerStateExited, nil
	default:
		return ContainerStateExited, nil
	}
}

// Stop requests a graceful sandbox stop.
func (m *MsbBackend) Stop(ctx context.Context, name string) error {
	h, err := msb.GetSandbox(ctx, name)
	if err != nil {
		return nil // already gone
	}
	return h.RequestStop(ctx)
}

// Cleanup removes the sandbox so it can be recreated: stop first if still
// running, wait for the stop to land, then remove.
func (m *MsbBackend) Cleanup(ctx context.Context, name string) error {
	h, err := msb.GetSandbox(ctx, name)
	if err != nil {
		return nil // already gone
	}
	if h.Status() == msb.SandboxStatusRunning {
		if err := h.Stop(ctx, msb.WithStopTimeout(30*time.Second)); err != nil {
			// Convergent Stop bounds itself, but a wedged guest falls back to
			// Kill so recreate is never blocked indefinitely — the root disk
			// is about to be removed anyway.
			if killErr := h.Kill(ctx, msb.WithKillTimeout(10*time.Second)); killErr != nil {
				return fmt.Errorf("stop sandbox %s: %w (kill fallback: %v)", name, err, killErr)
			}
		}
	}
	// Re-resolve until fully stopped: the handle's status is a snapshot and
	// passes through "draining" before "stopped"; Remove refuses earlier.
	for i := 0; i < 60; i++ {
		fresh, err := h.Refresh(ctx)
		if err != nil {
			return nil // gone
		}
		h = fresh
		if h.Status() == msb.SandboxStatusStopped {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	if err := h.Remove(ctx); err != nil {
		return fmt.Errorf("remove sandbox %s: %w", name, err)
	}
	return nil
}

// WorkingDir is unsupported until sandbox inspect parity lands (Step 7).
func (m *MsbBackend) WorkingDir(_ context.Context, _ string) (string, error) {
	return "", ErrMsbUnsupported
}

// MountSource is unsupported until PathMap translation lands (Step 7).
func (m *MsbBackend) MountSource(_ context.Context, _, _ string) (string, error) {
	return "", ErrMsbUnsupported
}

// Label is unsupported; msb volumes carry labels, sandboxes do not yet.
func (m *MsbBackend) Label(_ context.Context, _, _ string) (string, error) {
	return "", ErrMsbUnsupported
}

// ListByPrefix lists sandbox names matching a prefix.
func (m *MsbBackend) ListByPrefix(ctx context.Context, prefix string) []string {
	page, err := msb.ListSandboxes(ctx)
	if err != nil || page == nil {
		return nil
	}
	var names []string
	for _, h := range page.Sandboxes {
		n := h.Name()
		if len(n) >= len(prefix) && n[:len(prefix)] == prefix {
			names = append(names, n)
		}
	}
	return names
}

// IsStale is unsupported until image-ID comparison lands (Step 6 image path).
func (m *MsbBackend) IsStale(_ context.Context, _, _ string) bool {
	return false
}

// CheckImageCommand returns the msb command verifying the local image.
func (m *MsbBackend) CheckImageCommand() []string {
	return []string{"msb", "image", "inspect", "construct-box:latest"}
}

// connect attaches to a running sandbox by name.
func (m *MsbBackend) connect(ctx context.Context, name string) (*msb.Sandbox, error) {
	h, err := msb.GetSandbox(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("sandbox %s not found: %w", name, err)
	}
	return h.Connect(ctx)
}

// envSliceToMap converts ordered KEY=VALUE env slices into the map the SDK
// takes. Later assignments win, matching docker's repeated -e semantics
// that engine.go env masking relies on.
func envSliceToMap(env []string) map[string]string {
	out := make(map[string]string, len(env))
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok {
			out[k] = v
		}
	}
	return out
}

// Interface conformance.
var _ Backend = (*MsbBackend)(nil)

// DetectBackend resolves the configured isolation backend, fail closed:
// backend = "microvm" with msb missing is a hard error with install instructions,
// never a silent Docker fallback.
func DetectBackend(cfg *config.Config) (Backend, error) {
	backend := cfg.Runtime.Backend
	if backend == "" {
		backend = "auto"
	}
	switch backend {
	case "auto", "container", "podman", "docker":
		rt := DetectRuntime(backend)
		if rt == "" {
			return nil, errors.New("no container runtime found (container/podman/docker). Install Docker Desktop or Podman")
		}
		return NewDockerBackend(rt)
	case "microvm":
		if cfg != nil && strings.EqualFold(cfg.Network.Mode, "strict") {
			return nil, errors.New("network mode 'strict' is not yet supported under the microvm backend (use backend = \"docker\" or network mode = \"permissive\")")
		}
		m := NewMsbBackend()
		if ok, availErr := m.Available(context.Background()); !ok || availErr != nil {
			return nil, errors.New("runtime backend = \"microvm\" but microsandbox is not installed. Install it: curl -fsSL https://install.microsandbox.dev | sh (Apple Silicon macOS or Linux with KVM)")
		}
		return m, nil
	default:
		return nil, fmt.Errorf("unknown runtime backend %q (want \"auto\", \"container\", \"podman\", \"docker\", or \"microvm\")", backend)
	}
}

// ValidateBackendSelected reports whether the compose-based run path can
// serve the configured backend. Entry points call this before any container
// operation: backend = "microvm" must fail closed with a clear message on compose-only
// commands, never silently fall through to Docker.
func ValidateBackendSelected(cfg *config.Config) error {
	switch cfg.Runtime.Backend {
	case "", "auto", "container", "podman", "docker":
		return nil
	case "microvm":
		return errors.New("the microvm backend does not support this operation. Set `backend` to \"auto\", \"container\", \"podman\", or \"docker\" in [runtime] to use Docker")
	}
	return fmt.Errorf("unknown runtime backend %q (want \"auto\", \"container\", \"podman\", \"docker\", or \"microvm\")", cfg.Runtime.Backend)
}

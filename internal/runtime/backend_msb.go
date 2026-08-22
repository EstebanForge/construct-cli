package runtime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	msb "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/EstebanForge/construct-cli/internal/config"
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
	return msb.IsInstalled(), nil
}

// EnsureImage transitions the construct image into msb: probes local msb
// image first, attempts pulling from ghcr.io/estebanforge/construct-box:latest,
// and falls back to docker save to a temp archive + msb load.
func (m *MsbBackend) EnsureImage(_ *config.Config) error {
	if m.imageLoaded() {
		return nil
	}

	// Try msb pull from GHCR first if network available
	pull := exec.Command("msb", "pull", "ghcr.io/estebanforge/construct-box:latest")
	pull.Stdin = nil
	if _, err := pull.CombinedOutput(); err == nil {
		tag := exec.Command("msb", "image", "tag", "ghcr.io/estebanforge/construct-box:latest", "construct-box:latest")
		tag.Stdin = nil
		if terr := tag.Run(); terr == nil {
			return nil
		}
	}

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

	save := exec.Command("docker", "save", "-o", tmp.Name(), "construct-box:latest")
	if out, err := save.CombinedOutput(); err != nil {
		return fmt.Errorf("docker save construct-box: %w: %s", err, out)
	}
	load := exec.Command("msb", "load", "-i", tmp.Name())
	load.Stdin = nil // msb stdin trap: caller stdin must not stay open (§7.1)
	if out, err := load.CombinedOutput(); err != nil {
		return fmt.Errorf("msb load: %w: %s", err, out)
	}
	return nil
}

// imageLoaded probes msb for the construct image.
func (m *MsbBackend) imageLoaded() bool {
	cmd := exec.Command("msb", "image", "inspect", "construct-box:latest")
	cmd.Stdin = nil
	return cmd.Run() == nil
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
		if err := h.Stop(ctx); err != nil {
			return fmt.Errorf("stop sandbox %s: %w", name, err)
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
		backend = "docker"
	}
	switch backend {
	case "docker":
		rt := DetectRuntime(cfg.Runtime.Engine)
		if rt == "" {
			return nil, errors.New("no container runtime found (docker/podman). Install Docker Desktop or Podman")
		}
		return NewDockerBackend(rt)
	case "microvm":
		m := NewMsbBackend()
		if ok, availErr := m.Available(context.Background()); !ok || availErr != nil {
			return nil, errors.New("runtime backend = \"microvm\" but microsandbox is not installed. Install it: curl -fsSL https://msb.sh | sh (Apple Silicon macOS or Linux with KVM)")
		}
		return m, nil
	default:
		return nil, fmt.Errorf("unknown runtime backend %q (want \"docker\" or \"microvm\")", backend)
	}
}

// ValidateBackendSelected reports whether the compose-based run path can
// serve the configured backend. Entry points call this before any container
// operation: backend = "microvm" must fail closed with a clear message on compose-only
// commands, never silently fall through to Docker.
func ValidateBackendSelected(cfg *config.Config) error {
	if cfg.Runtime.Backend == "" || cfg.Runtime.Backend == "docker" {
		return nil
	}
	if cfg.Runtime.Backend == "microvm" {
		return errors.New("the microvm backend does not support this operation. Remove `backend = \"microvm\"` from [runtime] to use Docker")
	}
	return fmt.Errorf("unknown runtime backend %q (want \"docker\" or \"microvm\")", cfg.Runtime.Backend)
}

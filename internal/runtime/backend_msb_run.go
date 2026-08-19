package runtime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	msb "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/EstebanForge/construct-cli/internal/config"
)

// msbHomeMountDest is the guest path of the host construct home bind.
// /home/construct binds the host ~/.config/construct-cli/home directly,
// matching Docker's compose, so installed agent binaries persist on the
// host and AreAgentsInstalled() keeps working unchanged.
//
// /home/linuxbrew is deliberately NOT volume-backed: msb does not copy
// image content into an empty named volume on first mount (Docker does),
// so a fresh volume shadows the image's linuxbrew entirely. Instead, brew
// state persists in the sandbox root disk: sandboxes are named, kept
// across runs (stopped, not removed), and stop/start preserves the root
// disk (verified 2026-08-19).
const msbHomeMountDest = "/home/construct"

// EnsureMsbVolumes is kept for API compatibility; the packages volume was
// removed (it shadowed the image's linuxbrew — msb has no Docker-style
// copy-image-content-into-empty-volume behavior). No named volumes remain.
func EnsureMsbVolumes(_ context.Context) error { return nil }

// msbSandboxMounts maps the construct mount layout onto msb MountConfig:
// host construct home -> /home/construct (direct bind, Docker-parity),
// project dir bind -> /workspace, plus conditional auto-mounts (qmd models)
// when the host path exists. linuxbrew stays on the sandbox root disk.
func msbSandboxMounts(projectDir string) map[string]msb.MountConfig {
	mounts := map[string]msb.MountConfig{}
	if home := msbHostConstructHome(); home != "" {
		mounts[msbHomeMountDest] = msb.Mount.Bind(home, msb.MountOptions{})
	}

	if projectDir != "" {
		// Resolve host symlinks (macOS /var -> /private/var etc.): msb binds
		// the literal path and fails with ENOTDIR on unresolved temp paths.
		if resolved, err := filepath.EvalSymlinks(projectDir); err == nil {
			projectDir = resolved
		}
		mounts["/workspace"] = msb.Mount.Bind(projectDir, msb.MountOptions{})
	}

	for _, m := range conditionalAutoMounts() {
		mounts[m.Dest] = msb.Mount.Bind(m.Src, msb.MountOptions{Readonly: m.Readonly})
	}
	return mounts
}

// msbHostConstructHome resolves the host construct home dir
// (~/.config/construct-cli/home), symlink-resolved for msb's literal-path
// binds. Empty means unavailable; /home/construct then falls back to the
// image's baked-in skeleton.
func msbHostConstructHome() string {
	p := filepath.Join(config.GetConfigDir(), "home")
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		return ""
	}
	return resolved
}

// conditionalAutoMounts mirrors GenerateDockerComposeOverride's host-exists
// auto-mounts (AGENTS.md "Conditional Host Mounts"). Returns destination,
// source, and whether the mount is read-only. The qmd models cache stays RW
// (lazily-fetched models write back to the shared host cache).
//
// NOTE: only directory mounts belong here. msb agentd bind-mounts require the
// target path to pre-exist in the guest image (v0.6.10: a missing target
// aborts sandbox start with ENOENT, unlike Docker which auto-creates it).
// File-sized auto-mounts (global gitignore) are seeded post-boot via
// msbSeedAutoFiles instead.
func conditionalAutoMounts() []msbAutoMount {
	var mounts []msbAutoMount
	if p, ok := getQmdModelsPath(); ok {
		mounts = append(mounts, msbAutoMount{Dest: "/home/construct/.cache/qmd/models", Src: p, Readonly: false})
	}
	return mounts
}

type msbAutoMount struct {
	Dest     string
	Src      string
	Readonly bool
}

// msbNetworkConfig maps construct network modes onto msb network profiles
// (docs/VMs.md §6): permissive = public, strict = public + in-guest filter
// (msb policy layer OFF until stacking verified, §9), offline = no net.
// Every sandbox carries the guest->host transport rules (§3.1).
func msbNetworkConfig(mode string, bridgePorts []int) *msb.NetworkConfig {
	switch mode {
	case "offline":
		return msbNetworkOffline(bridgePorts)
	default: // permissive, strict
		return msbNetworkPublic(bridgePorts)
	}
}

func msbNetworkPublic(bridgePorts []int) *msb.NetworkConfig {
	// Public egress with explicit guest->host transport rules (§3.1):
	// allow@host:tcp:<port> per bridge + DNS resolution.
	net := &msb.NetworkConfig{DefaultEgress: msb.PolicyActionAllow}
	for _, port := range bridgePorts {
		net.Rules = append(net.Rules, msb.PolicyRule{
			Action:      msb.PolicyActionAllow,
			Direction:   msb.PolicyDirectionEgress,
			Destination: "host",
			Protocol:    msb.PolicyProtocolTCP,
			Port:        fmt.Sprintf("%d", port),
		})
	}
	net.Rules = append(net.Rules, dnsRule())
	return net
}

func msbNetworkOffline(bridgePorts []int) *msb.NetworkConfig {
	// No public egress; host transport + DNS only (deny-by-default base).
	net := &msb.NetworkConfig{DefaultEgress: msb.PolicyActionDeny}
	for _, port := range bridgePorts {
		net.Rules = append(net.Rules, msb.PolicyRule{
			Action:      msb.PolicyActionAllow,
			Direction:   msb.PolicyDirectionEgress,
			Destination: "host",
			Protocol:    msb.PolicyProtocolTCP,
			Port:        fmt.Sprintf("%d", port),
		})
	}
	net.Rules = append(net.Rules, dnsRule())
	return net
}

func dnsRule() msb.PolicyRule {
	return msb.PolicyRule{
		Action:    msb.PolicyActionAllow,
		Direction: msb.PolicyDirectionEgress,
		Protocol:  msb.PolicyProtocolUDP,
		Port:      "53",
	}
}

// msbHostAlias is the per-backend host alias (Spike B winner, §3.1).
const msbHostAlias = "host.microsandbox.internal"

// MsbRunSpec is the sandbox-creation input assembled from config; the
// engine-side port (Step 6 wiring) consumes it.
type MsbRunSpec struct {
	Name         string
	Image        string
	Mounts       map[string]msb.MountConfig
	Network      *msb.NetworkConfig
	Env          map[string]string
	HostAliasEnv string // CONSTRUCT_HOST_ALIAS value for the entrypoint
	Cmd          []string // workload; empty = sleep infinity (persistent sandbox)
	CPUs         uint8    // 0 = msb default (1)
	MemoryMiB    uint32   // 0 = msb default (512)
}

// BuildMsbRunSpec assembles the sandbox spec from config and project dir.
// Pure function: no side effects, unit-testable without msb installed.
func BuildMsbRunSpec(cfg *config.Config, name, projectDir string, bridgePorts []int) *MsbRunSpec {
	env := map[string]string{
		"CONSTRUCT_HOST_ALIAS": msbHostAlias,
	}
	for k, v := range envSliceToMap(msbBaseEnv(cfg)) {
		env[k] = v
	}
	return &MsbRunSpec{
		Name:         name,
		Image:        "construct-box:latest",
		Mounts:       msbSandboxMounts(projectDir),
		Network:      msbNetworkConfig(cfg.Network.Mode, bridgePorts),
		Env:          env,
		HostAliasEnv: msbHostAlias,
	}
}

// msbBaseEnv holds the backend-agnostic env every msb sandbox carries.
func msbBaseEnv(cfg *config.Config) []string {
	_ = cfg
	return []string{}
}

// CreateMsbSandbox boots a sandbox from a run spec (image must already be
// loaded via EnsureImage).
func CreateMsbSandbox(ctx context.Context, spec *MsbRunSpec) (*msb.Sandbox, error) {
	if spec == nil || strings.TrimSpace(spec.Name) == "" {
		return nil, errors.New("msb sandbox spec requires a name")
	}
	opts := []msb.SandboxOption{
		msb.WithImage(spec.Image),
		msb.WithMounts(spec.Mounts),
		msb.WithNetwork(spec.Network),
		msb.WithEnv(spec.Env),
	}
	// The image CMD (/bin/bash) exits immediately without a TTY; Docker's
	// compose keeps it alive via stdin_open+tty, which msb has no equivalent
	// for. Persistent sandboxes default to a no-op workload; one-shot specs
	// (agent install) pass their own Cmd and stop when it exits.
	cmd := spec.Cmd
	oneShot := len(cmd) > 0 // explicit workload: run it to completion
	if len(cmd) == 0 {
		cmd = []string{"sleep", "infinity"}
	}
	opts = append(opts, msb.WithCmd(cmd...))
	if spec.CPUs > 0 {
		opts = append(opts, msb.WithCPUs(spec.CPUs))
	}
	if spec.MemoryMiB > 0 {
		opts = append(opts, msb.WithMemory(spec.MemoryMiB))
	}
	sb, err := msb.CreateSandbox(ctx, spec.Name, opts...)
	if err != nil {
		return nil, fmt.Errorf("create sandbox: %w", err)
	}
	if err := msbSeedAutoFiles(ctx, sb); err != nil {
		return nil, err
	}
	if oneShot {
		// SDK contract: WithCmd/WithEntrypoint only configure the default
		// workload — the SDK never auto-runs it at create (the msb CLI does
		// that wiring for `msb run`). One-shot specs block on it here and
		// surface the entrypoint's exit code.
		out, err := sb.ExecDefault(ctx)
		if err != nil {
			return nil, fmt.Errorf("default workload: %w", err)
		}
		if code := out.ExitCode(); code != 0 {
			return nil, fmt.Errorf("default workload exited with code %d — run `msb logs %s` for details", code, spec.Name)
		}
		return sb, nil
	}
	// Persistent sandbox: run the default workload (entrypoint + sleep
	// infinity) in the background; it dies with the sandbox on stop.
	go func() {
		_, _ = sb.ExecDefault(context.Background()) //nolint:errcheck // workload result surfaced via sandbox logs
	}()
	return sb, nil
}

// MsbInstallAgents runs the one-shot first-run agent install inside a
// sandbox (msb analog of InstallAgentsAfterBuild): boot with the default
// entrypoint and a trivial command, wait for it to exit, verify exit code.
// The entrypoint performs the actual install (marker-gated, entrypoint.sh).
// The generated install script is (re)written into the mounted home first —
// PrepareBackendAgnostic owns this on the normal path; MsbInstallAgents
// regenerates it so a stale/empty script cannot silently skip the install.
func MsbInstallAgents(ctx context.Context, cfg *config.Config) error {
	if err := EnsureMsbVolumes(ctx); err != nil {
		return fmt.Errorf("msb agent install: %w", err)
	}
	if err := NewMsbBackend().EnsureImage(cfg); err != nil {
		return fmt.Errorf("msb agent install: %w", err)
	}
	if err := writeMsbInstallScript(); err != nil {
		return fmt.Errorf("msb agent install: %w", err)
	}

	name := "construct-msb-install"
	m := NewMsbBackend()
	if err := m.Cleanup(ctx, name); err != nil {
		return fmt.Errorf("msb agent install (stale sandbox): %w", err)
	}

	spec := BuildMsbRunSpec(cfg, name, "", nil)
	spec.Name = name
	spec.Cmd = []string{"echo", "Installation complete"}
	// CreateMsbSandbox blocks on the one-shot default workload (the
	// entrypoint's install) and fails on a non-zero exit code.
	if _, err := CreateMsbSandbox(ctx, spec); err != nil {
		return err
	}
	return nil
}

// writeMsbInstallScript regenerates install_user_packages.sh inside the
// mounted construct home (same content PrepareBackendAgnostic writes).
func writeMsbInstallScript() error {
	pkgs, err := config.LoadPackages()
	if err != nil {
		return fmt.Errorf("load packages config: %w", err)
	}
	containerDir := filepath.Join(config.GetConfigDir(), "home", ".config", "construct-cli", "container")
	if err := os.MkdirAll(containerDir, 0o755); err != nil {
		return err
	}
	scriptPath := filepath.Join(containerDir, "install_user_packages.sh")
	return os.WriteFile(scriptPath, []byte(pkgs.GenerateInstallScript()), 0o755)
}

// msbSeedAutoFiles copies file-sized conditional auto-mounts (global
// gitignore) into the guest after boot. msb cannot bind-mount single files
// unless the target already exists in the image (ENOENT kills the sandbox,
// v0.6.10), so the file is copied instead. The copy is a seed, not a live
// bind: host edits need a sandbox restart to propagate.
func msbSeedAutoFiles(ctx context.Context, sb *msb.Sandbox) error {
	p, ok := getGlobalGitIgnorePath()
	if !ok {
		return nil
	}
	fs := sb.FS()
	for _, dir := range []string{"/home/construct/.config", "/home/construct/.config/git"} {
		if err := fs.Mkdir(ctx, dir); err != nil {
			// Already-existing dirs are fine; anything else is fatal.
			if _, statErr := fs.Stat(ctx, dir); statErr != nil {
				return fmt.Errorf("msb seed %s: %w", dir, err)
			}
		}
	}
	if err := fs.CopyFromHost(ctx, p, "/home/construct/.config/git/ignore"); err != nil {
		return fmt.Errorf("msb seed gitignore: %w", err)
	}
	return nil
}

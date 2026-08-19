package runtime

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	msb "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/EstebanForge/construct-cli/internal/config"
)

// Msb volume names (docs/VMs.md §5-6): packages carries /home/linuxbrew
// (brew/npm state, chown cost — must be disk-backed so the fresh-root-disk
// chown never repeats), home carries ~/.config/construct-cli/home.
const (
	msbVolumePackages = "construct-packages"
	msbVolumeHome     = "construct-home"

	// msbPackagesVolumeSizeMiB sizes the disk-backed packages volume. msb
	// requires an explicit size for disk volumes; 20 GiB covers linuxbrew +
	// npm/bun/cargo agent state with headroom (the container image alone is
	// 3.4 GiB before any agent install).
	msbPackagesVolumeSizeMiB = 20 * 1024
)

// EnsureMsbVolumes creates the named volumes the msb backend needs,
// idempotently. Packages is disk-backed (ext4) per §7.1; home is
// dir-backed (small files, host-accessible).
func EnsureMsbVolumes(ctx context.Context) error {
	if _, err := msb.GetVolume(ctx, msbVolumePackages); err != nil {
		// missing -> create (disk-backed: chown cost, §7.1)
		if _, err := msb.CreateVolume(ctx, msbVolumePackages,
			msb.WithVolumeKind(msb.VolumeKindDisk),
			msb.WithVolumeSize(msbPackagesVolumeSizeMiB)); err != nil {
			return fmt.Errorf("create msb volume %s: %w", msbVolumePackages, err)
		}
	}

	if _, err := msb.GetVolume(ctx, msbVolumeHome); err == nil {
		return nil
	}
	if _, err := msb.CreateVolume(ctx, msbVolumeHome, msb.WithVolumeKind(msb.VolumeKindDir)); err != nil {
		return fmt.Errorf("create msb volume %s: %w", msbVolumeHome, err)
	}
	return nil
}

// msbSandboxMounts maps the construct mount layout onto msb MountConfig:
// packages volume -> /home/linuxbrew, home volume -> /home/construct,
// project dir bind -> /workspace, plus conditional auto-mounts
// (global gitignore, qmd models) when their host paths exist.
func msbSandboxMounts(projectDir string) map[string]msb.MountConfig {
	mounts := map[string]msb.MountConfig{
		"/home/linuxbrew/.linuxbrew": msb.Mount.Named(msbVolumePackages, msb.MountOptions{}),
		"/home/construct":            msb.Mount.Named(msbVolumeHome, msb.MountOptions{}),
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
		// The image CMD (/bin/bash) exits immediately without a TTY; Docker's
		// compose keeps it alive via stdin_open+tty, which msb has no equivalent
		// for. Keep the sandbox (and its entrypoint) alive with a no-op workload.
		msb.WithCmd("sleep", "infinity"),
	}
	sb, err := msb.CreateSandbox(ctx, spec.Name, opts...)
	if err != nil {
		return nil, fmt.Errorf("create sandbox: %w", err)
	}
	if err := msbSeedAutoFiles(ctx, sb); err != nil {
		return nil, err
	}
	return sb, nil
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

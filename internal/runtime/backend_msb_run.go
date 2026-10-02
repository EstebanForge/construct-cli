package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	msb "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/EstebanForge/construct-cli/internal/cerrors"
	"github.com/EstebanForge/construct-cli/internal/config"
	"github.com/EstebanForge/construct-cli/internal/constants"
	"github.com/EstebanForge/construct-cli/internal/msbembed"
	"github.com/EstebanForge/construct-cli/internal/ui"
)

// msbHomeMountDest is the guest path of the host construct home bind.
// /home/construct binds the host ~/.config/construct-cli/home directly,
// matching Docker's compose, so installed agent binaries persist on the
// host and AreAgentsInstalled() keeps working unchanged.
//
// Baked tooling lives on the sandbox root disk (apt, /usr/local) and is
// deliberately NOT volume-backed: msb does not copy image content into an
// empty named volume on first mount (Docker does), so a fresh volume would
// shadow the image's toolchain entirely. Root-fs state persists because
// sandboxes are named, kept across runs (stopped, not removed), and
// stop/start preserves the root disk (verified 2026-08-19).
const msbHomeMountDest = "/home/construct"

// EnsureMsbVolumes is kept for API compatibility; the packages volume was
// removed (it would have shadowed the image's baked toolchain — msb has no
// Docker-style copy-image-content-into-empty-volume behavior). No named
// volumes remain.
func EnsureMsbVolumes(_ context.Context) error { return nil }

func cleanProjectDir(projectDir string) string {
	if projectDir == "" {
		return ""
	}
	// Canonicalize BEFORE symlink evaluation: EvalSymlinks returns relative
	// results for relative inputs ("." stays "."), and store keys must be
	// absolute so decline records and mount hashes are cwd-independent.
	if abs, err := filepath.Abs(projectDir); err == nil {
		projectDir = abs
	}
	if v := EvaluateWorkspace(projectDir, 0); v.Risk == WorkspaceRiskSystem {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(projectDir); err == nil {
		return resolved
	}
	return filepath.Clean(projectDir)
}

// GetMsbWorkspaceMountDest returns the guest mount destination for a project dir (/workspaces/<name>).
func GetMsbWorkspaceMountDest(projectDir string) string {
	cleaned := cleanProjectDir(projectDir)
	if cleaned == "" {
		return "/workspaces"
	}
	projectName := filepath.Base(cleaned)
	if projectName == "." || projectName == "/" || projectName == "" {
		return "/workspaces"
	}
	return "/workspaces/" + projectName
}

// msbSandboxMounts maps the construct mount layout onto msb MountConfig:
// host construct home -> /home/construct (direct bind, Docker-parity),
// then either the configured multi-path daemon mounts (daemon.mount_paths,
// Docker parity: every root mounted under /workspaces/<hash>) or the single
// project dir bind -> /workspaces/<name>, plus conditional auto-mounts
// (qmd models) when the host path exists. Baked tooling (apt, /usr/local)
// stays on the sandbox root disk.
func msbSandboxMounts(cfg *config.Config, projectDir string) map[string]msb.MountConfig {
	mounts := map[string]msb.MountConfig{}
	if home := msbHostConstructHome(); home != "" {
		mounts[msbHomeMountDest] = msb.Mount.Bind(home, msb.MountOptions{
			StatVirtualization: msb.StatVirtualizationOff,
		})
	}

	if dm := ResolveDaemonMounts(cfg); dm.Enabled {
		for _, m := range dm.Mounts {
			mounts[m.ContainerPath] = msb.Mount.Bind(m.HostPath, msb.MountOptions{
				StatVirtualization: msb.StatVirtualizationOff,
			})
		}
	} else {
		// Single-path + learned roots: mount every effective workspace root
		// (boot project + learned set) at its own /workspaces/<name> dest.
		store, err := LoadRootsStore()
		if err != nil {
			store = RootsStore{Version: rootsStoreVersion}
		}
		for _, dir := range effectiveWorkspaceRoots(projectDir, store) {
			dest := GetMsbWorkspaceMountDest(dir)
			mounts[dest] = msb.Mount.Bind(dir, msb.MountOptions{
				StatVirtualization: msb.StatVirtualizationOff,
			})
		}
	}

	for _, m := range conditionalAutoMounts(cfg) {
		mounts[m.Dest] = msb.Mount.Bind(m.Src, msb.MountOptions{
			Readonly:           m.Readonly,
			StatVirtualization: msb.StatVirtualizationOff,
		})
	}
	return mounts
}

// MsbPathMap pairs a guest mount point with its host source (used by the
// host exec bridge to translate a guest cwd to the host working dir).
type MsbPathMap struct {
	Guest string
	Host  string
}

// MsbPathMaps returns the guest→host path translations for every bind in
// msbSandboxMounts (home, workspace mounts, auto-mounts). Derived from the
// same sources so the host exec bridge can never drift from actual mounts.
// Maps are sorted longest guest-path first to resolve nested paths correctly.
func MsbPathMaps(cfg *config.Config, projectDir string) []MsbPathMap {
	maps := []MsbPathMap{}
	if home := msbHostConstructHome(); home != "" {
		maps = append(maps, MsbPathMap{Guest: msbHomeMountDest, Host: home})
	}
	if dm := ResolveDaemonMounts(cfg); dm.Enabled {
		for _, m := range dm.Mounts {
			maps = append(maps, MsbPathMap{Guest: m.ContainerPath, Host: m.HostPath})
		}
	} else {
		// Single-path + learned roots: one map entry per effective root.
		store, err := LoadRootsStore()
		if err != nil {
			store = RootsStore{Version: rootsStoreVersion}
		}
		for _, dir := range effectiveWorkspaceRoots(projectDir, store) {
			maps = append(maps, MsbPathMap{Guest: GetMsbWorkspaceMountDest(dir), Host: dir})
		}
	}
	for _, m := range conditionalAutoMounts(cfg) {
		maps = append(maps, MsbPathMap{Guest: m.Dest, Host: m.Src})
	}
	// Sort longest guest path first so specific nested paths match before parent mounts.
	sort.Slice(maps, func(i, j int) bool {
		return len(maps[i].Guest) > len(maps[j].Guest)
	})
	return maps
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
// (lazily-fetched models write back to the shared host cache). Skills mounts
// default to read-only (cfg.Sandbox.SkillsReadOnly = true); opt-in RW when
// the user wants agents to author or edit skills (see docs/VMsv2.md phase 7).
//
// NOTE: only directory mounts belong here. msb agentd bind-mounts require the
// target path to pre-exist in the guest image (v0.6.10: a missing target
// aborts sandbox start with ENOENT, unlike Docker which auto-creates it).
// File-sized auto-mounts (global gitignore) are seeded post-boot via
// msbSeedAutoFiles instead.
func conditionalAutoMounts(cfg *config.Config) []msbAutoMount {
	var mounts []msbAutoMount
	if p, ok := getQmdModelsPath(); ok {
		mounts = append(mounts, msbAutoMount{Dest: "/home/construct/.cache/qmd/models", Src: p, Readonly: false})
	}
	// Persistent apt download cache (created on demand): recreated daemons
	// re-provision packages.toml on first boot; cached .debs turn that from
	// a network download into a local copy.
	if p, ok := getAptCachePath(); ok {
		mounts = append(mounts, msbAutoMount{Dest: "/var/cache/apt/archives", Src: p, Readonly: false})
	}
	// Host skills source -> per-agent skills directory. Each SupportedAgents
	// entry that hosts skills gets one bind. Mount mode matches cfg.Sandbox
	// .SkillsReadOnly (default true; opt-in RW). See docs/VMsv2.md phase 7.
	if p, ok := GetSkillsSourcePath(cfg); ok {
		readonly := cfg == nil || cfg.Sandbox.SkillsReadOnly
		for _, target := range SkillsMountTargets() {
			mounts = append(mounts, msbAutoMount{Dest: target, Src: p, Readonly: readonly})
		}
	}
	return mounts
}

type msbAutoMount struct {
	Dest     string
	Src      string
	Readonly bool
}

// msbNetworkConfig maps construct network modes onto msb network profiles
// (docs/VMsv2.md): permissive = public, strict = public + in-guest filter
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

// msbHostTransportRules emits the guest->host transport rules (§3.1).
// bridgePorts are the known bridge listener ports; when empty (the engine
// binds random ports per run and the sandbox outlives them) a single
// any-port host-TCP rule is emitted instead. Safe: destination is the host
// only, and every bridge enforces token auth.
func msbHostTransportRules(bridgePorts []int) []msb.PolicyRule {
	if len(bridgePorts) == 0 {
		return []msb.PolicyRule{{
			Action:      msb.PolicyActionAllow,
			Direction:   msb.PolicyDirectionEgress,
			Destination: "host",
			Protocol:    msb.PolicyProtocolTCP,
		}}
	}
	rules := make([]msb.PolicyRule, 0, len(bridgePorts))
	for _, port := range bridgePorts {
		rules = append(rules, msb.PolicyRule{
			Action:      msb.PolicyActionAllow,
			Direction:   msb.PolicyDirectionEgress,
			Destination: "host",
			Protocol:    msb.PolicyProtocolTCP,
			Port:        fmt.Sprintf("%d", port),
		})
	}
	return rules
}

func msbNetworkPublic(bridgePorts []int) *msb.NetworkConfig {
	// Public egress with explicit guest->host transport rules (§3.1):
	// allow@host:tcp per bridge + DNS resolution.
	net := &msb.NetworkConfig{DefaultEgress: msb.PolicyActionAllow}
	net.Rules = append(msbHostTransportRules(bridgePorts), dnsRule())
	return net
}

func msbNetworkOffline(bridgePorts []int) *msb.NetworkConfig {
	// No public egress; host transport + DNS only (deny-by-default base).
	net := &msb.NetworkConfig{DefaultEgress: msb.PolicyActionDeny}
	net.Rules = append(msbHostTransportRules(bridgePorts), dnsRule())
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
	HostAliasEnv string   // CONSTRUCT_HOST_ALIAS value for the entrypoint
	Entrypoint   []string // empty = image entrypoint; override for one-shot flows (update-all, install, sha256 verify)
	Cmd          []string // workload; empty = sleep infinity (persistent sandbox)
	PortBindings []msb.PortBinding
	Labels       map[string]string
	Detached     bool   // VM outlives the creating process (daemon sandboxes)
	CPUs         uint8  // 0 = msb default (1)
	MemoryMiB    uint32 // 0 = msb default (512)
}

// BuildMsbRunSpec assembles the sandbox spec from config and project dir.
// Pure function: no side effects, unit-testable without msb installed.
func BuildMsbRunSpec(cfg *config.Config, name, projectDir string, bridgePorts []int, imageRef string) *MsbRunSpec {
	env := map[string]string{
		"CONSTRUCT_HOST_ALIAS": msbHostAlias,
	}
	for k, v := range envSliceToMap(msbBaseEnv(cfg)) {
		env[k] = v
	}
	labels := map[string]string{
		"construct.project_dir": cleanProjectDir(projectDir),
	}
	// Mount-set hash, stamped in BOTH layouts so the daemon reuse check
	// detects changes without re-deriving mounts: multi-path uses the
	// configured daemon.mount_paths set; single-path uses the effective
	// workspace set (boot project + learned roots).
	if dm := ResolveDaemonMounts(cfg); dm.Enabled {
		labels[DaemonMountsLabelKey] = dm.Hash
	} else {
		store, err := LoadRootsStore()
		if err != nil {
			store = RootsStore{Version: rootsStoreVersion}
		}
		if roots := effectiveWorkspaceRoots(projectDir, store); len(roots) > 0 {
			labels[DaemonMountsLabelKey] = hashDaemonMountPaths(roots)
		}
	}
	// Skills hash (separate label so a skills-only toggle does not require a
	// multi-path daemon). Stamped whenever skills mounts are enabled, even
	// if the auto-detect found no source; the recreate check distinguishes
	// "enabled + source present" from "enabled + source missing" via the
	// hash contents.
	if skillsHash := SkillsDaemonHash(cfg); skillsHash != "" {
		labels[DaemonSkillsLabelKey] = skillsHash
	}
	// Sudo policy label: toggling sandbox.passwordless_sudo must recreate
	// the daemon because the entrypoint applies the sudoers drop-in at
	// sandbox creation only.
	labels[DaemonSudoLabelKey] = sudoPolicy(cfg)
	// SDK version label: recreate across construct upgrades that bump the
	// embedded msb runtime, before the ffi contract can refuse a connect.
	labels[DaemonSDKVersionLabelKey] = msb.SDKVersion()
	// Image digest label: EnsureImage has already run by the time a spec
	// is built, so the local cache holds the freshly-pulled digest; a
	// later decision run that sees a different digest recreates onto it.
	labels[DaemonImageDigestLabelKey] = msbCachedImageDigest()
	// Boot on the ref the caller's EnsureImage verified: create and image
	// acquisition must never resolve independently. A listed-but-unusable
	// entry (e.g. a legacy bare ref the acquisition sweep missed) winning
	// the candidate race while EnsureImage verified GHCR sends msb create
	// to resolve the bare name at docker.io, where the repo does not
	// exist. Empty ref (install path probes independently / unit tests)
	// falls back to candidate resolution.
	if imageRef == "" {
		imageRef = MsbConstructImageRef()
	}
	return &MsbRunSpec{
		Name: name,
		// The daemon resolves image refs exactly as stored, and
		// `msb load -i` imports as localhost/construct-box:latest.
		Image:        imageRef,
		Mounts:       msbSandboxMounts(cfg, projectDir),
		Network:      msbNetworkConfig(cfg.Network.Mode, bridgePorts),
		Env:          env,
		Labels:       labels,
		HostAliasEnv: msbHostAlias,
		CPUs:         4,
		MemoryMiB:    4096,
	}
}

// sudoPolicy names the guest sudo regime for the daemon label: "free"
// (NOPASSWD:ALL, default) or "scoped" (apt/ufw/chown allowlist only).
func sudoPolicy(cfg *config.Config) string {
	if cfg != nil && !cfg.Sandbox.PasswordlessSudo {
		return "scoped"
	}
	return "free"
}

// msbBaseEnv holds the backend-agnostic env every msb sandbox carries.
func msbBaseEnv(cfg *config.Config) []string {
	var envVars []string
	if lp := loopbackPortsString(cfg); lp != "" {
		envVars = append(envVars, "CONSTRUCT_LOOPBACK_PORTS="+lp)
	}
	// Emit only the opt-out: the entrypoint and the image both default to
	// free sudo, so an unset var keeps today's behavior.
	if cfg != nil && !cfg.Sandbox.PasswordlessSudo {
		envVars = append(envVars, "CONSTRUCT_PASSWORDLESS_SUDO=0")
	}
	if cfg != nil && cfg.Sandbox.ExecAsHostUser {
		if uid := os.Getuid(); uid > 0 {
			envVars = append(envVars, fmt.Sprintf("CONSTRUCT_HOST_UID=%d", uid))
			envVars = append(envVars, fmt.Sprintf("CONSTRUCT_HOST_GID=%d", os.Getgid()))
		}
	}
	return envVars
}

// ResolveExecUserMsb resolves the exec user inside the msb guest sandbox.
// The entrypoint aligns the guest construct user with CONSTRUCT_HOST_UID:GID,
// so commands always execute as "construct" with matching numeric host ownership.
func ResolveExecUserMsb(_ *config.Config) string {
	return "construct"
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
		// Explicit workdir: construct-box images declare WORKDIR /projects
		// (Dockerfile) and msb >= 0.6.15 validates the image workdir exists
		// in the guest at create time — the transitioned archive fails that
		// check. /home/construct exists in every construct-box image; exec
		// paths set their own WithExecCwd, so this is only the default.
		msb.WithWorkdir("/home/construct"),
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
	// Daemon semantics: run until explicitly stopped (Docker parity). The
	// msb default idle timeout reboots the sandbox after inactivity and the
	// default workload does not re-run on reboot — both kill the daemon model.
	opts = append(opts, msb.WithMaxDuration(0), msb.WithIdleTimeout(0))
	if spec.Detached {
		// Attached sandboxes power down when the creating process exits
		// ("creator process exited; stopping attached sandbox"); the daemon
		// must survive the construct invocation that booted it. Callers
		// release with Detach, never Close (Close stops a detached VM).
		opts = append(opts, msb.WithDetached())
	}
	if len(spec.Entrypoint) > 0 {
		// Entrypoint override replaces the image entrypoint entirely (msb
		// rejects an empty override); construct's one-shot flows (update-all,
		// install, sha256 verify) map here, mirroring compose --entrypoint.
		opts = append(opts, msb.WithEntrypoint(spec.Entrypoint...))
	}
	if spec.CPUs > 0 {
		opts = append(opts, msb.WithCPUs(spec.CPUs))
	}
	if spec.MemoryMiB > 0 {
		opts = append(opts, msb.WithMemory(spec.MemoryMiB))
	}
	if len(spec.PortBindings) > 0 {
		opts = append(opts, msb.WithPortBindings(spec.PortBindings...))
	}
	if len(spec.Labels) > 0 {
		opts = append(opts, msb.WithLabels(spec.Labels))
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

type msbConfigRaw struct {
	Mounts []struct {
		Type  string `json:"type"`
		Host  string `json:"host"`
		Guest string `json:"guest"`
	} `json:"mounts"`
}

// parseMsbConfigMounts parses guest->host bind mounts from sandbox ConfigJSON.
// The SDK's SandboxConfig.Volumes is unpopulated because the JSON schema uses "mounts" (array).
func parseMsbConfigMounts(configJSON string) map[string]string {
	var raw msbConfigRaw
	if err := json.Unmarshal([]byte(configJSON), &raw); err != nil {
		return nil
	}
	mounts := make(map[string]string, len(raw.Mounts))
	for _, m := range raw.Mounts {
		if m.Type == "Bind" && m.Guest != "" {
			mounts[m.Guest] = m.Host
		}
	}
	return mounts
}

// msbDaemonName is the persistent sandbox backing the daemon mode under
// the msb backend (Docker analog: construct-cli-daemon container). Named
// sandboxes persist across stop/start, so agent installs and root-disk
// toolchain state survive daemon restarts (docs/VMsv2.md).
const msbDaemonName = "construct-cli-daemon"

// ErrMsbDaemonWorkdirUnmapped reports that the requested project dir falls
// outside every configured daemon.mount_paths root. Callers must not
// recreate the daemon sandbox in this case: the mount set is static config
// (Docker parity), and recreation would needlessly destroy the guest root
// disk (installs, toolchain state).
var ErrMsbDaemonWorkdirUnmapped = errors.New("msb daemon: current directory is outside the configured daemon mount paths")

// ErrMsbDaemonWorkdirDeclined reports that the user previously answered NO
// to the learn prompt for this folder and the decline is persisted: ct
// never re-prompts and the folder stays unmounted until
// `construct sys daemon roots add <path>` reverses the decision. No
// subsystem prefix: callers (agent engine) already wrap with "msb daemon:".
var ErrMsbDaemonWorkdirDeclined = errors.New("workdir mounting was declined for this folder")

// msbDaemonNeedsRecreate decides whether an existing daemon sandbox can
// serve projectDir. Multi-path mode (daemon.mount_paths): recreate only
// when the mounted hash drifted from config (mounts are create-time only;
// the SDK has no hot-add). Single-path mode: recreate only when the current
// mount cannot map projectDir (label/mount drift, a disallowed workspace,
// or a cwd outside the mounted root); subdirectories of the mounted root
// reuse the daemon. The returned reason explains any recreate to the user.
//
// The skills hash (DaemonSkillsLabelKey) is checked in BOTH modes; a skills
// toggle, RO/RW flip, source appearance, or supported-agent-list growth
// must recreate the running daemon so the new mounts take effect.
func msbDaemonNeedsRecreate(dm DaemonMounts, sandboxLabels map[string]string, configJSON, projectDir string, allowHome bool, cfg *config.Config, imageDigest string) (bool, string) {
	// Skills hash parity. Checked FIRST because it is the cheapest decision
	// (no mount parsing) and the most likely drift source in routine use.
	if currentSkills := SkillsDaemonHash(cfg); currentSkills != "" || sandboxLabels[DaemonSkillsLabelKey] != "" {
		if sandboxLabels[DaemonSkillsLabelKey] != currentSkills {
			return true, "host skills mounts changed (source, mode, or targets)"
		}
	}
	// Sudo policy parity: the entrypoint applies the sudoers drop-in at
	// sandbox creation, so a policy toggle needs a recreate. Old daemons
	// predate the label; their empty label compares unequal to the current
	// policy only when the user actually opted out, which recreates once.
	if sandboxLabels[DaemonSudoLabelKey] != sudoPolicy(cfg) {
		return true, "sudo policy changed (sandbox.passwordless_sudo)"
	}
	// Embedded-runtime parity: construct upgrades that bump the SDK
	// recreate once, like the sudo and image labels. Daemons predating the
	// label carry an empty value and recreate exactly once.
	if sandboxLabels[DaemonSDKVersionLabelKey] != msb.SDKVersion() {
		return true, "embedded msb runtime changed (construct upgrade)"
	}
	// Image drift: the daemon may outlive image republishes. The label
	// records the digest the sandbox was created from; a mismatch with the
	// local cache means the sandbox predates the current image and would
	// boot stale baked content (entrypoint, sudoers) no matter how well
	// its config labels match. An unknown local digest (image inspect
	// failed) never forces a recreate, and daemons predating the label
	// recreate exactly once — the same upgrade vehicle as the sudo label.
	if imageDigest != "" && sandboxLabels[DaemonImageDigestLabelKey] != imageDigest {
		return true, "construct-box image changed"
	}
	if dm.Enabled {
		if sandboxLabels[DaemonMountsLabelKey] != dm.Hash {
			return true, "daemon.mount_paths changed"
		}
		return false, ""
	}
	// Single-path: the mount set is the effective workspace roots (boot
	// project + learned roots). The stamped hash decides — a learned root
	// added or evicted changes the hash and recreates exactly once.
	store, lerr := LoadRootsStore()
	if lerr != nil {
		store = RootsStore{Version: rootsStoreVersion}
	}
	currentProjectDir, hasProjectLabel := sandboxLabels["construct.project_dir"]
	mounts := parseMsbConfigMounts(configJSON)

	// Rebuild the effective root set as the RUNNING daemon sees it: the
	// boot project stays mounted, learned roots join it, and the current
	// cwd joins only when no existing root already covers it (subdirs ride
	// their parent mount) and the user has not declined it (declined
	// folders never enter the mount set; EnsureMsbDaemon errors on them
	// before this decision, so this guard is hash-consistency defense).
	roots := store.Paths()
	if cleaned := cleanProjectDir(projectDir); cleaned != "" {
		covered := currentProjectDir != "" && containsPath(currentProjectDir, cleaned)
		if !covered {
			for _, r := range roots {
				if containsPath(r, cleaned) {
					covered = true
					break
				}
			}
		}
		if !covered && !store.IsDeclined(cleaned) {
			roots = append(roots, cleaned)
		}
	}
	if currentProjectDir != "" && !store.IsDeclined(currentProjectDir) && !slices.Contains(roots, currentProjectDir) {
		roots = append(roots, currentProjectDir)
	}
	sort.Strings(roots)

	switch {
	case !hasProjectLabel && len(roots) == 0:
		return true, "daemon has no workspace label"
	case sandboxLabels[DaemonMountsLabelKey] != hashDaemonMountPaths(roots):
		return true, "workspace roots changed (learned root added, removed, or evicted)"
	case currentProjectDir != "" && mounts[GetMsbWorkspaceMountDest(currentProjectDir)] != currentProjectDir:
		return true, "workspace label does not match mounted state"
	case !allowHome && currentProjectDir != "" && EvaluateWorkspace(currentProjectDir, 0).Risk == WorkspaceRiskHome:
		return true, "mounted workspace is no longer allowed"
	}
	return false, ""
}

// EnsureMsbDaemon guarantees the persistent daemon sandbox exists and is
// running: create when missing, boot when stopped (default workload — the
// entrypoint + sleep infinity — is re-invoked explicitly; the SDK never
// auto-runs it, not at create and not at start). Returns the live sandbox.
//
// Boot telemetry: each return path emits one msb-boot: line with the
// outcome (cold | recreate | warm | reconnect), the elapsed seconds, the
// mount count, and (for recreate) the reason. The start clock is captured
// at the top; the log is fire-and-forget and never affects the returned
// sandbox. See msbLogBoot + docs/VMsv2.md phase 0.
func EnsureMsbDaemon(ctx context.Context, cfg *config.Config, projectDir string) (*msb.Sandbox, error) {
	bootStart := msbBootClock()
	bootOutcome := msbBootCold
	bootReason := ""
	bootCounts := msbBootCountsFor(cfg, projectDir)

	// Fail fast on launch-contract mismatch BEFORE image acquisition: the
	// embedded SDK refuses non-matching host runtimes at create time with
	// a cryptic error, and without this gate construct would first pull
	// gigabytes into a store it can never boot.
	if err := ensureMsbLaunchCompat(); err != nil {
		return nil, err
	}

	// Provision the image BEFORE the daemon lock: acquisition may now
	// block on an interactive build-confirmation prompt, and holding the
	// lock through it would stall every concurrent construct invocation.
	// Image provisioning never reads or mutates daemon state, so it needs
	// no serialization.
	m := NewMsbBackend()
	bootRef, err := m.EnsureImage(cfg)
	if err != nil {
		return nil, err
	}

	// Serialize all daemon state mutations across concurrent ct
	// invocations. The critical section wraps read-state, decide, write-
	// state, and the recreate/boot itself so two invocations learning
	// different roots cannot produce last-write-wins root loss or a double
	// recreate. Blocking is correct: a 10-minute first boot behind the
	// lock is expected (the prior install path runs inside it). The lock
	// is host-local; one per construct config dir.
	releaseLock, err := acquireDaemonLock()
	if err != nil {
		return nil, fmt.Errorf("acquire daemon lock: %w", err)
	}
	defer releaseLock()

	// Regenerate the guest installer from the current packages.toml on
	// every daemon start: agent runs and first-run do this elsewhere
	// (PrepareBackendAgnostic / MsbInstallAgents), but a plain `daemon
	// start` (or sys exec) never did, so packages.toml edits would not
	// reach the guest until an agent run. Best-effort: a write failure
	// must not block the daemon (the previous script stays in place).
	if err := writeMsbInstallScript(); err != nil {
		ui.InfoF("⚠️  Could not regenerate install script: %v (using the previous one)\n", err)
	}

	// Multi-path mode (Docker parity): the mount set is static config. A cwd
	// outside every configured root is a hard error, never a recreate — the
	// daemon state survives and the user gets an actionable message instead.
	dm := ResolveDaemonMounts(cfg)
	if dm.Enabled && projectDir != "" {
		if _, ok := MapDaemonWorkdirFromMounts(projectDir, dm.Mounts); !ok {
			return nil, fmt.Errorf("%w: %s (add the root to [daemon] mount_paths or disable daemon.multi_paths_enabled)", ErrMsbDaemonWorkdirUnmapped, cleanProjectDir(projectDir))
		}
	}

	// Learned roots (single-path, phase 2.2): learn-or-touch the cwd, then
	// let the mount-set hash decide whether the daemon recreates. All store
	// access is inside the flock critical section.
	learnedRoot := ""
	if !dm.Enabled && projectDir != "" {
		if cleaned := cleanProjectDir(projectDir); cleaned != "" {
			store, lerr := LoadRootsStore()
			if lerr == nil {
				// A cwd already covered by a known root (the root itself or a
				// subdir) just refreshes that root's last_used — it rides the
				// existing mount and must never be learned as a shadow root.
				touched := ""
				for _, r := range store.Paths() {
					if cleaned == r || strings.HasPrefix(cleaned+string(os.PathSeparator), r+string(os.PathSeparator)) {
						touched = r
						break
					}
				}
				if touched != "" && !isUserHome(cleaned) {
					store.TouchRoot(touched, time.Now())
					_ = SaveRootsStore(store) //nolint:errcheck // best-effort last_used update; continue with in-memory set
				} else {
					learned, lerr2 := requestLearnRoot(cfg, projectDir)
					if lerr2 != nil {
						return nil, lerr2
					}
					if learned {
						learnedRoot = cleaned
					}
				}
			}
		}
	}

	if h, err := msb.GetSandbox(ctx, msbDaemonName); err == nil {
		if sbc, cerr := h.Config(); cerr == nil && sbc != nil {
			needRecreate := sbc.MemoryMiB < 2048
			reason := "memory below minimum"
			if !needRecreate {
				allowHome := cfg != nil && cfg.Sandbox.AllowHomeWorkspace
				// Decision inputs prefer the construct-owned spec file: a forked
				// daemon's msb record carries no labels (restore drops them), and
				// a stale record would read as total drift and recreate forever.
				needRecreate, reason = msbDaemonNeedsRecreate(dm, msbDaemonDecisionLabels(sbc.Labels), h.ConfigJSON(), projectDir, allowHome, cfg, msbCachedImageDigest())
			}
			if needRecreate {
				bootOutcome = msbBootRecreate
				bootReason = reason
				// Eligibility reads the raw reason: the learned-root message
				// below replaces it for display only, and a learned-root add is
				// a workspace-roots change underneath.
				forkable := msbRecreateForkable(reason)
				if learnedRoot != "" {
					bootReason = "learned root added: " + learnedRoot
				}
				ui.InfoF("🔄 Recreating microVM daemon sandbox (%s)...\n", bootReason)
				if forkable && msbDaemonInstallComplete() {
					ui.InfoLn("📸 Preserving installed tools: rebooting the daemon from a disk snapshot...")
					sb, ferr := forkMsbDaemonFromSnapshot(ctx, m, cfg, projectDir, bootRef)
					if ferr == nil {
						ui.InfoLn("✓ MicroVM environment ready (installed tools carried over)")
						bootOutcome = msbBootFork
						msbLogBoot(cfg, bootOutcome, bootStart, bootReason, bootCounts)
						return sb, nil
					}
					ui.InfoF("⚠️  Snapshot fork unavailable (%v); doing a full recreate (installs will re-run)...\n", ferr)
				}
				ui.InfoLn("   In-VM agent and OS updates revert to image versions; run 'construct sys update' to re-apply them.")
				if terr := m.forceRemoveMsbDaemon(ctx, msbDaemonName); terr != nil {
					// Reported, not fatal: the create below retries with a forced
					// removal if the record survived this pass.
					ui.InfoF("⚠️  Old daemon teardown incomplete (%v); the create below will retry with a forced removal.\n", terr)
				}
				goto create
			}
		} else {
			// The recreate decision reads the sandbox's stored config: the
			// memory floor plus the skills/mounts/sudo drift labels. A read
			// failure means every drift check is skipped this run (an old
			// msb record can predate config persistence entirely), so never
			// let that pass silently.
			detail := ""
			if cerr != nil {
				detail = cerr.Error()
			} else {
				detail = "empty sandbox config"
			}
			ui.InfoF("⚠️  Cannot read the daemon sandbox config (%s); skipping recreate checks this run. If the daemon behaves stale, run 'construct sys daemon recreate'.\n", detail)
		}

		if h.Status() == msb.SandboxStatusRunning {
			sb, cerr := h.Connect(ctx)
			if cerr == nil {
				// The default workload (entrypoint) does not survive reboots or
				// relaunch on its own: re-invoke it when the sleep-infinity
				// keeper is gone (entrypoint is idempotent — markers + probes),
				// then wait for it to reach the keeper before handing control to
				// the agent exec (first boot runs installs; needs the window).
				if out, eerr := sb.Exec(ctx, "test", []string{"-e", msbReadyMarker}); eerr != nil || out == nil || out.ExitCode() != 0 {
					ui.InfoLn("⏳ Waiting for microVM guest environment initialization...")
					msbRunDefaultAsync(sb)
					if werr := msbWaitKeeper(ctx, sb); werr != nil {
						return nil, fmt.Errorf("msb daemon entrypoint: %w (see `msb logs %s`)", werr, msbDaemonName)
					}
					ui.InfoLn("✓ MicroVM environment ready")
					bootOutcome = msbBootWarm
					bootReason = "ready marker missing; re-ran default workload"
					msbLogBoot(cfg, bootOutcome, bootStart, bootReason, bootCounts)
					return sb, nil
				}
				bootOutcome = msbBootReconnect
				bootReason = "ready marker present"
				msbLogBoot(cfg, bootOutcome, bootStart, bootReason, bootCounts)
				return sb, nil
			}
			return nil, fmt.Errorf("connect daemon sandbox: %w", cerr)
		}
		ui.InfoLn("🚀 Starting microVM daemon sandbox...")
		sb, serr := h.StartDetached(ctx)
		if serr != nil {
			// Draining (stop in flight): wait it out, then boot.
			if werr := h.Stop(ctx); werr == nil {
				sb, serr = h.StartDetached(ctx)
			}
		}
		if serr != nil {
			return nil, fmt.Errorf("start daemon sandbox: %w", serr)
		}
		ui.InfoLn("⏳ Waiting for microVM guest environment initialization...")
		msbRunDefaultAsync(sb)
		if werr := msbWaitKeeper(ctx, sb); werr != nil {
			return nil, fmt.Errorf("msb daemon entrypoint: %w (see `msb logs %s`)", werr, msbDaemonName)
		}
		ui.InfoLn("✓ MicroVM environment ready")
		bootOutcome = msbBootWarm
		bootReason = "stopped sandbox booted via StartDetached"
		msbLogBoot(cfg, bootOutcome, bootStart, bootReason, bootCounts)
		return sb, nil
	}

create:
	// Not present (or mount set changed): create with the workspace binds so
	// agent workdirs resolve — multi-path mode mounts every configured root;
	// single-path mode mounts the project dir.
	// Bridge ports are omitted: host bridges bind random ports at engine run
	// time, which cannot be baked into boot-time egress rules. Permissive
	// mode (default-allow) needs no rule; offline/strict bridge egress is
	// part of the Step 7 bridge wiring (docs/VMsv2.md).
	ui.InfoLn("🚀 Booting microVM daemon sandbox...")
	spec := BuildMsbRunSpec(cfg, msbDaemonName, projectDir, nil, bootRef)
	spec.Detached = true
	sb, err := CreateMsbSandbox(ctx, spec)
	if err != nil && msb.IsKind(err, msb.ErrSandboxAlreadyExists) {
		// A teardown that lost a race with a wedged shutdown leaves the
		// record behind; absorb that state once instead of failing the run.
		ui.InfoF("⚠️  A stale daemon record blocked the create (%v); forcing removal and retrying...\n", err)
		if terr := m.forceRemoveMsbDaemon(ctx, msbDaemonName); terr != nil {
			return nil, fmt.Errorf("create sandbox: %w (forced removal also failed: %w)", err, terr)
		}
		sb, err = CreateMsbSandbox(ctx, spec)
	}
	if err != nil {
		return nil, err
	}
	// Record the decision inputs for later forks (restore drops msb record
	// labels, so this file is the only carrier across a snapshot fork).
	if serr := saveMsbDaemonSpecFromRunSpec(spec); serr != nil {
		ui.InfoF("⚠️  Could not persist daemon spec (%v); the next run may recreate once more.\n", serr)
	}
	ui.InfoLn("⏳ Waiting for microVM guest environment initialization (first boot may take several minutes, please be patient)...")
	// First boot runs the full entrypoint (chown, installs) before the
	// ready marker; the agent exec must not race it.
	if werr := msbWaitKeeper(ctx, sb); werr != nil {
		return nil, fmt.Errorf("msb daemon entrypoint: %w (see `msb logs %s`)", werr, msbDaemonName)
	}
	ui.InfoLn("✓ MicroVM environment ready")
	if bootOutcome != msbBootRecreate {
		bootReason = "first create (no existing sandbox)"
	}
	msbLogBoot(cfg, bootOutcome, bootStart, bootReason, bootCounts)
	return sb, nil
}

// msbReadyMarker is touched by the entrypoint immediately before exec "$@".
// It lives on tmpfs: absent on every fresh boot, so its presence proves the
// entrypoint completed (installs, bridges, PATH) on THIS boot.
const msbReadyMarker = "/tmp/.construct_entrypoint_ready"

// msbWaitKeeper polls until the entrypoint posts its readiness marker or
// the timeout elapses. It emits periodic progress notices every 60 seconds.
func msbWaitKeeper(ctx context.Context, sb *msb.Sandbox) error {
	deadline := time.Now().Add(msbKeeperTimeout)
	start := time.Now()
	lastReport := time.Now()
	for time.Now().Before(deadline) {
		if out, err := sb.Exec(ctx, "test", []string{"-e", msbReadyMarker}); err == nil && out != nil && out.ExitCode() == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
			if time.Since(lastReport) >= 60*time.Second {
				mins := int(time.Since(start).Round(time.Minute) / time.Minute)
				if mins < 1 {
					mins = 1
				}
				ui.InfoF("⏳ Still initializing guest environment (%dm elapsed, please be patient)...\n", mins)
				lastReport = time.Now()
			}
		}
	}
	return errors.New("entrypoint did not reach the sleep keeper in time")
}

// msbKeeperTimeout bounds every boot wait (cold create, warm boot, fork).
// First-boot installs are the only slow case; every other boot path returns
// in well under a minute.
const msbKeeperTimeout = 10 * time.Minute

// msbRunDefaultAsync runs the default workload (entrypoint + sleep
// infinity) in the background; it dies with the sandbox on stop.
func msbRunDefaultAsync(sb *msb.Sandbox) {
	go func() {
		_, _ = sb.ExecDefault(context.Background()) //nolint:errcheck // workload result surfaced via sandbox logs
	}()
}

// MsbInstallAgents runs the one-shot first-run agent install inside a
// sandbox (msb analog of InstallAgentsAfterBuild): boot with the default
// entrypoint and a trivial command, wait for it to exit, verify exit code.
// The entrypoint performs the actual install (marker-gated, entrypoint.sh).
// The generated install script is (re)written into the mounted home first —
// PrepareBackendAgnostic owns this on the normal path; MsbInstallAgents
// regenerates it so a stale/empty script cannot silently skip the install.
func MsbInstallAgents(ctx context.Context, cfg *config.Config) error {
	if err := ensureMsbLaunchCompat(); err != nil {
		return err
	}
	ui.InfoLn("📦 Installing agents inside microVM sandbox...")
	if err := EnsureMsbVolumes(ctx); err != nil {
		return fmt.Errorf("msb agent install: %w", err)
	}
	if _, err := NewMsbBackend().EnsureImage(cfg); err != nil {
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

	spec := BuildMsbRunSpec(cfg, name, "", nil, "")
	spec.Name = name
	spec.Cmd = []string{"echo", "Installation complete"}
	// CreateMsbSandbox blocks on the one-shot default workload (the
	// entrypoint's install) and fails on a non-zero exit code.
	if _, err := CreateMsbSandbox(ctx, spec); err != nil {
		return err
	}
	// The one-shot sandbox has served its purpose; remove it so it cannot
	// sit stopped forever pinning the construct-box image (an install
	// sandbox blocked image cleanup on the Mac and needed a manual
	// removal). Best-effort: the install itself succeeded, and a failed
	// removal only leaves the pre-existing stopped record.
	if cerr := m.Cleanup(ctx, name); cerr != nil {
		ui.LogWarning(fmt.Sprintf("Could not remove the one-shot install sandbox (harmless, remove with 'msb rm %s'): %v", name, cerr))
	}
	ui.InfoLn("✓ MicroVM agent installation complete")
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

// GetMsbDaemonProjectDir returns the mounted project dir of the running msb daemon.
func GetMsbDaemonProjectDir(ctx context.Context) string {
	h, err := msb.GetSandbox(ctx, msbDaemonName)
	if err != nil {
		return ""
	}
	sbc, err := h.Config()
	if err != nil || sbc == nil {
		return ""
	}
	return sbc.Labels["construct.project_dir"]
}

// msb-boot: telemetry for EnsureMsbDaemon. One outcome tag per return path:
// cold (create, first boot with installs), recreate (sandbox torn down +
// rebuilt, reason included), fork (recreate served from a disk snapshot of
// the outgoing daemon — installs carried over), warm (stopped sandbox
// booted via StartDetached + keeper wait), reconnect (already running,
// marker present). The `msb-boot:` prefix is stable; numbers are greppable
// to fill section 10 of docs/VMsv2.md and gate phase 6 (snapshot fork).
//
// Logged via stderr (ui.InfoF) so it respects the run-path-output rule
// (AGENTS.md "Run-Path Output"); users capture via `2>log.txt`.

const (
	msbBootCold      = "cold"
	msbBootRecreate  = "recreate"
	msbBootFork      = "fork"
	msbBootWarm      = "warm"
	msbBootReconnect = "reconnect"
)

// msbImageEntrypoint is the construct-box image ENTRYPOINT (Dockerfile).
// The snapshot fork boots it explicitly: msb restore carries no workload or
// env surface, so the forked daemon's boot is an Exec with the current env.
const msbImageEntrypoint = "/usr/local/bin/entrypoint.sh"

// msbForkSnapshotPrefix names each fork's disk snapshot. Snapshot names
// scope per source sandbox ("<sandbox>:<name>"), and the daemon sandbox is
// destroyed and recreated between forks, so a reused name collides inside
// its group — every capture gets a unique, prefixed, timestamped name and
// older captures are pruned after a successful fork.
const msbForkSnapshotPrefix = "construct-daemon-fork-"

// captureForkSnapshot runs the full-capture flush ladder (Required, then
// Auto) for the fork's disk snapshot.
func captureForkSnapshot(ctx context.Context, snapName string) (*msb.SnapshotArtifact, error) {
	snap, err := msb.Snapshot.Create(ctx, msb.SnapshotCreateOptions{
		FromSandbox: msbDaemonName,
		Name:        snapName,
		Full:        true,
		GuestFlush:  msb.GuestFlushRequired,
	})
	if err == nil {
		return snap, nil
	}
	ui.InfoF("📸 Full writeback rejected (%v); capturing without it...\n", err)
	return msb.Snapshot.Create(ctx, msb.SnapshotCreateOptions{
		FromSandbox: msbDaemonName,
		Name:        snapName,
		Full:        true,
		GuestFlush:  msb.GuestFlushAuto,
	})
}

// recycleDaemonForCapture re-spawns the daemon's VMM through a stop +
// detached boot so it accepts capture ops from this process (see the
// retry at the call site). Restores the readiness state before returning.
func recycleDaemonForCapture(ctx context.Context, h *msb.SandboxHandle) error {
	if err := h.Stop(ctx, msb.WithStopTimeout(30*time.Second)); err != nil {
		return fmt.Errorf("recycle stop: %w", err)
	}
	sb, err := h.StartDetached(ctx)
	if err != nil {
		return fmt.Errorf("recycle boot: %w", err)
	}
	msbRunDefaultAsync(sb)
	if err := msbWaitKeeper(ctx, sb); err != nil {
		_ = h.Stop(ctx, msb.WithStopTimeout(30*time.Second)) //nolint:errcheck // cold fallback proceeds
		return fmt.Errorf("recycle readiness: %w", err)
	}
	return nil
}

// msbRecreateForkable reports whether a recreate reason only re-declares
// host bind mounts or restarts the engine. Those recreates exist because
// msb defines binds at boot and the VMM comes from this binary: the guest
// root disk (installs, entrypoint hash gate) stays valid, so the
// replacement can boot from a disk snapshot of the outgoing daemon and
// skip the full reinstall. Reasons implying a root-disk or policy reset
// must cold-recreate: sudo policy is applied by the entrypoint at creation
// only, image drift means stale baked content, and the remaining reasons
// repair config/label state the fork cannot set.
func msbRecreateForkable(reason string) bool {
	switch reason {
	case "host skills mounts changed (source, mode, or targets)",
		"daemon.mount_paths changed",
		"workspace roots changed (learned root added, removed, or evicted)",
		"embedded msb runtime changed (construct upgrade)":
		return true
	}
	return false
}

// msbDaemonInstallComplete reports whether the daemon's bind-side
// setup-completion marker exists: the outgoing root disk finished its
// entrypoint install at least once. A snapshot of a half-installed disk
// would poison the restored boot with a torn dpkg state, so a daemon whose
// first boot never completed always cold-recreates.
func msbDaemonInstallComplete() bool {
	home := msbHostConstructHome()
	if home == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(home, ".local", ".construct_setup_complete"))
	return err == nil
}

// forkMsbDaemonFromSnapshot replaces the daemon sandbox while preserving
// its root disk: full snapshot (disk + checkpoint) of the running daemon ->
// stop -> destroy -> disk-only restore into the same name with the NEW
// mount set. The restored boot runs the image entrypoint explicitly (restore
// has no workload/env surface) with the CURRENT env; its root-fs hash gate
// matches the snapshot's disk, so installs skip and readiness lands in
// seconds instead of the full reinstall.
//
// Every failure path leaves the daemon name free (best-effort Cleanup) so
// the caller's cold-recreate fallback proceeds; the error is reported, and
// never fatal to the run.
func forkMsbDaemonFromSnapshot(ctx context.Context, m *MsbBackend, cfg *config.Config, projectDir, bootRef string) (*msb.Sandbox, error) {
	h, err := msb.GetSandbox(ctx, msbDaemonName)
	if err != nil {
		return nil, fmt.Errorf("daemon lookup: %w", err)
	}
	// A full capture needs a RUNNING source (checkpoint state). The recreate
	// decision also fires on boot-after-stop (e.g. roots add + daemon
	// restart), so boot the stopped daemon first; its gate usually matches
	// and this is seconds. A failure here falls back to the cold path.
	if h.Status() != msb.SandboxStatusRunning {
		sbBoot, berr := h.StartDetached(ctx)
		if berr != nil {
			return nil, fmt.Errorf("pre-fork boot: %w", berr)
		}
		msbRunDefaultAsync(sbBoot)
		if werr := msbWaitKeeper(ctx, sbBoot); werr != nil {
			_ = h.Stop(ctx, msb.WithStopTimeout(30*time.Second)) //nolint:errcheck // best-effort; cold fallback proceeds
			return nil, fmt.Errorf("pre-fork boot: %w", werr)
		}
	}
	// Full capture REQUIRES a running source (checkpoint state). Capture
	// BEFORE Stop: a stopped source has no checkpoint and disk-only restore
	// rejects it. Flush ladder: Required asks the guest for a full writeback
	// (no lost tail writes); some guests reject that control op on bind-
	// heavy sandboxes, so fall back to Auto — a crash-consistent disk where
	// only the last few seconds of page-cache writes can be missing. The
	// entrypoint hash gate and dpkg state survive either way; a lost gate
	// write only means one reinstall on the restored boot.
	snapName := fmt.Sprintf("%s%d", msbForkSnapshotPrefix, time.Now().Unix())
	snap, err := captureForkSnapshot(ctx, snapName)
	if err != nil {
		// Engine quirk (verified live): a VMM spawned by an in-process create
		// rejects capture ops from later client processes with "control
		// operation rejected by peer", while StartDetached spawns accept
		// them. Recycle the daemon through a stop + detached boot and retry
		// once — a warm boot is seconds against the ~2 min cold fallback.
		ui.InfoF("📸 Capture rejected (%v); recycling the daemon through a detached boot and retrying...\n", err)
		if rerr := recycleDaemonForCapture(ctx, h); rerr != nil {
			return nil, fmt.Errorf("snapshot daemon disk: %w (recycle failed: %w)", err, rerr)
		}
		snap, err = captureForkSnapshot(ctx, snapName)
	}
	if err != nil {
		return nil, fmt.Errorf("snapshot daemon disk: %w", err)
	}
	if err := h.Stop(ctx, msb.WithStopTimeout(30*time.Second)); err != nil {
		return nil, fmt.Errorf("stop daemon: %w", err)
	}
	if err := m.Cleanup(ctx, msbDaemonName); err != nil {
		return nil, fmt.Errorf("cleanup daemon: %w", err)
	}
	// The replacement's spec: NEW mount set, labels, and env. The labels
	// also feed the construct-owned daemon spec (below) because the msb
	// record loses its labels across a restore.
	spec := BuildMsbRunSpec(cfg, msbDaemonName, projectDir, nil, bootRef)
	restoreCfg := msb.RestoreConfig{
		SnapshotDiskOnly: true,
		Volumes:          spec.Mounts,
	}
	if spec.CPUs > 0 {
		restoreCfg.CPUs = &spec.CPUs
	}
	if spec.MemoryMiB > 0 {
		restoreCfg.MemoryMiB = &spec.MemoryMiB
	}
	restored, err := msb.RestoreSandbox(ctx, snap, msbDaemonName,
		msb.WithRestoreConfig(restoreCfg),
		// The volumes are construct-managed host paths built above; restore
		// requires explicit authorization to bind local sources.
		msb.WithDangerouslyInheritResources(),
	)
	if err != nil {
		_ = m.Cleanup(ctx, msbDaemonName) //nolint:errcheck // free the name for the cold fallback
		return nil, fmt.Errorf("restore daemon disk: %w", err)
	}
	if err := msbSeedAutoFiles(ctx, restored); err != nil {
		_ = m.Cleanup(ctx, msbDaemonName) //nolint:errcheck // free the name for the cold fallback
		return nil, err
	}
	// Restore carries no workload or env: boot the image entrypoint (its
	// hash gate matches the snapshot's disk, so installs skip) with the
	// current env, then wait for the readiness marker like every boot path.
	go func() {
		_, _ = restored.Exec(context.Background(), msbImageEntrypoint, //nolint:errcheck // boot result asserted via the readiness marker
			[]string{"sleep", "infinity"}, msb.WithExecEnv(spec.Env))
	}()
	if werr := msbWaitKeeper(ctx, restored); werr != nil {
		_ = m.Cleanup(ctx, msbDaemonName) //nolint:errcheck // free the name for the cold fallback
		return nil, fmt.Errorf("restored daemon entrypoint: %w (see `msb logs %s`)", werr, msbDaemonName)
	}
	// Land the decision inputs BEFORE returning: the next EnsureMsbDaemon
	// must not read the now-stale record labels (empty after a restore).
	if serr := saveMsbDaemonSpecFromRunSpec(spec); serr != nil {
		ui.InfoF("⚠️  Could not persist daemon spec (%v); the next run may recreate once more.\n", serr)
	}
	// Prune earlier fork snapshots (best-effort): each is a full disk image.
	msbPruneForkSnapshots(ctx, snapName)
	return restored, nil
}

// msbPruneForkSnapshots removes fork snapshots of the daemon except keep
// (the freshest capture). Best-effort throughout; snapshot hygiene must
// never fail a run.
func msbPruneForkSnapshots(ctx context.Context, keep string) {
	listed, err := msb.Snapshot.List(ctx)
	if err != nil {
		return
	}
	scopedPrefix := msbDaemonName + ":" + msbForkSnapshotPrefix
	for _, s := range listed {
		name := s.Name()
		if name == nil || !strings.HasPrefix(*name, scopedPrefix) {
			continue
		}
		if strings.TrimPrefix(*name, msbDaemonName+":") == keep {
			continue
		}
		_ = msb.Snapshot.Remove(ctx, *name, true) //nolint:errcheck // best-effort prune
	}
}

// msbBootClock is the clock used for msb-boot: telemetry. Tests override
// it to produce deterministic durations without sleeping.
var msbBootClock = time.Now

// msbBootCounts carries the three measurements behind the msb-boot log
// fields. Mounts = total sandbox bind count (home + workspace roots +
// conditional automounts); Learned = size of the learned-roots store;
// CwdMounted = the run carried a workspace cwd (learned, pinned, or
// appended one-off). The old single "roots=N" field conflated all three:
// 15 on `ct pi` (cwd appended) vs 14 on `ct sys update` (no cwd) read as
// "a root was lost". Split fields make each dimension readable.
type msbBootCounts struct {
	Mounts     int
	Learned    int
	CwdMounted bool
}

// msbBootCountsFor is the source for the msb-boot measurement fields.
// Tests override it to keep the log assertion deterministic when the real
// mount map is empty.
var msbBootCountsFor = func(cfg *config.Config, projectDir string) msbBootCounts {
	counts := msbBootCounts{Mounts: len(msbSandboxMounts(cfg, projectDir))}
	store, err := LoadRootsStore()
	if err != nil {
		store = RootsStore{Version: rootsStoreVersion}
	}
	counts.Learned = len(store.Paths())
	counts.CwdMounted = cleanProjectDir(projectDir) != ""
	return counts
}

// msbTelemetryEvent is the canonical (wide) event for one daemon boot.
// Environment context travels with every measurement so a log bundle from
// any machine is self-describing (which construct build, which host msb,
// which platform). The msb_version field is what makes version-skew
// regressions visible in the wild: the host CLI and the embedded SDK pin
// must match exactly, and this field records the host side per boot.
type msbTelemetryEvent struct {
	Event            string `json:"event"`
	TS               string `json:"ts"`
	ConstructVersion string `json:"construct_version"`
	MsbVersion       string `json:"msb_version"`
	OS               string `json:"os"`
	Arch             string `json:"arch"`
	Outcome          string `json:"outcome"`
	Seconds          int    `json:"seconds"`
	Mounts           int    `json:"mounts"`
	Learned          int    `json:"learned"`
	CwdMounted       bool   `json:"cwd_mounted"`
	Reason           string `json:"reason"`
}

// msbHostVersion is the source for the telemetry msb_version field. One
// cheap exec per boot event; failures degrade to "unknown" — skew
// debugging needs the field present, never the run to fail.
// msbHostVersion is the source for the telemetry msb_version field.
// Memoized per process: at most one exec per construct invocation, and
// bounded by the 2s context timeout. Failures degrade to "unknown" — skew
// debugging needs the field present, never the run to fail.
var msbHostVersion = sync.OnceValue(func() string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "msb", "--version")
	cmd.Stdin = nil // msb stdin trap: open pipe hangs (docs/VMsv2.md)
	out, err := cmd.Output()
	if err != nil {
		return "unknown"
	}
	// `msb --version` prints "msb 0.7.2"; keep just the semver so the
	// JSONL field parses without string surgery downstream.
	return strings.TrimPrefix(strings.TrimSpace(string(out)), "msb ")
})

// MsbLaunchVerdict is the compatibility ruling for a host msb CLI version
// against the runtime the embedded SDK launches.
type MsbLaunchVerdict struct {
	OK         bool // launch may proceed
	Warning    bool // proceed, but flag it (doctor reporting)
	HardStop   bool // untestable pairing (doctor escalates to an error)
	Message    string
	Suggestion string
}

// ClassifyMsbLaunch applies the host-msb launch policy, shared by the
// launch path and doctor so they can never diverge. The embedded SDK's
// FFI enforces this pairing internally at sandbox create (exact version
// match, or the legacy 0.6.x<=18 window) and refuses anything else with
// the cryptic "no tested sandbox launch contract"; classifying here turns
// that into actionable guidance. The planned construct-managed runtime
// pair removes the constraint entirely.
func ClassifyMsbLaunch(host, sdk string) MsbLaunchVerdict {
	if host == sdk {
		return MsbLaunchVerdict{OK: true, Message: fmt.Sprintf("Host msb matches the embedded runtime (%s)", host)}
	}
	hMaj, hMin, hPat, hOK := parseSemverTriplet(host)
	if !hOK {
		return MsbLaunchVerdict{
			HardStop:   true,
			Message:    fmt.Sprintf("Unrecognized host msb version %q", host),
			Suggestion: "Reinstall the host msb CLI, then retry",
		}
	}
	align := "Align versions: run `msb update` for the host CLI and `ct sys self-update` for construct, then retry"
	switch {
	case hMaj == 0 && hMin == 6 && hPat <= 18:
		// The FFI's legacy window: launches, but construct only exercises
		// 0.7.x, so flag it rather than bless it.
		return MsbLaunchVerdict{OK: true, Warning: true, Message: fmt.Sprintf("Host msb %s uses the SDK's legacy launch contract", host), Suggestion: align}
	case hMaj >= 1:
		return MsbLaunchVerdict{
			HardStop:   true,
			Message:    fmt.Sprintf("Host msb %s is a 1.x release; construct launches with %s and has no tested contract for it", host, sdk),
			Suggestion: align,
		}
	}
	sMaj, sMin, sPat, sOK := parseSemverTriplet(sdk)
	if sOK && tupleNewer(hMaj, hMin, hPat, sMaj, sMin, sPat) {
		return MsbLaunchVerdict{
			Message:    fmt.Sprintf("Host msb %s is newer than construct's embedded runtime %s", host, sdk),
			Suggestion: fmt.Sprintf("Run `ct sys self-update` to get a construct embedding msb %s, then retry", host),
		}
	}
	return MsbLaunchVerdict{
		Message:    fmt.Sprintf("Host msb %s cannot launch with construct's embedded runtime %s", host, sdk),
		Suggestion: "Run `msb update`, then retry; if it lands a version construct does not embed yet, run `ct sys self-update` instead",
	}
}

// parseSemverTriplet parses an x.y.z version into non-negative ints.
// Build-metadata suffixes ("0.7.6-rc1") are rejected: the SDK compares
// exact strings first, and suffixed hosts are genuinely unmatched.
func parseSemverTriplet(v string) (maj, mi, pat int, ok bool) {
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return 0, 0, 0, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return 0, 0, 0, false
		}
		switch i {
		case 0:
			maj = n
		case 1:
			mi = n
		case 2:
			pat = n
		}
	}
	return maj, mi, pat, true
}

// tupleNewer reports whether a > b component-wise.
func tupleNewer(aMaj, aMin, aPat, bMaj, bMin, bPat int) bool {
	if aMaj != bMaj {
		return aMaj > bMaj
	}
	if aMin != bMin {
		return aMin > bMin
	}
	return aPat > bPat
}

// ensureMsbLaunchCompat fails fast when the host msb cannot launch with
// the embedded SDK, before any image acquisition spends bandwidth on a
// sandbox create that the FFI would refuse.
// Embedded-runtime builds skip the host check entirely: construct launches
// its own extracted pair, so whatever the host has on PATH is irrelevant
// (the create itself proves the pair works).
func ensureMsbLaunchCompat() error {
	if msbembed.Available() {
		return nil
	}
	host := msbHostVersion()
	if host == "" || host == "unknown" {
		return fmt.Errorf("could not read the host msb version (`msb --version` failed)")
	}
	v := ClassifyMsbLaunch(host, msb.SDKVersion())
	if v.OK {
		return nil
	}
	return &cerrors.ConstructError{
		Category:   cerrors.ErrorCategoryRuntime,
		Operation:  "microVM launch compatibility check",
		Suggestion: v.Suggestion,
		Err:        fmt.Errorf("%s", v.Message),
	}
}

// telemetryEnabled reports whether local telemetry file collection is on.
// A nil config means defaults, and telemetry defaults to true (opt-out).
func telemetryEnabled(cfg *config.Config) bool {
	return cfg == nil || cfg.Runtime.Telemetry
}

// msbLogBoot emits the structured `msb-boot:` line. Format is fixed so
// downstream tooling can grep for `msb-boot:` and parse the fields
// without coordinating on a new schema. The outcome + reason carry the
// semantics; seconds + counts carry the measurements.
//
// Locally it also appends two artifacts under <config>/logs/, both gated
// by [runtime] telemetry (default true, local-only by design — nothing is
// ever sent over the network). Both files are size-capped (truncated at
// 5 MB) so telemetry cannot grow without bound:
//   - msb-boot.log: the same line RFC3339-stamped (dogfood P0 greps)
//   - msb-telemetry.jsonl: one canonical wide event per boot with the
//     environment context (construct + host msb versions, os/arch) so a
//     log bundle from any machine answers "which version pairing failed,
//     how, how often" without another data source.
func msbLogBoot(cfg *config.Config, outcome string, start time.Time, reason string, counts msbBootCounts) {
	seconds := int(msbBootClock().Sub(start).Round(time.Second).Seconds())
	if seconds < 0 {
		seconds = 0
	}
	ui.InfoF("msb-boot: outcome=%s seconds=%d mounts=%d learned=%d cwd_mounted=%t reason=%q\n",
		outcome, seconds, counts.Mounts, counts.Learned, counts.CwdMounted, reason)
	if !telemetryEnabled(cfg) {
		return
	}
	// Persistent copies: stderr is ephemeral, and the P6 gate greps
	// ~/.config/construct-cli/logs/*.log for these lines (dogfood guide
	// P0). Without the append the medians are uncollectable.
	logDir := filepath.Join(config.GetConfigDir(), "logs")
	//nolint:errcheck // telemetry is best-effort; appends below degrade to no-ops
	_ = os.MkdirAll(logDir, 0o700)

	capLogSize := func(path string) {
		if st, err := os.Stat(path); err == nil && st.Size() > 5*1024*1024 {
			//nolint:errcheck // best-effort telemetry
			_ = os.Rename(path, path+".1") // rotate: keep the last 5MB as <name>.1
		}
	}

	bootLogPath := filepath.Join(logDir, "msb-boot.log")
	capLogSize(bootLogPath)
	if f, err := os.OpenFile(bootLogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
		//nolint:errcheck // telemetry is best-effort; the stderr copy already fired
		fmt.Fprintf(f, "%s msb-boot: outcome=%s seconds=%d mounts=%d learned=%d cwd_mounted=%t reason=%q\n",
			time.Now().Format(time.RFC3339), outcome, seconds, counts.Mounts, counts.Learned, counts.CwdMounted, reason)
		//nolint:errcheck // telemetry is best-effort; stderr copy already fired
		_ = f.Close()
	}
	// Wide event (canonical log line): single structured record per boot.
	// Best-effort like the plain log: telemetry failures never fail the run.
	if ev, err := json.Marshal(msbTelemetryEvent{
		Event:            "msb-boot",
		TS:               time.Now().Format(time.RFC3339),
		ConstructVersion: constants.Version,
		MsbVersion:       msbHostVersion(),
		OS:               goruntime.GOOS,
		Arch:             goruntime.GOARCH,
		Outcome:          outcome,
		Seconds:          seconds,
		Mounts:           counts.Mounts,
		Learned:          counts.Learned,
		CwdMounted:       counts.CwdMounted,
		Reason:           reason,
	}); err == nil {
		telemetryPath := filepath.Join(logDir, "msb-telemetry.jsonl")
		capLogSize(telemetryPath)
		if f, ferr := os.OpenFile(telemetryPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); ferr == nil {
			//nolint:errcheck // telemetry is best-effort; the stderr copy already fired
			_, _ = f.Write(append(ev, '\n'))
			//nolint:errcheck // telemetry is best-effort; the stderr copy already fired
			_ = f.Close()
		}
	}
}

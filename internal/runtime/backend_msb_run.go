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
	"strings"
	"sync"
	"time"

	msb "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/EstebanForge/construct-cli/internal/config"
	"github.com/EstebanForge/construct-cli/internal/constants"
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
func BuildMsbRunSpec(cfg *config.Config, name, projectDir string, bridgePorts []int) *MsbRunSpec {
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
	return &MsbRunSpec{
		Name: name,
		// Resolve the cached ref (bare / localhost/ / full registry): the
		// daemon resolves image refs exactly as stored, and `msb load -i`
		// imports as localhost/construct-box:latest. Falls back to the
		// bare name when msb is unavailable (unit tests).
		Image:        MsbConstructImageRef(),
		Mounts:       msbSandboxMounts(cfg, projectDir),
		Network:      msbNetworkConfig(cfg.Network.Mode, bridgePorts),
		Env:          env,
		Labels:       labels,
		HostAliasEnv: msbHostAlias,
		CPUs:         4,
		MemoryMiB:    4096,
	}
}

// msbBaseEnv holds the backend-agnostic env every msb sandbox carries.
func msbBaseEnv(cfg *config.Config) []string {
	var envVars []string
	if lp := loopbackPortsString(cfg); lp != "" {
		envVars = append(envVars, "CONSTRUCT_LOOPBACK_PORTS="+lp)
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
// toolchain state survive daemon restarts (docs/VMs.md §7.1).
const msbDaemonName = "construct-cli-daemon"

// ErrMsbDaemonWorkdirUnmapped reports that the requested project dir falls
// outside every configured daemon.mount_paths root. Callers must not
// recreate the daemon sandbox in this case: the mount set is static config
// (Docker parity), and recreation would needlessly destroy the guest root
// disk (installs, toolchain state).
var ErrMsbDaemonWorkdirUnmapped = errors.New("msb daemon: current directory is outside the configured daemon mount paths")

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
func msbDaemonNeedsRecreate(dm DaemonMounts, sandboxLabels map[string]string, configJSON, projectDir string, allowHome bool, cfg *config.Config) (bool, string) {
	// Skills hash parity. Checked FIRST because it is the cheapest decision
	// (no mount parsing) and the most likely drift source in routine use.
	if currentSkills := SkillsDaemonHash(cfg); currentSkills != "" || sandboxLabels[DaemonSkillsLabelKey] != "" {
		if sandboxLabels[DaemonSkillsLabelKey] != currentSkills {
			return true, "host skills mounts changed (source, mode, or targets)"
		}
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
	// their parent mount).
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
		if !covered {
			roots = append(roots, cleaned)
		}
	}
	if currentProjectDir != "" && !slices.Contains(roots, currentProjectDir) {
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
	bootRoots := msbBootMountCount(cfg, projectDir)

	// Provision the image BEFORE the daemon lock: acquisition may now
	// block on an interactive build-confirmation prompt, and holding the
	// lock through it would stall every concurrent construct invocation.
	// Image provisioning never reads or mutates daemon state, so it needs
	// no serialization.
	m := NewMsbBackend()
	if err := m.EnsureImage(cfg); err != nil {
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
				if touched != "" {
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
				needRecreate, reason = msbDaemonNeedsRecreate(dm, sbc.Labels, h.ConfigJSON(), projectDir, allowHome, cfg)
			}
			if needRecreate {
				bootOutcome = msbBootRecreate
				bootReason = reason
				if learnedRoot != "" {
					bootReason = "learned root added: " + learnedRoot
				}
				ui.InfoF("🔄 Recreating microVM daemon sandbox (%s)...\n", bootReason)
				_ = h.Stop(ctx, msb.WithStopTimeout(30*time.Second)) //nolint:errcheck // best-effort stop before recreate
				_ = m.Cleanup(ctx, msbDaemonName)                    //nolint:errcheck // best-effort cleanup before recreate
				goto create
			}
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
					if werr := msbWaitKeeper(ctx, sb, 10*time.Minute); werr != nil {
						return nil, fmt.Errorf("msb daemon entrypoint: %w (see `msb logs %s`)", werr, msbDaemonName)
					}
					ui.InfoLn("✓ MicroVM environment ready")
					bootOutcome = msbBootWarm
					bootReason = "ready marker missing; re-ran default workload"
					msbLogBoot(cfg, bootOutcome, bootStart, bootReason, bootRoots)
					return sb, nil
				}
				bootOutcome = msbBootReconnect
				bootReason = "ready marker present"
				msbLogBoot(cfg, bootOutcome, bootStart, bootReason, bootRoots)
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
		if werr := msbWaitKeeper(ctx, sb, 10*time.Minute); werr != nil {
			return nil, fmt.Errorf("msb daemon entrypoint: %w (see `msb logs %s`)", werr, msbDaemonName)
		}
		ui.InfoLn("✓ MicroVM environment ready")
		bootOutcome = msbBootWarm
		bootReason = "stopped sandbox booted via StartDetached"
		msbLogBoot(cfg, bootOutcome, bootStart, bootReason, bootRoots)
		return sb, nil
	}

create:
	// Not present (or mount set changed): create with the workspace binds so
	// agent workdirs resolve — multi-path mode mounts every configured root;
	// single-path mode mounts the project dir.
	// Bridge ports are omitted: host bridges bind random ports at engine run
	// time, which cannot be baked into boot-time egress rules. Permissive
	// mode (default-allow) needs no rule; offline/strict bridge egress is
	// part of the Step 7 bridge wiring (docs/VMs.md §7 Step 7).
	ui.InfoLn("🚀 Booting microVM daemon sandbox...")
	spec := BuildMsbRunSpec(cfg, msbDaemonName, projectDir, nil)
	spec.Detached = true
	sb, err := CreateMsbSandbox(ctx, spec)
	if err != nil {
		return nil, err
	}
	ui.InfoLn("⏳ Waiting for microVM guest environment initialization (first boot may take several minutes, please be patient)...")
	// First boot runs the full entrypoint (chown, installs) before the
	// ready marker; the agent exec must not race it.
	if werr := msbWaitKeeper(ctx, sb, 10*time.Minute); werr != nil {
		return nil, fmt.Errorf("msb daemon entrypoint: %w (see `msb logs %s`)", werr, msbDaemonName)
	}
	ui.InfoLn("✓ MicroVM environment ready")
	if bootOutcome != msbBootRecreate {
		bootReason = "first create (no existing sandbox)"
	}
	msbLogBoot(cfg, bootOutcome, bootStart, bootReason, bootRoots)
	return sb, nil
}

// msbReadyMarker is touched by the entrypoint immediately before exec "$@".
// It lives on tmpfs: absent on every fresh boot, so its presence proves the
// entrypoint completed (installs, bridges, PATH) on THIS boot.
const msbReadyMarker = "/tmp/.construct_entrypoint_ready"

// msbWaitKeeper polls until the entrypoint posts its readiness marker or
// the timeout elapses. It emits periodic progress notices every 60 seconds.
func msbWaitKeeper(ctx context.Context, sb *msb.Sandbox, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
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
	ui.InfoLn("📦 Installing agents inside microVM sandbox...")
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
// rebuilt, reason included), warm (stopped sandbox booted via
// StartDetached + keeper wait), reconnect (already running, marker
// present). The `msb-boot:` prefix is stable; numbers are greppable to
// fill section 10 of docs/VMsv2.md and gate phase 6 (snapshot fork).
//
// Logged via stderr (ui.InfoF) so it respects the run-path-output rule
// (AGENTS.md "Run-Path Output"); users capture via `2>log.txt`.

const (
	msbBootCold      = "cold"
	msbBootRecreate  = "recreate"
	msbBootWarm      = "warm"
	msbBootReconnect = "reconnect"
)

// msbBootClock is the clock used for msb-boot: telemetry. Tests override
// it to produce deterministic durations without sleeping.
var msbBootClock = time.Now

// msbBootMountCount is the source for the "roots=N" field. Tests override
// to keep the log assertion deterministic when the real mount map is empty.
var msbBootMountCount = func(cfg *config.Config, projectDir string) int {
	return len(msbSandboxMounts(cfg, projectDir))
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
	Roots            int    `json:"roots"`
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
	cmd.Stdin = nil // msb stdin trap: open pipe hangs (docs/VMs.md §7.1)
	out, err := cmd.Output()
	if err != nil {
		return "unknown"
	}
	// `msb --version` prints "msb 0.7.2"; keep just the semver so the
	// JSONL field parses without string surgery downstream.
	return strings.TrimPrefix(strings.TrimSpace(string(out)), "msb ")
})

// telemetryEnabled reports whether local telemetry file collection is on.
// A nil config means defaults, and telemetry defaults to true (opt-out).
func telemetryEnabled(cfg *config.Config) bool {
	return cfg == nil || cfg.Runtime.Telemetry
}

// msbLogBoot emits the structured `msb-boot:` line. Format is fixed so
// downstream tooling can grep for `msb-boot:` and parse the fields
// without coordinating on a new schema. The outcome + reason carry the
// semantics; seconds + roots carry the measurements.
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
func msbLogBoot(cfg *config.Config, outcome string, start time.Time, reason string, rootCount int) {
	seconds := int(msbBootClock().Sub(start).Round(time.Second).Seconds())
	if seconds < 0 {
		seconds = 0
	}
	ui.InfoF("msb-boot: outcome=%s seconds=%d roots=%d reason=%q\n",
		outcome, seconds, rootCount, reason)
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
		fmt.Fprintf(f, "%s msb-boot: outcome=%s seconds=%d roots=%d reason=%q\n",
			time.Now().Format(time.RFC3339), outcome, seconds, rootCount, reason)
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
		Roots:            rootCount,
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

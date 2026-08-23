package runtime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	msb "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/EstebanForge/construct-cli/internal/config"
	"github.com/EstebanForge/construct-cli/internal/ui"
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
// project dir bind -> /workspaces/<name>, plus conditional auto-mounts (qmd models)
// when the host path exists. linuxbrew stays on the sandbox root disk.
func msbSandboxMounts(projectDir string) map[string]msb.MountConfig {
	mounts := map[string]msb.MountConfig{}
	if home := msbHostConstructHome(); home != "" {
		mounts[msbHomeMountDest] = msb.Mount.Bind(home, msb.MountOptions{
			StatVirtualization: msb.StatVirtualizationOff,
		})
	}

	if dir := cleanProjectDir(projectDir); dir != "" {
		dest := GetMsbWorkspaceMountDest(dir)
		mounts[dest] = msb.Mount.Bind(dir, msb.MountOptions{
			StatVirtualization: msb.StatVirtualizationOff,
		})
	}

	for _, m := range conditionalAutoMounts() {
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
// msbSandboxMounts (home, workspace, auto-mounts). Derived from the same
// sources so the host exec bridge can never drift from actual mounts.
// Maps are sorted longest guest-path first to resolve nested paths correctly.
func MsbPathMaps(projectDir string) []MsbPathMap {
	maps := []MsbPathMap{}
	if home := msbHostConstructHome(); home != "" {
		maps = append(maps, MsbPathMap{Guest: msbHomeMountDest, Host: home})
	}
	if dir := cleanProjectDir(projectDir); dir != "" {
		dest := GetMsbWorkspaceMountDest(dir)
		maps = append(maps, MsbPathMap{Guest: dest, Host: dir})
	}
	for _, m := range conditionalAutoMounts() {
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
	return &MsbRunSpec{
		Name:         name,
		Image:        "construct-box:latest",
		Mounts:       msbSandboxMounts(projectDir),
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

// msbDaemonName is the persistent sandbox backing the daemon mode under
// the msb backend (Docker analog: construct-cli-daemon container). Named
// sandboxes persist across stop/start, so agent installs and brew state on
// the root disk survive daemon restarts (docs/VMs.md §7.1).
const msbDaemonName = "construct-cli-daemon"

// EnsureMsbDaemon guarantees the persistent daemon sandbox exists and is
// running: create when missing, boot when stopped (default workload — the
// entrypoint + sleep infinity — is re-invoked explicitly; the SDK never
// auto-runs it, not at create and not at start). Returns the live sandbox.
func EnsureMsbDaemon(ctx context.Context, cfg *config.Config, projectDir string) (*msb.Sandbox, error) {
	m := NewMsbBackend()
	if err := m.EnsureImage(cfg); err != nil {
		return nil, err
	}

	if h, err := msb.GetSandbox(ctx, msbDaemonName); err == nil {
		if sbc, cerr := h.Config(); cerr == nil && sbc != nil {
			currentProjectDir, hasLabel := sbc.Labels["construct.project_dir"]
			needRecreate := sbc.MemoryMiB < 2048
			allowHome := cfg != nil && cfg.Sandbox.AllowHomeWorkspace
			if projectDir != "" {
				dest := GetMsbWorkspaceMountDest(currentProjectDir)
				if !hasLabel || currentProjectDir == "" {
					needRecreate = true
				} else if sbc.Volumes == nil || sbc.Volumes[dest].Bind != cleanProjectDir(currentProjectDir) {
					needRecreate = true
				} else if !allowHome && EvaluateWorkspace(currentProjectDir, 0).Risk == WorkspaceRiskHome {
					needRecreate = true
				} else if isGitRoot(cleanProjectDir(projectDir)) && cleanProjectDir(projectDir) != cleanProjectDir(currentProjectDir) {
					needRecreate = true
				} else if _, ok := MapDaemonWorkdir(projectDir, currentProjectDir, dest); !ok {
					needRecreate = true
				}
			}
			if needRecreate {
				ui.InfoLn("🔄 Recreating microVM daemon sandbox for workspace...")
				_ = h.Stop(ctx)                   //nolint:errcheck // best-effort stop before recreate
				_ = m.Cleanup(ctx, msbDaemonName) //nolint:errcheck // best-effort cleanup before recreate
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
				}
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
		return sb, nil
	}

create:
	// Not present: create with the project bind so agent workdirs resolve.
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

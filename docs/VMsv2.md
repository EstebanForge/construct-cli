# VMs v2: microVM UX Hardening Plan

Status: approved direction, phased implementation. Owner: Esteban. Last updated: 2026-08-26 (phase 7 added; skills daemon-recreate gap closed).

Scope: the microVM backend (`backend = "microvm"`, microsandbox SDK v0.6.10) and the cross-backend host-skills mount feature. This document is the forward plan that improves speed, ease of use, and user satisfaction without changing the construct-cli fundamentals and without weakening security.

Provenance: the approach was derived from studying Docker Sandboxes (`sbx`, docs.docker.com/ai/sandboxes) and survived two adversarial peer-review rounds (isolated reviewer sessions). The review history is recorded in section 9. The code comments in `internal/runtime/backend_msb_run.go` cite a `docs/VMs.md` that does not exist in the repo; the shipped design baseline is `docs/ARCHITECTURE-DESIGN.md` section 4.1 plus everything tagged 1.16.2 in `CHANGELOG.md`. This file supersedes those references as the forward plan.

## 1. Fundamentals (do not change these)

- ONE shared sandbox `construct-cli-daemon` backs all microVM runs. No per-workspace VMs, no per-agent images, no second execution model.
- One generic image `construct-box:latest` (GHCR). Agents are NOT baked into images. Agents install into the host bind `/home/construct` (`~/.config/construct-cli/home`) via `install_user_packages.sh` generated from `internal/templates/packages.toml`, gated by the `.entrypoint_hash` marker on the same bind.
- Mounts are create-time only in SDK v0.6.10 (`ModifyOptions` has no mounts field). Changing the mount set always means recreate. There is no hot-add. Do not search for one.
- Named sandboxes keep the root disk across stop/start. `Cleanup` (remove) destroys it. brew and apt state lives on the root disk and dies with it. npm and bun agent installs live on the host bind and survive everything.
- `WithIdleTimeout(0)` and `WithMaxDuration(0)` stay zero forever: an idle REBOOT does not re-run the default workload and kills the daemon model (see comment at `internal/runtime/backend_msb_run.go` in `CreateMsbSandbox`). Idle behavior is implemented host-side (phase 3), never by the SDK idle timeout.
- Run-path output goes to stderr (`ui.Info*`), never stdout (AGENTS.md "Run-Path Output").

## 2. Baseline: what 1.16.2 already shipped

- Multi-path daemon mounts: `daemon.multi_paths_enabled` + `daemon.mount_paths` mount every configured root under `/workspaces/<hash8>` (full-path hash, collision-free), gated by the `construct.daemon.mounts_hash` label. Recreate only when the configured set changes. Cwd outside the set returns `ErrMsbDaemonWorkdirUnmapped` (error, never destructive).
- Single-path default: subdirectories of the mounted root reuse the daemon; different roots recreate.
- Symlink resolution on both workdir mappers (`MapDaemonWorkdir`, `MapDaemonWorkdirFromMounts` in `internal/runtime/daemon_mounts.go`).
- Unified `[runtime] backend` key (`auto` default, `container`, `podman`, `docker`, `microvm`) with in-place config migration.

## 3. Discarded options (do not resurrect without new evidence)

- Per-workspace persistent VMs (sbx's core model). Rejected: N x 4096 MiB RAM, N root disks, full rewrite of every `msbDaemonName` assumption (bridges, doctor, `sys daemon` CLI, `MsbPathMaps`), and N entrypoints racing on the shared `/home/construct` markers.
- Ephemeral per-run sandboxes from a fat image. Rejected: image rebuild on every tooling change; contradicts the host-bind agent model.
- Ephemeral per-run sandboxes forked from a snapshot as THE RUN MODEL. Rejected: its value was coupled to per-workspace VMs. The snapshot primitive survives only as a recreate accelerator (phase 6, gated).
- A config knob choosing daemon vs non-daemon under microVM. Superseded: the real complaint was "forced always-on", solved by idle stop (phase 3) at a fraction of the cost.
- Per-agent template images (sbx `sandbox-templates`). Rejected: loses packages.toml same-day flexibility and the single shared image.

## 4. Phase order (corrected by review; ship in this order)

Ordering rule learned the hard way: the lock ships BEFORE learned roots. Shipping state-mutation without the lock ships the race.

1. Phase 0: boot telemetry (item 4)
2. Phase 1: widened flock (item 3)
3. Phase 2: learned roots + prompt + LRU + `daemon roots` command (item 1)
4. Phase 3: idle stop, own timer, session-based (item 2)
5. Phase 4: background image pre-pull (item 6)
6. Phase 5: credential proxy design doc (item 7)
7. Phase 6: snapshot fork, gated on phase 0 numbers (item 5)
8. Phase 7: host skills mount, docker + microvm (item 8)

## 5. Phases

### Phase 0: boot telemetry

Goal: measure before optimizing. Every later decision (especially phase 6) is gated on these numbers.

Design: instrument `EnsureMsbDaemon` in `internal/runtime/backend_msb_run.go` with duration logging for four events, tagged with an outcome: `cold` (create path, first boot with installs), `recreate` (with the reason string already produced by `msbDaemonNeedsRecreate`), `warm` (stopped sandbox booted via `StartDetached` + `msbWaitKeeper`), `reconnect` (already running, marker present). `msbWaitKeeper` already tracks `start`; extend it to return the elapsed wait. Log via `ui.LogInfo` (or the structured log the daemon already writes) with a stable prefix such as `msb-boot:` so numbers are greppable later, example: `msb-boot: outcome=warm seconds=23 root=/path`.

- [x] P0.1 Instrument cold, recreate, warm, reconnect durations in `EnsureMsbDaemon` with the `msb-boot:` prefix
- [x] P0.2 Include the recreate reason and the mounted root count in the log line
- [x] P0.3 Unit test: the four outcome tags are emitted (fake clock or injectable timer if needed; do not sleep in tests)
- [ ] P0.4 Dogfood on macOS for one week, collect numbers, record median cold vs warm vs reconnect in this file (section 10)

Acceptance: `grep msb-boot` over a week of logs answers "what does a warm boot cost" and "how often do recreates happen".

### Phase 1: widened flock

Goal: serialize daemon state mutations. The critical section MUST wrap, as one unit: read roots state, decide recreate, write roots state, perform recreate/boot. Two concurrent `ct` invocations learning two different roots must never produce last-write-wins root loss or a double recreate.

Design: lock file `~/.config/construct-cli/daemon.lock` (create dir if missing, mode 0600). Acquire with `syscall.Flock(fd, LOCK_EX)` (blocking) in `EnsureMsbDaemon` before reading any daemon state, release on return. Blocking is correct: a 10-minute first boot behind the lock is expected, not exceptional. Print a notice when the lock is not acquired within ~250ms: "Waiting for another construct invocation to finish daemon setup...". Release must happen on ALL return paths (`defer`). The lock is host-local; one per construct config dir.

- [x] P1.1 Add `internal/runtime/daemon_lock.go` with `acquireDaemonLock() (release func(), err error)` using a blocking `syscall.Flock`
- [x] P1.2 Wire acquisition at the top of `EnsureMsbDaemon`; `defer` release; wrap the entire decision + learn + recreate sequence
- [x] P1.3 Waiting notice after 250ms blocked (a goroutine + timer, cancel on acquire)
- [x] P1.4 Test: two goroutines enter `EnsureMsbDaemon`'s critical section, second observes the lock (unit test on the lock helper with a held fd)
- [x] P1.5 Test: release happens on error paths (recreate failure does not strand the lock)
- [x] P1.6 Document the lock file in `construct sys doctor` output (show "daemon lock: free/held")

### Phase 2: learned roots

Goal: default users (no `mount_paths` config) get per-project warmth with one shared VM and zero configuration. Today they recreate on EVERY root hop; after this phase they recreate ONCE per new root, then reuse forever.

State: `~/.config/construct-cli/roots.json`. Format: `{"version":1,"roots":[{"path":"/abs/resolved","learned_at":"RFC3339","last_used":"RFC3339"}]}`. Paths are symlink-resolved (reuse `cleanProjectDir`). Writes only inside the flock critical section. Update `last_used` on every successful mount-set resolution (best-effort, also inside the lock; if the write fails, log and continue with the in-memory set).

Semantics:
- Configured `daemon.mount_paths` roots are pinned: always mounted, never evicted, do not consume LRU slots.
- Learned roots are capped: default 8, configurable as `daemon.max_learned_roots`. Eviction is LRU by `last_used`. Eviction changes the mount set hash, so it recreates on next run; prefer evicting only when the cap is exceeded.
- Learning a new root prompts once when interactive: "Add <root> to the daemon's mounted roots?" (reuse the `EnforceWorkspace` pattern: `term.IsTerminal` + `ui.GumConfirm`). NON-INTERACTIVE SESSIONS (agents, scripts, CI) MUST DENY, never hang: print "cd into <root> and run construct once interactively to add it, or add it to daemon.mount_paths" and return `ErrMsbDaemonWorkdirUnmapped`.
- Every learned root still passes `EvaluateWorkspace` before entering the set. System roots are refused; the host home root warns. Note for implementers: `EvaluateWorkspace` returns OK unconditionally for any git root (see `internal/runtime/workspace_guard.go`); the prompt-on-learn is the real consent gate, the guard is only a floor.
- The combined set (configured + learned) feeds one hash. Extend `ResolveDaemonMounts` in `internal/runtime/daemon_mounts.go` (or add a wrapper `ResolveDaemonMountsWithLearned(cfg, learned)`) so `msbSandboxMounts`, `MsbPathMaps`, `BuildMsbRunSpec`, and `msbDaemonNeedsRecreate` all derive from the same combined source. The label stays `construct.daemon.mounts_hash`.
- Recreate message when a root is learned: "🔄 Recreating microVM daemon sandbox (learned root added: <root>)...".

New CLI surface (mirror the existing `sys daemon` verb style in `internal/daemon/daemon.go` and its command registration):
- `construct sys daemon roots`: table of root, source (configured/learned), last used, mount dest.
- `construct sys daemon roots forget <path>`: remove a learned root (refuse to forget configured roots; point at config.toml), then note that the next run recreates with the smaller set.

Edge cases to handle explicitly: concurrent learn of two roots (covered by phase 1 lock), root deleted from disk between runs (stat check; drop silently with a log line), root that resolves through a symlink (store resolved), cap reached (evict LRU, warn once).

- [x] P2.1 `internal/runtime/roots_store.go`: load/save/update roots.json with version field, atomic write (`writeFileAtomic` pattern from `internal/config/config.go`), and LRU cap
- [ ] P2.2 Combined mount-set resolution (configured pinned + learned LRU) with single hash; all four consumers derive from it
  - P2.2 stubs already shipped: `ResolveDaemonMountsWithLearned` is a no-op wrapper that currently delegates to `ResolveDaemonMounts` (placeholder for the unified set). `requestLearnRoot` is shipped with `nolint:unused` because the call site in `EnsureMsbDaemon` is part of this item. The system-root workspace guard inside `requestLearnRoot` is a defensive backstop; in practice `cleanProjectDir` filters system roots upstream so the backstop never fires.
- [x] P2.3 Prompt-on-learn interactive path; hard deny + actionable message non-interactive path; helper shipped (covered by tests; EnsureMsbDaemon call site is part of P2.2 wire-in)
- [x] P2.4 `EvaluateWorkspace` floor + refusal of configured-root forgetting
- [x] P2.5 `construct sys daemon roots` list command + tests
- [x] P2.6 `construct sys daemon roots forget <path>` + tests
- [ ] P2.7 Update `docs/CONFIGURATION.md` (learned roots, cap, prompt behavior, the command) and the config template comment block
- [ ] P2.8 Dogfood: two projects, verify exactly one recreate on first visit to the second, zero recreates thereafter

### Phase 3: idle stop

Goal: nothing runs while the user is away. The daemon VM stops after N minutes with zero live construct-owned sessions. State persists (stopped, not removed). Bridges die with it, shrinking the host attack surface to zero while idle.

Design (host-side only, per the fundamentals):
- Session registry: `~/.config/construct-cli/sessions/` with one file per live run (`<pid>.json`: pid, sandbox, started_at). Register in the engine run path (`execViaMsbDaemon`, after `EnsureMsbDaemon` succeeds), unregister in the existing Teardown. Stale entries (pid not alive) are ignored and swept on next scan.
- Idle watcher: on the LAST unregister (count hits zero), spawn a detached helper `construct sys daemon idle-watch` (best-effort `exec.Command` with `StartNotWait`-style launch, output to the log dir). The helper sleeps `daemon.idle_stop_minutes` (default 45, 0 disables the feature entirely), rechecks the registry, and if still zero calls the existing stop path (`RequestStop` + wait-for-stopped loop; reuse `stopMsb` logic in `internal/daemon/daemon.go`). If the helper dies, the VM simply stays running (status quo; no correctness loss).
- NEVER use SDK `WithIdleTimeout` for this (see fundamentals). Idle is defined as "zero registered sessions", never VM CPU: an agent thinking for 20 minutes or a long build keeps its session registered and the VM alive.
- A run arriving during the countdown: it registers a session; the helper rechecks after the sleep and stands down.
- Warm restart already works: the stopped-daemon path (`StartDetached` + `ExecDefault` + `msbWaitKeeper`) exists today and is exercised by `sys daemon start`.

- [x] P3.1 Session registry helper (register/unregister/sweep-stale) + unit tests
- [x] P3.2 Wire register/unregister into `execViaMsbDaemon` and Teardown
- [x] P3.3 `idle-watch` helper command (sleep, recheck, stop) + config `daemon.idle_stop_minutes` (default 45)
- [x] P3.4 Spawn-on-last-unregister logic, best-effort, logged
- [x] P3.5 Test: registry count zero after unregisters; stale pid swept; idle-watch stands down when a session appears
- [ ] P3.6 Update `docs/CONFIGURATION.md` + config template comment; mention in `construct sys daemon status`
  - Partial: config template updated for `daemon.idle_stop_minutes`; `ct sys daemon status` now prints "Live sessions: N". Full `docs/CONFIGURATION.md` write still pending (defer to a docs sweep, not blocking).

### Phase 4: background image pre-pull

Goal: remove the first-run "why is this downloading gigabytes" surprise. Install and update pull `construct-box` in the background.

Design: after a successful `construct update` (and on first-run init when `backend = "microvm"` is detected in config), spawn a detached best-effort process running the equivalent of the GHCR pull path in `EnsureImage` (`msb pull ghcr.io/estebanforge/construct-box:latest` + `msb image tag ... construct-box:latest`), output appended to the construct log dir. Never blocks, never fails the parent command. Opt-out: `runtime.prepull_image = false`. Note: `EnsureImage` currently pulls `:latest` only; keep that behavior here (do not "fix" it in this phase).

- [x] P4.1 Detached pre-pull helper (pull + tag + log), reused by update and first-run paths
- [x] P4.2 Config flag `runtime.prepull_image` (default true) + template/docs
- [ ] P4.3 Verify: fresh sandbox image store + update run -> later `ct` skips the pull in `EnsureImage` (wall-clock, requires live msb)

### Phase 5: credential proxy design doc (no implementation)

Goal: provider API keys stop crossing the VM boundary. This phase delivers `docs/CREDS-PROXY.md` only. Implementation is a separate effort after the design is reviewed.

Hard constraints recorded from review (the design must address each):
- Header injection requires TLS termination. A blind TCP relay (the socat pattern in `internal/templates/entrypoint.sh`) cannot inject `Authorization`. The design needs a construct-generated CA installed in the image trust store, a host-side HTTPS proxy, and a per-provider hostname allowlist.
- Guest wiring: `HTTPS_PROXY` (and `HTTP_PROXY`) point at `host.microsandbox.internal` on a fixed port; egress-to-host is already permitted by `msbHostTransportRules` in all network modes.
- Token storage host-side: macOS Keychain / Linux Secret Service, with a permissions-protected file fallback (sbx precedent); never in guest env, never in the image.
- The proxy only pays off if provider keys STOP being passed as guest env. The design must include the removal plan for the env passthrough of provider keys (`collectForwardedEnv` in `internal/agent/engine.go` and friends) or the proxy is theater.
- Interaction with network modes: offline still allows host transport; strict/permissive policy rules need the proxy port allowed guest-to-host.

- [x] P5.1 Write `docs/CREDS-PROXY.md`: threat model, component diagram, CA lifecycle, per-provider rule format, token store, guest env removal plan, rollout flags
- [ ] P5.2 Review round on the design doc (isolated peer review, same protocol as this plan)

### Phase 6: snapshot fork (gated; do not start before the gate opens)

Gate: phase 0 numbers recorded in section 10 show median cold recreate above ~120 seconds AND recreates occur in the normal workflow (once per new project root). If warm reuse after phase 2 makes recreates rare enough and cheap enough, this phase stays parked. Re-evaluate after one dogfood week.

Design sketch: after the first successful `msbWaitKeeper` on a cold boot, call `SandboxHandle.Snapshot(ctx, "construct-base")`. When a recreate is unavoidable, build the replacement with `WithFromSnapshot("construct-base")` plus FRESH mounts (see spike list). Staleness: invalidate the snapshot when `construct-box` image version, `packages.toml` hash, or entrypoint hash changes; store the invalidation key host-side (roots.json sibling or its own `snapshot.json`).

Spike checklist (must all pass before any wiring):
- [ ] P6.1 Fork boot time vs cold boot (measured, recorded in section 10)
- [ ] P6.2 Disk semantics: does a fork share or copy the base (du before/after several forks)
- [ ] P6.3 Forked sandbox accepts fresh mounts; old `/workspaces` set is not frozen into the fork
- [ ] P6.4 `/home/construct` host bind re-binds cleanly on the fork (marker still matches, no stale guest state)
- [ ] P6.5 Fork labels/config can be set independently of the base

Wiring (only after the gate and spikes):
- [ ] P6.6 Snapshot after first successful cold boot; invalidation key on image + packages + entrypoint hashes
- [ ] P6.7 `EnsureMsbDaemon` recreate path prefers the snapshot fork; falls back to cold create if the snapshot is missing/stale
- [ ] P6.8 `construct sys daemon snapshot refresh` manual command
- [ ] P6.9 Telemetry tags fork-based recreates distinctly (`msb-boot: outcome=recreate-fork`)

### Phase 7: host skills mount (docker + microvm)

Goal: stop asking the user to duplicate their skill library into the sandbox. Today, `~/Dev/EstebanForge/AGENTS/manage.sh link` runs in `--construct-cli` mode and rsyncs the central `skills/` into the persistent sandbox home (`~/.config/construct-cli/home/<agent>/skills/`) before each construct invocation. This is the duplication the user is paying for: a per-machine copy of a host-managed library, kept in sync only when the helper runs. After this phase, the host skills source is bind-mounted into every supported agent's expected skills location inside the sandbox at create time, and the persistent home no longer needs to mirror it.

Scope (cross-backend, both runpaths):
- Docker (`internal/runtime/runtime.go` `GenerateDockerComposeOverride`): one bind per agent, all under the existing `volumes:` block.
- microVM (`internal/runtime/backend_msb_run.go` `conditionalAutoMounts` + `MsbPathMaps`): one `msb.Mount.Bind` per agent, surfaced through the same auto-mount helper used by the qmd models cache.
- No second config knob for the second backend. The skill source is resolved once, the per-agent mount list is built once, each backend consumes the list.

Source resolution (`internal/runtime/skills_mount.go` or a section in `runtime.go`, mirroring `getQmdModelsPath`):
1. `$CONSTRUCT_SKILLS_SOURCE` env var — always wins (CI, ephemeral hosts, multi-machine setups).
2. `[sandbox] skills_source` in `config.toml` — non-empty overrides auto-detect; empty triggers the walk.
3. Auto-detect walks these locations in order, first existing wins:
   - `~/Dev/EstebanForge/AGENTS/skills` (the agent-forge layout this repo ships from)
   - `~/AGENTS/skills`
   - `~/.config/construct-cli/skills` (construct-cli's own canonical location, distinct from the persistent home)
   - `$XDG_DATA_HOME/construct/skills` (XDG default)
4. None found — no mount, no error, log a single line at debug. The feature is opt-out by silent absence, not by failing the run.

Targets (derived from `internal/agent/agent.go` `SupportedAgents` `<ConfigPath>/skills`, NOT hard-coded):
- agy     → `/home/construct/.antigravity/skills`
- claude  → `/home/construct/.claude/skills`
- amp     → `/home/construct/.config/amp/skills`
- qwen    → `/home/construct/.qwen/skills`
- copilot → `/home/construct/.copilot/skills`
- crush   → `/home/construct/.config/crush/skills`
- droid   → `/home/construct/.factory/skills`
- goose   → `/home/construct/.config/goose/skills`
- kilocode→ `/home/construct/.kilocode/skills`
- cline   → `/home/construct/.cline/skills`
- codex, opencode, pi: no skills mount today (matches the user's `manage.sh` mapping; revisit when those agents publish skill formats).

Mount mode: **read-only by default** (`[sandbox] skills_read_only = true`). The host source is the source of truth; agents consume skills, they do not author them. This is the safe default — read-write bind-mounts of host content from inside a sandbox are a known foot-gun, and the agent does not need RW to consume a skill. Users who want agents to author or edit skills opt in with `[sandbox] skills_read_only = false`; the same trust bound as the persistent home bind applies (agent already runs as the host user). The docker compose `:ro` syntax combines with `:z` via the comma variant `:ro,z` on Linux SELinux systems.

Config knobs:
- `[sandbox] mount_skills bool` (default `true`): feature on/off. `false` disables the resolver, the auto-detect side-effects, and the per-agent mount lines.
- `[sandbox] skills_read_only bool` (default `true`): mount mode. `false` opts into read-write so agents can author skills.
- `[sandbox] skills_source string` (default empty): override the auto-detected source path. Env var `$CONSTRUCT_SKILLS_SOURCE` always wins.

Override hash integration (`internal/runtime/runtime.go` `overrideInputs` + `hashOverrideInputs`):
- New field `SkillsSource string` (empty when disabled). Hash line `skillssource:%s`.
- New field `SkillsTargetCount int`. Hash line `skillstargets:%d`.
- New field `SkillsReadOnly bool`. Hash line `skillsreadonly:%v`.
- All three change together with any future per-agent override (none planned) to force regeneration of `docker-compose.override.yml`.

microvm daemon recreate parity (`internal/runtime/backend_msb_run.go`):
- New label `construct.daemon.skills_hash` stamped by `BuildMsbRunSpec` whenever skills mounts are enabled (regardless of whether the source resolves).
- Hash covers `MountSkills` + resolved source path + `SkillsReadOnly` + target count. Returns "" when skills are disabled (no label, no recreate trigger).
- `msbDaemonNeedsRecreate` checks this label FIRST in both multi-path and single-path modes, returning recreate with reason `host skills mounts changed (source, mode, or targets)` on mismatch. Skills toggle, RO/RW flip, source appearance, and supported-agent-list growth all recreate the running daemon so the new mounts take effect.

Docker volumes block (mirror qmd models cache, both `linux` and `darwin` blocks):
```
if skillsSource, found := getSkillsSourcePath(cfg); found {
    for _, target := range skillsMountTargets() {
        fmt.Fprintf(&override, "      - %s:%s%s\n",
            formatVolumePath(skillsSource), target, selinuxSuffix)
    }
}
```
Comma variant (`selinuxCommaSuffix`) is NOT needed: skills mounts have no `:ro` flag and use the standalone `:z` suffix like the qmd cache mount. macOS branch uses the same line without `:z`.

microVM (`msbSandboxMounts` + `MsbPathMaps`):
- Extend `conditionalAutoMounts()` to append one `msbAutoMount{Dest, Src, Readonly:false}` per target.
- `MsbPathMaps` returns the same entries; the host-exec bridge resolves `cwd` against the longest prefix as today.

Failure mode contract: source missing → no mount, no error, no recreate. Source exists but target path is unwritable inside the sandbox → run still starts; agents that need skills see an empty directory (silent degradation). The override hash sees `SkillsSource=""`, regenerates only when the source appears/disappears.

Interaction with `manage.sh`:
- `manage.sh link` in `--construct-cli` mode (per the repo's `manage.sh` comments) is the duplication this phase retires. The user's host repo should drop the `construct_Claude`, `construct_Antigravity`, etc. entries after this lands, OR keep them as a fallback for non-construct run paths. Either is fine; construct-cli no longer requires them.
- Standard symlink mode (`manage.sh link` outside `--construct-cli`) is untouched — it still works on the host for non-construct agents.

- [ ] P7.1 `internal/runtime/skills_mount.go` (or section in `runtime.go`): `getSkillsSourcePath(cfg) (string, bool)` with env > config > auto-detect precedence; `skillsMountTargets() []string` derived from `internal/agent.SupportedAgents`; `skillsMountOptions(cfg, selinuxSuffix)` builds the compose third-field option string (`:ro` by default, `:ro,z` combined on Linux SELinux)
- [ ] P7.2 `[sandbox] mount_skills bool` (default true) + `[sandbox] skills_read_only bool` (default true) + `[sandbox] skills_source string` (default empty) in `SandboxConfig`, `DefaultConfig()`, and the `config.toml` template comment block
- [ ] P7.3 Docker override: `overrideInputs` fields + hash lines (including `SkillsReadOnly`); per-agent mount lines in `linux` AND `darwin` volume blocks using `:ro`/`:ro,z` for read-only, plain suffix for read-write
- [ ] P7.4 microVM: `conditionalAutoMounts` + `MsbPathMaps` extend with the same target list; `msbAutoMount.Readonly` set from `cfg.Sandbox.SkillsReadOnly`
- [ ] P7.5 Tests: `TestGetSkillsSourcePath` (env var precedence, config override, auto-detect, disabled, missing source); `TestHashOverrideInputsIncludesSkillsSource` (incl. readonly flag changes hash); `TestGenerateDockerComposeOverrideMountsSkillsWhenEnabled` (asserts `:ro` suffix on default); `TestGenerateDockerComposeOverrideMountsSkillsRWWhenOptIn` (no `:ro`); `TestMsbSandboxMountsIncludesSkillsMount` (readonly flag flows); `TestSkillsMountOptions` (table-driven for selinux combinations)
- [ ] P7.6 Dogfood: zero skills inside the sandbox persistent home after a fresh `construct build`; edit a skill on the host, observe it inside the sandbox without a rebuild; confirm agents cannot write to the host source by default; opt into RW, confirm writes flow back
- [ ] P7.7 Update `docs/CONFIGURATION.md` with the new keys, the precedence order, and the `manage.sh` retirement note

## 6. Cross-cutting acceptance criteria

- [ ] All new config keys documented in `docs/CONFIGURATION.md` and the `internal/templates/config.toml` comment block in the same PR that introduces them
- [ ] Every phase ships with unit tests; live-msb behavior verified once on macOS dogfooding and noted in section 10
- [ ] No phase may violate section 1 fundamentals; if a phase appears to require it, stop and re-review instead of bending the rule
- [ ] Run-path output stays on stderr; `make check` green before merge on every phase
- [ ] Every feature that touches mounts must (a) update `overrideInputs` + `hashOverrideInputs` for docker, (b) extend `conditionalAutoMounts` for microvm, (c) update `MsbPathMaps` if the host-exec bridge needs to translate the new path. Phase 7 demonstrates the pattern.

## 7. Security review notes (carried from the review rounds)

- A learned root is NOT trust-equivalent to a configured root. The prompt-on-learn is the consent gate; the workspace guard is only a floor. Never auto-learn silently, even interactively-adjacent.
- Idle stop REDUCES attack surface: bridges (SSH proxy, clipboard, host exec, herdr) exist only while at least one session is live.
- The mount set must be inspectable at all times (`construct sys daemon roots`); invisible state is the security objection in miniature.
- The credential proxy only counts if guest-env provider keys are removed (phase 5 constraint).
- Skills mount (phase 7) is **read-only by default** (`:ro` / `msbAutoMount.Readonly = true`); the user opts into read-write via `[sandbox] skills_read_only = false`. The default is the safe choice — a read-only bind cannot mutate the host source even if the agent is hostile, and the agent does not need RW to consume a skill. RW opt-in is documented in `docs/SECURITY.md` when that doc gains a filesystem-exposure section. Mount is per-agent (one bind per `<ConfigPath>/skills`), so even in RW mode a hostile agent cannot escape its own skills directory into another agent's config.

## 8. Where things live (implementer's map)

- Daemon lifecycle and mounts: `internal/runtime/backend_msb_run.go` (`EnsureMsbDaemon`, `msbSandboxMounts`, `MsbPathMaps`, `BuildMsbRunSpec`, `msbDaemonNeedsRecreate`, `msbWaitKeeper`)
- Mount set resolution: `internal/runtime/daemon_mounts.go` (`ResolveDaemonMounts`, `MapDaemonWorkdirFromMounts`, `daemonMountDest`, `normalizeMountPath`)
- Skills mount: `internal/runtime/skills_mount.go` (phase 7) — `GetSkillsSourcePath`, `SkillsMountTargets`, `SkillsMountOptions`, `SkillsDaemonHash`. Docker: `internal/runtime/runtime.go` (`GenerateDockerComposeOverride`, `overrideInputs`, `hashOverrideInputs`). microvm: `internal/runtime/backend_msb_run.go` (`BuildMsbRunSpec` stamps the skills hash label, `conditionalAutoMounts`, `MsbPathMaps`, `msbDaemonNeedsRecreate` checks `construct.daemon.skills_hash` in both multi-path and single-path modes)
- Per-agent config paths: `internal/agent/agent.go` (`SupportedAgents`, `<ConfigPath>/skills` is the mount target)
- Workspace guard: `internal/runtime/workspace_guard.go`
- Engine run path: `internal/agent/engine_msb.go` (`execViaMsbDaemon`), Teardown in `internal/agent/engine.go`
- Daemon CLI surface: `internal/daemon/daemon.go` (start/stop/attach/status) and its command registration
- Image acquisition: `internal/runtime/backend_msb.go` (`EnsureImage`, `DetectBackend`)
- Config: `internal/config/config.go` (RuntimeConfig, DaemonConfig, SandboxConfig), template `internal/templates/config.toml`
- Atomic write precedent: `writeFileAtomic` in `internal/config/config.go`
- Docs: `docs/CONFIGURATION.md`, `docs/ARCHITECTURE-DESIGN.md` section 4.1, this file

## 9. Review provenance

- Round 1 (multi-path port, 2026-08-25): blocker found and fixed (single-path mapper lacked symlink resolution). Shipped in 1.16.2.
- Round 2 (engine/backend merge, 2026-08-25): two blockers found and fixed (migration layer disagreement; deprecated field round-trip through Save). Shipped in 1.16.2.
- Round 3 (architecture, continued reviewer session): verdict "keep the shared daemon, discard per-workspace and snapshot-as-run-model"; elevated flock and credential injection. Adopted.
- Round 4 (this plan, continued reviewer session, higher-tier reviewer): corrections adopted: prompt-on-learn + LRU cap + `daemon roots` command; learned roots are not trust-equivalent; item 1 and item 5 coupling (learn = hash change = recreate = root disk wipe); own timer never `WithIdleTimeout`; flock widened and moved first; TLS-termination constraint for the credential proxy; ordering 0-1-2-3-4-5-6.
- Round 5 (phase 7, host skills mount, 2026-08-26): owner-direct decision (no external reviewer this round). Source resolution precedence fixed (env > config > auto-detect) instead of letting config shadow env. Per-agent targets derived from `SupportedAgents` rather than hard-coded, so adding an agent picks up the mount for free. **Mount mode flipped from RW to RO** during implementation: RW is a known foot-gun when bind-mounting host content into a sandbox, and the agent does not need RW to consume a skill. Users opt into RW via `[sandbox] skills_read_only = false` when they want agents to author skills; trust bound matches the persistent home. Failure mode is silent (source missing → no mount, no error), not an interactive prompt — the feature is opt-out by absence, not by asking.
- Round 6 (peer review follow-up, 2026-08-26): isolated Sonnet review caught a daemon recreate gap — `msbDaemonNeedsRecreate` did not consider skills mounts, so a skills toggle on a running daemon would silently miss the new mounts until manually recreated. Fix: new label `construct.daemon.skills_hash` (hash of source + RO flag + target count), stamped in `BuildMsbRunSpec` whenever skills are enabled, checked first in `msbDaemonNeedsRecreate` for both multi-path and single-path modes. Doc nits accepted: `SkillsTargetCount` comment corrected to "static full count, never 0"; `config.toml` comment now names the real per-agent paths; missing `$CONSTRUCT_SKILLS_SOURCE` missing-path test added.
- Round 7 (peer review follow-up, 2026-08-26, same P3-P5 commit): the requestLearnRoot return-value split Sonnet flagged was traced to dead code — `cleanProjectDir` already filters system roots upstream, so the system-root backstop in `requestLearnRoot` never fires. Reverted the split; added a VMsv2.md note explaining the no-op `ResolveDaemonMountsWithLearned` wrapper and the defensive-backstop status of the workspace guard, so a future maintainer does not "fix" them away.
- Round 8 (peer review follow-up, 2026-08-26, on P3 idle stop + P4 prepull): isolated Sonnet review caught two must-fix items and several nits. Validated against the codebase before fixing.
  - **Critical: idle-watcher lock race.** `StopMsbDaemonBestEffort` did not acquire the daemon flock, so two concurrent watchers could race each other AND a fresh `ct`'s `EnsureMsbDaemon` mid-flight. Fix: acquire `acquireDaemonLock()` before the final recheck + stop; re-check `LiveSessionCount()` under the lock so a session that registered between the watcher's last tick and the stop is honored.
  - **Prepull backend visibility.** Sonnet claimed `auto` could resolve to microvm; verified false — `ResolveContainerRuntime` only picks from `[container, podman, docker]`, never microvm. But the underlying discoverability problem is real: default-config users see `prepull_image = true` yet the feature silently never fires. Fix: log a warning when `prepull_image = true` but `backend != "microvm"`. Gate itself unchanged.
  - **LiveSessionCount error path now logs.** Previously returned 0 on `ActiveSessions` error invisibly.
  - **Style nits:** `containsBytes` reimpl `bytes.Contains` (replaced); `prepullLogWriter` dead `stderr` param (removed); added a comment in `update.go` locking in the install-before-prepull ordering invariant.
  - **Did NOT act on**: Setsid on macOS (manual integration check per plan pattern, not a code defect); `imageLoadedForPrepull` substring match (false negative is harmless); `os.Executable()` after `SelfUpdate` (verified correct order; comment now locks it in).

## 10. Dogfood numbers (fill from phase 0 telemetry)

- Median cold boot (create + installs): TBD
- Median recreate: TBD
- Median warm boot (idle-stopped -> running): TBD
- Median reconnect (already running): TBD
- Recreate frequency per dogfood week: TBD
- Snapshot fork boot time (phase 6 spike): TBD
- Skills mount: source resolved automatically (TBD dogfood); override regenerates within one `construct` invocation when source appears/disappears (TBD); zero skills inside the persistent home after phase 7 ships (TBD)

## 11. Open questions and risks (carry forward, do not silently resolve)

- skills_source resolution: a future change in XDG defaults (XDG Base Directory Specification rev) may require re-ordering the auto-detect list. Document the lookup order in the config comment block so users understand the precedence without reading the code.
- Read-write skills mount is a trust-bound expansion: an agent that authors or edits skills writes directly to the host source. The same trust bound applies to the persistent home today, so this is not a new attack surface, but `manage.sh` users coming from symlink mode should be told their central repo is now RW-reachable from inside the sandbox. Cover in `docs/SECURITY.md` if the doc grows a "filesystem exposure" section.
- microvm stat virtualization: skills mounts use `StatVirtualizationOff` (same as the persistent home) to keep the agent's `ls` output honest. Re-evaluate when msb SDK exposes a per-mount option that distinguishes bind behavior.
- `internal/agent.SupportedAgents` currently lists 13 agents. If a 14th agent publishes a skill format, the targets list regenerates automatically. The "codex/opencode/pi excluded" decision is captured as a comment in `skillsMountTargets` so future maintainers see the rationale.

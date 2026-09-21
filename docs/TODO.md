# TODO: Move Claude from packages.go to packages.toml [OBSOLETE 2026-09-21 — superseded by Image Layering below: Claude is BAKED into the image at `/usr/local/bin`; it must be REMOVED from `packages.go`, NOT moved to `packages.toml`. A packages.toml entry would shadow the baked binary with a bind copy.]

## Background

Claude Code is currently hard-coded in `internal/config/packages.go` as a "Standard Tool (Always installed)" alongside Bun, Amp, imagemagick, topgrade, and cargo-update.

Other agents (agy, copilot, codex, pi, etc.) are installed via `packages.toml` (npm/brew sections).

## Current State

```go
// internal/config/packages.go:201-207
// Standard Tools (Always installed)
script += "echo 'Installing Claude Code...'\n"
script += "if [ -x \"/home/construct/.local/bin/claude\" ]; then\n"
script += "    echo \"Claude already installed; skipping.\"\n"
script += "else\n"
script += "    curl -fsSL https://claude.ai/install.sh | bash\n"
script += "fi\n\n"
```

## Proposed Change

Move to `[post_install].commands` in `internal/templates/packages.toml`:

```toml
[post_install]
commands = [
    "agent-browser install --with-deps",
    "if [ -x \"$HOME/.local/bin/droid\" ]; then echo \"Droid already installed\"; else curl -fsSL https://app.factory.ai/cli | sh; fi",
    "if [ -x \"$HOME/.opencode/bin/opencode\" ]; then echo \"OpenCode already installed\"; else curl -fsSL https://opencode.ai/install | bash; fi",
    "if [ -x \"$HOME/.local/bin/claude\" ]; then echo \"Claude already installed\"; else curl -fsSL https://claude.ai/install.sh | bash; fi",
]
```

Remove hard-coded install from `packages.go`.

## Impact Analysis

| Question | Answer |
|----------|--------|
| **New user setup break?** | ❌ NO — same install script |
| **Upgrades break?** | ❌ NO — idempotency check prevents re-install |
| **PATH issues?** | ❌ NO — `$HOME/.local/bin` already in PATH |
| **Verification loop?** | ⚠️ Already checks for `claude` command (line 401) |

## Files to Modify

1. `internal/config/packages.go` — remove lines 201-207
2. `internal/templates/packages.toml` — add to `[post_install].commands`

## Open Questions

- Is Claude a "first-class citizen" that should always be installed?
- Or is it like other agents (user-configurable)?
- Product decision needed.

## Paths Reference

| What | Path |
|------|------|
| Claude binary | `/home/construct/.local/bin/claude` |
| Claude config | `/home/construct/.claude` |
| PATH component | `$HOME/.local/bin` ✓ |
| Volume mount | `~/.config/construct-cli/home:/home/construct` ✓ |

## Notes

- No technical dependency on Claude being installed early
- Install order: Standard Tools → packages.toml (difference: Claude would run slightly later)
- Same official install script: `curl -fsSL https://claude.ai/install.sh | bash`

---

# TODO: MicroVM Engine General Availability (GA) Checklist

Items required before graduating `backend = "microvm"` out of experimental status.

## Prerequisites

- [ ] **Publish Multi-Arch Image to GHCR**: Build and publish `ghcr.io/estebanforge/construct-box:latest` and tagged versions for `linux/amd64` and `linux/arm64` via the release workflow (`.github/workflows/release.yml`). Users running the microVM backend must pull this image directly via `msb pull` without requiring a local Docker engine.
- [ ] **Verify Non-Docker Cold Start**: Validate that fresh machines with `backend = "microvm"` and `msb` installed can run `construct sys init` and agent sessions with zero Docker dependencies installed.
- [ ] **Dogfooding & Stability**: Complete dogfooding across daily workloads (Claude, Pi, Codex, Antigravity) validating bridges (SSH agent, clipboard, host exec, loopback forwarders) and project directory transitions.
- [ ] **Documentation Update**: Remove experimental warnings in `README.md`, `INSTALLATION.md`, `CONFIGURATION.md`, and `ARCHITECTURE-DESIGN.md`.

---

# TODO: Bidirectional Synchronized Workspace for MicroVM (High-Performance Monorepos)

## Background

Virtiofs passthrough over Apple Virtualization Framework carries high metadata latency per file operation (`stat`, `readdir`, `open`). In large monorepos with hundreds of thousands of files or heavy dependency trees, recursive scans by agents (`find`, `rg`, indexing) can saturate the virtualization queue.

Docker Sandboxes solves this problem for monorepos through **Synchronized File Shares** (Mutagen-based background caching), keeping the guest workspace on a native ext4 disk while synchronizing file updates to the host.

## Goal

Implement an opt-in `sync_mode = "bidirectional"` for the Construct microVM backend to give agents native Linux ext4 filesystem performance while keeping host files updated in real time.

## Proposed Architecture

```
Host Project Directory (APFS / ext4)
         │  ▲
         │  │  Bi-directional Sync (Mutagen / Vsock Daemon)
         ▼  │
MicroVM Guest `/workspace` (Native ext4 Virtual Disk)
         ▲
         │  Full Native I/O Speed
AI Agent (Pi, Claude, Codex, Antigravity)
```

## Implementation Plan

1. **Configuration Knob**:
   - Add `[sandbox] sync_mode = "virtiofs"` (default) or `"bidirectional"` to `internal/config/config.go` and `internal/templates/config.toml`.
2. **Guest Storage Layout**:
   - When `sync_mode = "bidirectional"`, omit the host bind mount for `/workspace` in `msbSandboxMounts`.
   - Allocate `/workspace` directly on the microVM's virtual disk.
3. **Synchronization Transport**:
   - Establish a bi-directional file synchronization bridge between the host CLI process and the guest environment over vsock or guest SSH bridge.
   - Use an embedded synchronization agent (such as Mutagen) or a lightweight bidirectional rsync/inotify watcher.
4. **Lifecycle & Synchronization Stages**:
   - **Pre-flight**: Perform an initial seed synchronization from host to guest `/workspace` before starting the agent session.
   - **Runtime**: Stream incremental file modifications bi-directionally during the agent session.
   - **Teardown**: Perform a final sync flush from guest to host before detaching or shutting down.
5. **Ignore & Pruning Rules**:
   - Automatically exclude heavy cache directories and binary artifacts (`node_modules/`, `.git/objects/`, target builds) from bi-directional host sync to minimize synchronization overhead.

## Success Criteria

- Recursive file operations (`find /workspace`, `rg`, `git status`) inside the microVM execute at native ext4 speed with zero Virtiofs queue lag.
- Files created or edited by the agent inside `/workspace` appear immediately on the host filesystem.
- Zero risk of microVM freeze when running agents across large monorepos.

---

# TODO: smolvm Backend Spike (post-GA)

Status: parked. Do not start before the MicroVM Engine GA checklist above ships.

## Background

Alternatives review, 2026-08-27. microsandbox (msb) stays the GA engine: it is the only local-first microVM option that satisfies all four construct constraints (macOS arm64 + Linux KVM, embeddable Go SDK, local-first, single shared daemon).

The watch item is smol machines / smolvm ([smolmachines.com](https://smolmachines.com), [github.com/smol-machines/smolvm](https://github.com/smol-machines/smolvm), Apache-2.0, Rust). Feature superset relevant to us: VM fork with copy-on-write memory (would supersede the parked VMsv2 phase 6 snapshot fork), checkpoints/restore, portable stateful `.smolmachine` artifacts, first-class egress allowlist, networking off by default, OCI pulls with no Docker daemon, CUDA-over-vsock GPU remoting.

Gap: no Go SDK (Rust crate + Node/Python SDK; REST via `smolvm serve`). Integration paths: shell out to the `smolvm` CLI, thin REST client, or cgo bindings to the Rust crate. Any of these is a new backend implementation, not a rewrite: the `Backend` interface and conformance harness exist for exactly this.

## Spike scope

- [ ] Evaluate `smolvm` CLI/REST stability, mount semantics (vs the msb create-time-only mounts constraint), and daemon/lifecycle model on macOS arm64 + Linux KVM.
- [ ] Prototype a smolvm `Backend` implementation (`internal/runtime/backend.go`) passing the existing conformance suite (`internal/runtime/conformance_test.go`) with no engine-specific forks of the harness.
- [ ] Measure time-to-ready vs the msb warm/reconnect baselines in `docs/VMsv2.md` section 10 (requires P0.4 dogfood medians to exist first).
- [ ] Confirm the egress allowlist + secret handling cover what `msbHostTransportRules` + network modes (permissive/strict/offline) do today, including the host loopback relays.

## Decision gate

Switch (or add as a second microvm backend) only if BOTH hold: msb SDK velocity keeps producing breakage we must absorb (SDK pin lockstep, missing mount APIs, incomplete-rootfs fallback) while smolvm ships stable, AND the conformance prototype passes. Otherwise keep msb and leave VMsv2 phase 6 parked under its own gate: if the spike passes, smolvm fork replaces phase 6; if not, the phase 6 gate still applies. Re-evaluate after GA ships + one dogfood week.

---

# TODO: Image Layering — Bake Dev Baseline + Core Agents (prerequisite for the first GHCR publish)

Status: DECIDED (owner, 2026-09-21). The first `image.yml` publish carries this; a base-only image will not be published. Image size is accepted (~3-4 GiB) — users receive a complete, secure work environment. Sequencing note: this lands BEFORE the GA checklist's image-publish item; `scripts/lab-matrix.sh` re-runs against the baked image before dispatch.

## Decision

Move EVERYTHING dev-oriented from `packages.toml` into the image, and pre-install the five core agents users are certain to use. Both engines consume the same Dockerfile, so one bake serves Docker (local build, auto-rebuilt on template-hash change) and microVM (GHCR pull).

**Baked into the image (build time):**

- All `[apt]`, `[brew]`, `[cargo]`, `[pip]`, `[gems]`, `[tools]` dev packages — the full baseline
- Core agents, installed via their official installers at `/usr/local/bin` (root-owned, NOT the bind path): **claude, codex, agy, pi, opencode**

**Stays in `packages.toml` (optional layer, runs at guest init, installs to the home bind):**

- Remaining agents: amp, copilot, crush, goose, kilocode, qwen, cline (+ any future additions)
- User-defined packages and `[post_install]` commands

## The override contract (bind vs baked)

Actual PATH precedence (verified `internal/env/env.go` `PathComponents`):

1. `/home/linuxbrew/.linuxbrew/bin` — FIRST; brew-tier tools shadow EVERYTHING (bind AND baked)
2. `/home/construct/.local/bin`, `/home/construct/.npm-global/bin` — the bind tier (overrides for npm/curl-installed agents)
3. `/usr/local/bin` — the baked baseline (loses to both tiers above)

Layer semantics that qualify the contract:

- `[apt]` and `[brew]` user entries install to the guest ROOT disk, not the bind — a microvm recreate wipes them (compose keeps them until container recreate). This is existing behavior and must be documented, not changed by the bake.
- `gem install` as the construct user fails on system paths; user-space gems land in `$HOME/.gem/ruby/.../bin`, which is ABSENT from `PathComponents`. Fix during implementation: `gem install --bindir /home/construct/.local/bin` (or add the gem bin dir to PATH in all 4 sync files).
- The installer must NEVER write to `/usr/local/bin` at runtime — that is what keeps the two layers from corrupting each other.

- `packages.toml` entry PRESENT → user-layer install on the bind → **shadows the baked baseline** → user controls the version (pin old, try new)
- Entry ABSENT → baked baseline serves; nothing reinstalls it behind the user's back
- Entry REMOVED → the bind copy is NOT auto-deleted (the installer never sweeps user-layer installs — users keep their own manual tools there); documented one-liner: delete the bind copy to fall back to baked
- The installer must NEVER write to `/usr/local/bin` at runtime — that is what keeps the two layers from corrupting each other

## Existing-user migration (the shadow trap)

Existing home binds carry agent copies installed under the old model — those will shadow the new baked binaries forever. Where they actually live (verified):

- `/home/construct/.local/bin` — claude, amp (curl-installed)
- `/home/construct/.npm-global/bin` + `/home/construct/.npm-global/lib/node_modules/` — codex, pi (npm)
- `/home/construct/.opencode/bin` — opencode

`packages.toml` is NOT mounted into the container (only the generated `install_user_packages.sh` is), so the migration sweep CANNOT live in `entrypoint.sh`. Generate it in Go inside `GenerateInstallScript()` (`internal/config/packages.go`), which has the parsed config: emit explicit deletion commands for each baked tool ABSENT from `packages.toml`, targeting all three path families above (binary + npm module dir), marker-gated to run once, printing what it removed. A packages.toml entry is a deliberate override and must survive.

## Build requirements

- STRIP the baked set from `GenerateInstallScript()` — it currently hardcodes `apt-get update && apt-get install`, the Bun installer, brew `imagemagick`/`topgrade`/`libgit2`, and `cargo-update` (`internal/config/packages.go` ~176-256). Left in, every hash-change boot re-runs network installs and the <5 s goal dies. Post-bake, the installer emits ONLY packages.toml-defined items.
- REMOVE the `construct-packages` named volume (`internal/templates/docker-compose.yml:27,62` + `internal/runtime/runtime.go:1256,1288`): Docker initializes named volumes from image content ONLY at first creation — an existing volume would shadow the baked Homebrew baseline with stale files forever, and the microvm backend already dropped it (no copy-up in msb). Homebrew moves to the root filesystem on both engines.
  - COMPANION FIX (required, same PR): relocate the installer hash marker out of the home bind. Today `HASH_FILE=/home/construct/.local/.entrypoint_hash` (`entrypoint.sh:423`) lives on the bind, so a recreate with unchanged hash skips the installer entirely — with the volume gone, declared `[brew]`/`[apt]` packages would then be lost on every recreate. Move the marker to the root filesystem (e.g. `/var/lib/construct-cli/.entrypoint_hash`): it then resets per root-disk lifetime, so the installer re-runs exactly once after each recreate. Post-bake that re-run is a near-no-op (existence guards skip everything installed) — seconds, and it re-provisions user-declared packages. Routine stop/start still skips (root persists).

  Migration note for existing Docker users (release notes + `docs/CONFIGURATION.md`; AUTOMATED by `construct sys doctor --fix` — see the Sys Doctor section — the steps below are the manual fallback):

  ```text
  Docker users: one-time cleanup after upgrading

  Construct no longer mounts the construct-packages volume. Homebrew now
  ships inside the image, identical on the Docker and microVM engines.

  1. (Recommended, before upgrading) If you installed Homebrew packages
     manually inside the container, list them:

         docker compose exec construct brew list

     Add anything you need to packages.toml [brew]. Declared packages
     re-provision automatically after every container recreate.

  2. Upgrade construct and run it once; the container rebuilds with the
     baked image.

  3. Delete the orphaned volume (find the exact name first):

         docker volume ls | grep construct-packages
         docker volume rm <project>_construct-packages

     Skipping this is harmless — the volume is unused — but it keeps
     consuming disk.

  4. Verify: docker compose exec construct brew --version

  Note: packages installed manually inside the container (not declared in
  packages.toml) no longer survive a container recreate. This matches the
  microVM engine, where the root disk resets on recreate. Declare what you
  need; it comes back on its own.
  ```
- REMOVE the baked-agent update commands from `GenerateTopgradeConfig()` (`claude update`, `pi update --all`, `agy update`) and their fallback loops in `update-all.sh`: as the construct user they either fail with EACCES on root-owned `/usr/local/bin` or silently redirect to `$HOME/.local/bin`, creating a rogue shadowing bind copy.
- BuildKit cache mounts for `apt`, `npm`, and Homebrew caches (CI + docker-backend local build speed)
- Layer order by churn: OS/apt → brew core → static binaries → language runtimes → core agents LAST (pull progress + cache reuse)
- No recursive `chown` layers (`COPY --chown`)
- zstd layer compression — BLOCKED on verifying the msb 0.7.2 puller handles zstd layers before switching from gzip; also surface `msb pull` errors instead of falling through to a masked `docker save` fallback (`EnsureImage` currently swallows them)
- Core agents update on the image cadence (accepted); `packages.toml` override is the user escape hatch for newer/older versions

## Instrumentation prerequisite

`entrypoint.sh` emits `entrypoint_install_phase_sec` (via the guest log the host already collects) so install-phase time is separable from boot time. Success criteria: install phase drops from >300 s to <5 s on hash-change boots; image pull grows by <60 s.

## Verification gate (before dispatching `image.yml`)

1. `scripts/lab-matrix.sh` green against the baked image (isolated HOME, 0.7.2 pair) — EXTENDED with real assertions, not just `/bin/uname -sm`: each baked agent resolves to `/usr/local/bin/<name>`; a packages.toml codex entry shadows it; the migration sweep clears stale pre-bake copies from all three path families; `entrypoint_install_phase_sec < 5`; stale `construct-packages` volume absent from the compose output
2. Fresh-home first run: agents usable without any install phase; `claude --version`, `codex --version`, `agy --version`, `pi --version`, `opencode --version` resolve to `/usr/local/bin`
3. Override contract live: a `packages.toml` codex entry shadows the baked binary; removing the entry + recreating falls back to baked
4. Migration sweep verified against a home bind containing stale pre-bake copies
5. Image size + pull time recorded in `docs/VMsv2.md` section 10

## Post-publish follow-ups

- msb store garbage collection (keep `latest` + active images) — probe leftovers already demonstrated store growth
- Refresh mechanism for cached images on existing installs (digest-check in `EnsureImage` or `construct sys image refresh`) — today microvm users only pull on first create; without this they never receive baked-baseline updates
- Unify `construct sys update` across engines (currently compose-only, fails closed on microvm)

## Peer review round (Agy, 2026-09-21)

Verdict: adopt-with-changes. 3 blockers, 4 majors, 3 minors, 1 note — all folded into this section. Blockers: (1) idle-updater flock/session races (fixed in the updater section below); (2) `construct-packages` named volume shadowing the baked Homebrew baseline (docker named volumes never refresh from image after first creation); (3) migration sweep unimplementable in `entrypoint.sh` (packages.toml never mounted) and targeting the wrong paths. Majors: topgrade commands would EACCES/rogue-shadow baked agents; `GenerateInstallScript` still runs network installs post-bake; brew PATH precedence inverts the stated shadow contract; design text implied running topgrade twice. Minors: TODO §1 contradicted the bake (marked obsolete); lab-matrix.sh had no baked-image assertions; no image-refresh command for microvm users. Note: `EnsureImage` masks `msb pull` failures behind the docker-save fallback.

---

# TODO: Sys Doctor — Automated Migration (`construct sys doctor [--fix]`)

Status: IMPLEMENTED (2026-09-21). Correction to the original design: `construct sys doctor` already existed (health checks + --fix); task 22 EXTENDED it with the migration checks rather than building from scratch. Check 7 (old hash-marker orphan) is SUPERSEDED: task 20 repurposed the bind-side `.entrypoint_hash` as the host mirror, so no orphan exists. Shipped checks: Stale Packages Volume (docker/podman, unreferenced-only rm), Baked Agent Bind Copies (host-side detection via config.StaleBakedCopyPaths, --fix removes host-side), Baked Image Freshness (msb, --fix = bounded 10m msb pull), Host CLI/SDK Skew (msb --version vs constants.MsbSdkPin, report-only). Plus --json (machine-readable, header suppressed) and exit 1 on errors for CI gating. Sequencing rule stands: the volume removal must not ship before the baked image is on GHCR.

## CLI shape

```text
construct sys doctor           # read-only diagnosis: report + exit 0 clean / 1 issues found
construct sys doctor --fix     # diagnosis + apply the enumerated safe fixes, then re-report
construct sys doctor --json    # machine-readable report (lab-matrix assertions, CI)
```

## Checks and fixes

| # | Check (read-only) | Detects | `--fix` action | Engine |
|---|---|---|---|---|
| 1 | Stale package volume | `*_construct-packages` docker volume exists and is referenced by NO container of the current project | `docker volume rm` (only when unreferenced; skip with a warning if a container still mounts it) | docker |
| 2 | Compose layout currency | running container's compose config hash ≠ generated config | trigger the existing template-hash recreate path | docker |
| 3 | Baked agents resolve | exec `command -v <agent>` for claude/codex/agy/pi/opencode → expected `/usr/local/bin/<agent>` | report only (resolution is fixed by the image lane or check 4) | both |
| 4 | Guest sweep pending | bind copies of baked agents present in the three path families (`.local/bin`, `.npm-global/bin` + `lib/node_modules`, `.opencode/bin`) for tools NOT in `packages.toml` | run the generated installer once in the guest (it carries the Go-generated migration sweep; existence guards make this safe to repeat) | both |
| 5 | Image freshness | local msb image digest ≠ GHCR `latest` digest | `msb pull` (bounded, prepull-style, best-effort) | msb |
| 6 | Host CLI/SDK skew | `msbHostVersion()` ≠ SDK pin | report + doc link; NEVER auto-updates the host binary (schema lockstep is a manual decision) | msb |
| 7 | Old hash-marker orphan | guest `~/.local/.entrypoint_hash` present while the relocated root-disk marker is authoritative | delete the orphan file | both |
| 8 | Config template drift | user config missing newly introduced keys (`daemon.auto_update_packages`, …) | report the exact lines to add; never rewrite user config content | both |

## Safety rails

- Without `--fix`: pure read-only. No exec with side effects, no deletes, no pulls.
- `--fix` is idempotent (safe to re-run) and prints every mutation as it happens; one wide-event telemetry line per run (`doctor outcome= actions=N skipped=N`).
- Destructive surface is CLOSED: the only deletions are check 1 (unreferenced volume) and check 7 (orphan marker file). Everything else is report-only or additive (pull, recreate via the existing gate).
- Session-safety: read-only checks may run during live sessions; `--fix` actions that touch daemon state (recreate, pull) follow the same flock/LiveSessionCount rules as the idle updater — stand down if sessions are live.
- Engine-aware: checks scoped to the active backend; unknown/missing engine components are `skipped`, never errors.

## Implementation notes

- New `internal/doctor` package; reuses `msbHostVersion` (telemetry helper), the daemon flock, and the generated installer for check 4. Each check = one function returning (status, detail, fix-fn); `--fix` runs the fix-fns of failed checks.
- `lab-matrix.sh` gains a doctor stage: seed a stale-layout fixture (old volume name + stale bind copies) → `doctor --fix` → assert report clean and volume gone. This becomes part of the Image Layering verification gate.
- Docs: `docs/CONFIGURATION.md` gains a "Sys Doctor" section (check table + `--fix` semantics); release notes for the layering release lead with `construct sys doctor --fix` as THE upgrade step.

---

# TODO: Idle-Window Package Updater (silent, background)

Status: DESIGNED (2026-09-21), sequenced AFTER the Image Layering bake (the updater manages the optional layer; the baked baseline is image-cadence and must be excluded from it). Peer review of the mechanism: Agy round on the image-layering proposal endorsed the idle-window approach over scheduled timers (a fixed daily schedule would boot a stopped daemon, defeating `idle_stop_minutes`, and burn bandwidth on metered links); the follow-up adversarial review (2026-09-21) redesigned the locking model — see Mechanism and the review note at the end of this section.

## Decision

Package updates for the sandbox run inside the daemon's idle window — the dead time between the last session ending and the idle watcher stopping the VM. Default on: `daemon.auto_update_packages = true` (opt out for metered or air-gapped machines). No cron, no guest systemd, no host timer: the daemon's own lifecycle is the scheduler.

## Mechanism

1. Idle watcher arms after the last session unregisters (existing behavior).
2. NEW: when the watcher's wait elapses with zero sessions, it runs the updater BEFORE stopping the daemon. The single entry point is `update-all.sh` — it already invokes the generated topgrade config internally, so the updater does NOT run topgrade a second time. Bounded by a 30-minute hard timeout; output appended to `logs/update.log` + one telemetry line (`update outcome=ok|failed duration=N`).
3. LOCKING MODEL (redesigned per review — the blocker):
   - The update pass runs WITHOUT holding the daemon flock — holding it would block `EnsureMsbDaemon` for up to 30 minutes on any incoming session (the 250 ms wait-warning path becomes a hang).
   - A dedicated update state (atomic flag file or in-process guard) makes the pass idempotent-safe and prevents two concurrent passes.
   - The updater polls `LiveSessionCount()` during the pass; on `count > 0` it stands down (terminate the pass or let the current step finish, then exit WITHOUT stopping the daemon — the idle watcher re-arms normally).
   - The daemon stop itself re-acquires `acquireDaemonLock()`, re-checks `LiveSessionCount() == 0` under the lock, and only then stops. This is the `StopMsbDaemonBestEffort` reference pattern.
4. Failures are best-effort: logged, retried at the next idle window, never surfaced as run errors.

## Update scope

| Layer | Updated in-guest? | Why |
|---|---|---|
| Bind layer (optional agents from `packages.toml`, user installs) | YES — `update-all.sh` | Home bind persists; updates survive everything |
| Root-disk user additions (apt/brew the user added) | YES — generated topgrade config | Survives stop/start; a recreate resets to the baked floor (self-healing, deterministic) |
| Baked baseline (dev packages + core agents at `/usr/local/bin`) | NEVER in-guest | A recreate reverts to the image anyway — in-guest updates would be pure churn. Baked tools move on the image lane (GHCR push) |

The topgrade pass must EXCLUDE the baked set (generated config carries the exclusion list) or it burns bandwidth diverging from the image.

## Safety rails

- 30-minute hard timeout on the whole update pass
- Best-effort: failures logged + retried next window; never block or break an agent run
- Zero-session precondition: the pass starts only at zero sessions and STANDS DOWN if a session appears mid-pass (a recreate during an active update would destroy the sandbox out from under the installer); the stop decision is re-evaluated under the flock
- Flag: `daemon.auto_update_packages = true` default; `false` disables entirely
- Known caveat: some agent CLIs self-update (Claude Code by default) — if double-update loops appear, exclude self-updating CLIs from the topgrade pass and rely on their built-in updaters

## Implementation checklist

- [ ] `daemon.auto_update_packages` (default true) in `DaemonConfig` + `DefaultConfig` + config template comment + `docs/CONFIGURATION.md` (Daemon Settings)
- [ ] Hook: idle-watch stop path runs the updater before `RequestStop` when the flag is on and sessions are zero
- [ ] Bounded runner: 30-minute timeout, output to `logs/update.log`, one telemetry line per pass (`update outcome= duration=`)
- [ ] Baked-set exclusion list in the generated topgrade config
- [ ] `docs/CONFIGURATION.md`: Idle Stop section gains the updater paragraph (ordering: update, then stop)
- [ ] Verification: idle window with flag on → updates run + daemon stops; flag off → no updates; session arrival mid-update safe; bind updates survive stop/start; recreate resets root-disk updates to baked (documented)

## Rejected alternatives (for provenance)

- Guest-side cron/systemd timer: the microvm guest runs the entrypoint as its init workload — no scheduler exists to lean on
- Host systemd timer calling a construct update command: boots a stopped daemon just to update — defeats idle-stop, wakes machines at a fixed hour
- Update-on-boot staleness check: additive backstop option for always-on machines; not the primary mechanism

## Peer review round (Agy, 2026-09-21)

Found the blocker this section now encodes: running the update inside the flock hangs incoming sessions for the timeout; running it without coordination lets a concurrent recreate destroy the sandbox mid-update, and the original "stop afterwards regardless" step could kill a session that registered mid-pass. Fixes adopted: no-flock execution with a dedicated update guard, `LiveSessionCount()` polling with stand-down, stop re-decided under the flock. Also adopted: single entry point `update-all.sh` (topgrade already inside — no double run).

# VM Backend Plan (microsandbox) — rev 2

Status: planning document, revised after peer review against the codebase. Target: opt-in second isolation backend using [microsandbox](https://github.com/microsandbox/microsandbox) (msb) microVMs, alongside the existing Docker/Podman/container backend.

Rev 2 changes from rev 1: reordered (spikes before refactor), interface sized to the real primitive surface, exit codes and RunOptions gaps fixed, guest→host transport promoted to project gate, `engine` fail-open corrected to fail-closed `backend` key, dynamic network updates corrected (they survive), ghcr section corrected (agents are not in the image), effort estimates raised to realistic.

## 1. Why

Construct-cli today isolates agents in an OCI container. A container escape lands on the host kernel. A microVM (libkrun: KVM on Linux, Hypervisor.framework on macOS, WHP on Windows) puts the agent behind a separate guest kernel. Escape requires a hypervisor bug plus a kernel exploit. Materially stronger boundary for the same workflow.

Threat model context: Docker Sandboxes (sbx) already ships microVM isolation. This backend closes that gap without giving up multi-runtime support, the agent fleet, or the bridges.

Docker stays the default backend. The VM backend is opt-in via config. Users who want hardware isolation install microsandbox; everyone else changes nothing.

## 2. Why microsandbox over smolvm

| Requirement | microsandbox | smolvm |
|---|---|---|
| Single-file mounts (gitignore, qmd models auto-mounts) | `--mount-file` with `ro,noexec,nodev` flags | directories only |
| Strict network mode | deny-by-default egress policy, host-side DNS, rebinding protection | allowlist resolved once at boot, static |
| Go integration | native `sdk/go` | shell out to CLI |
| Persistent volumes | named volumes, disk-backed, shareable, host-accessible | overlay disks |
| SSH agent forwarding | host-sockets + ssh support | vsock bridge |
| macOS | Apple Silicon only | Intel + Apple Silicon |
| Windows | WHP | WHP, partial |
| License | Apache-2.0 | Apache-2.0 |

smolvm's differentiators (`.smolmachine` packing, GPU) are irrelevant here. Known gap: msb does not run on Intel Macs; those users stay on Docker. The Backend interface leaves room for a smolvm backend later if Intel-mac demand appears.

## 3. The real gate: guest→host transport

Five bridges depend on the guest reaching host services at `host.docker.internal` today:

1. Clipboard bridge (text + images) — `engine.go:130`
2. Host exec bridge — `engine.go:149`
3. SSH agent bridge
4. Herdr bridge
5. Host loopback relays (browser agents → host dev sites)

All five rest on ONE unsolved problem: a guest→host TCP transport that works under msb and survives msb's deny-by-default egress policy. If clipboard/hostexec cannot reach the host, the backend is not worth building — these bridges are the product's differentiators.

**This, not image boot, is the project gate.** Spike B (section 7) answers it before any refactor. Candidate transports to test: msb `host` network profile, host-sockets, a published-port host listener, or a dedicated host relay process. Whichever wins must be enforceable per-backend and support the existing token auth.

**Gate result (2026-08-18): PASSED.** See section 3.1.

### 3.1 Spike B results (2026-08-18, macOS aarch64, HVF, msb v0.6.10)

**Winning transport: the DNS alias `host.microsandbox.internal`.** Resolves to the host from inside any sandbox whose policy allows the `host` group. It replaces `host.docker.internal` in all five bridges.

Verified:

- Host HTTP server bound to `127.0.0.1` only is reachable from the guest — no LAN bind, no firewall changes needed.
- Under `--net "public,host"`: alias resolves, host service reachable.
- Under deny-by-default (`--no-net --net-rule "allow@host:tcp:18080,allow@dns"`): host service still reachable, public egress blocked (example.com fails). The transport survives the policy rather than bypassing it.
- Token auth works: `Authorization: Bearer <token>` header passes through the msb gateway unchanged (200 with token, 401 without). Clipboard bridge token auth is viable as-is.

Dead ends tested: `host.docker.internal` (no such name in msb), the gateway IP (refused; gateway proxies DNS/HTTP but is not the host), raw host LAN IP (refused). Note "refused" here means the msb network stack rejecting private/host-destination traffic under default policy — not the macOS firewall.

Implication for section 6: bridge endpoints change from `host.docker.internal` to a per-backend host alias; loopback relays keep working since they only need guest→host TCP to the bridge listener.

## 4. Backend interface

### 4.1 Primitive inventory first

11 non-test files import `internal/runtime`. Call sites use roughly 30 distinct primitives: `GetContainerState`, `IsContainerStale`, `GetContainerLabel`, `GetContainerWorkingDir`, `GetContainerMountSource`, `ListContainersByPrefix`, `ExecInteractiveAsUser`, `ExecNonInteractiveStream`, `ExecInContainerWithEnv`, `StopContainer`, `CleanupExitedContainer`, `CwdContainerName`, `Prepare`, `BuildImage`, `GetCheckImageCommand`, volume `rm`, `docker attach`, and more.

The interface must be designed against that inventory, not against "launch" alone. Conformance test suite (section 7, step 3) defines the contract; both backends must pass it.

### 4.2 Interface sketch (to be finalized after inventory)

```go
// Backend launches and manages the construct isolation environment.
type Backend interface {
    Name() string
    Available() (bool, error)

    // EnsureImage guarantees the construct image exists locally (build, pull, or load).
    EnsureImage(ctx context.Context) error

    // Run executes a one-shot session. Returns the workload exit code.
    Run(ctx context.Context, opts RunOptions) (int, error)

    // StartPersistent boots the long-lived environment (daemon replacement).
    StartPersistent(ctx context.Context, opts RunOptions) (PersistentHandle, error)
}

type RunOptions struct {
    ProjectDir     string   // security session root (sec.ProjectRoot()), not raw cwd — hide_secrets overlay
    WorkDir        string   // MapDaemonWorkdir result
    Agent          string
    ContainerName  string   // CwdContainerName-derived
    User           string   // ResolveExecUser result
    Labels         []string // daemon mount labels
    NetworkMode    string   // "permissive" | "strict" | "offline"
    AllowHosts     []string
    Env            []string // ordered; masking mutates in place (engine.go:623)
    Mounts         []Mount
    LoopbackPorts  []int
    PublishPorts   []PortMap // login callback: 127.0.0.1:%d:%d (engine.go:684)
    EntrypointOver string    // update-all.sh (sys/ops.go:89), install bash, sha256sum verify (runner.go:251)
    Stdio          StdioHandles
    TTY            bool
}

type PersistentHandle interface {
    Exec(ctx context.Context, opts ExecOptions) (int, error)
    Attach(ctx context.Context) error // daemon attach path
    Inspect() (State, error)          // WorkingDir, Mounts, labels
    Stop(ctx context.Context) error
}
```

Exit codes are mandatory: every path today returns them (`engine.go:451`, `:640`, `sys/exec.go:45`), including the 126/127 PATH hint at `engine.go:452`.

### 4.3 Config: fail closed, separate key

Do NOT reuse `[runtime] engine`. `DetectRuntime(preferredEngine)` (`runtime.go:46`) prepends and falls through on `LookPath` failure — a user asking for microVM isolation would silently get a Docker container. That is a security bug in this feature's context.

New key:

```toml
[runtime]
backend = "docker"   # default; "msb" = microsandbox (fails closed if not installed)
```

`backend = "msb"` + msb missing = hard error with install instructions. No fallback.

## 5. Image and agent installation strategy

Correction from rev 1: agents are NOT in the image. `install_user_packages.sh` installs them at first run into the `construct-packages` brew volume; `AreAgentsInstalled` (`internal/runtime/runtime.go:404`) checks `~/.config/construct-cli/home/.local/bin`.

Consequences:

1. **msb `load` transition path works** (msb loads Docker archives), and the first-run agent install (5-10 min, non-TTY, log capture via `InstallAgentsAfterBuild`, `runtime.go:445`) must work inside the VM. This is part of the MVP, not polish.
2. **ghcr publish** is still worth doing for the base image, but it removes only the image-build dependency, not the agent-install step. The "Docker-free install" story is Phase 3+ and requires the install flow verified end-to-end on msb. Rev 1's "CI plumbing, not code" claim was wrong.

Guest notes: real kernel, so the entrypoint root block (socat setcap, SSH bridge) runs unmodified. In-guest network filtering works (section 6). UID mapping (`CONSTRUCT_HOST_UID/GID`, userns-remap, SELinux `:z`) is Docker-only dead weight in this path. The `USER construct` question (commented out, `Dockerfile:107`) must be decided explicitly for the guest: gosu drop vs. running as the mapped user.

## 6. Feature translation map (corrected)

| Today (Docker) | microsandbox backend | Notes |
|---|---|---|
| compose volumes | `--mount` dirs + disk-backed named volume for packages | packages volume persists brew/npm state |
| Conditional auto-mounts | `--mount-file` / `--mount` per-run | same `getXPath` helpers; per-backend rendering |
| Network permissive | msb `public` profile | |
| Network strict | **in-guest filter retained** + optional msb policy | see below |
| Network offline | no network | |
| **Dynamic mid-session block updates** | **survive** | Strict mode is enforced inside by `network-filter.sh` via `docker exec` (`network/manager.go:655`), not by Docker. A guest kernel with NET_ADMIN runs the same iptables/ufw. Rev 1 was wrong. Keep the in-guest filter; treat msb's boot-time policy as an optional second layer — stacked enforcement can break silently, so verify interactions explicitly |
| Daemon mode | persistent sandbox + `Exec` + `Attach` via SDK | benchmark vs ~100ms baseline |
| SSH agent forwarding | host-sockets or the `host.microsandbox.internal` transport | §3.1 |
| Clipboard bridge | **`host.microsandbox.internal` transport** (verified, §3.1) | alias + token auth pass |
| Host exec bridge | transport verified (§3.1); remaining work is PathMap container→host translation for every mount | |
| Loopback relays | `host.microsandbox.internal` transport | `host.docker.internal` is a Docker-ism; per-backend alias |
| Terminal identity env flags | env via SDK Exec options | trivial |
| Login callback ports | published ports | `engine.go:684` |
| Entrypoint overrides | SDK entrypoint/cmd options | update-all, install, sha256 verify |
| Staleness/entrypoint-hash check run | Backend-provided check exec | `IsContainerStale` equivalent |
| Volume reset/reinstall | volume `rm` equivalent | `ResetVolumes`, `ReinstallPackages` |
| hide_secrets | ProjectDir carries `sec.ProjectRoot()` merged root | not raw cwd |

## 7. Implementation order (revised) — task board

Spikes first, refactor second. Rev 1 had this backwards. Steps 1-2 are done; each remaining step is a checklist with verification criteria. Syntax: `[ ]` pending, `[x]` done, `[-]` obsolete.

**Step 1 — Spike A: image boot. DONE 2026-08-18.**

- [x] `docker save construct-box:latest` → `msb load -i` — works, image cached (3.4 GiB)
- [x] Boot under HVF, entrypoint runs (gosu drop, PATH, setup)
- [x] Host dir mount via `--mount-dir host:guest:rw`
- [x] `msb exec` into running sandbox (with stdin caveat, §7.1)
- [-] `pi --version` in-VM — obsolete as a spike item: agents install at first run into a volume; moved to Step 6 as a gate

**Step 2 — Spike B: guest→host transport + egress (the project gate). DONE 2026-08-18.**

- [x] Winning transport: `host.microsandbox.internal` DNS alias (§3.1)
- [x] Policy survival: `--no-net --net-rule "allow@host:tcp:<port>,allow@dns"` allows guest→host, denies public
- [x] Token-auth header passthrough (Bearer 200/401) — clipboard token auth viable as-is
- [x] Dead ends documented: `host.docker.internal`, gateway IP, raw LAN IP (§3.1)

**Step 2.5 — Session-derived pre-work (do anytime, benefits both backends).**

- [ ] Entrypoint: idempotence check for `chown -R construct:construct /home/linuxbrew/.linuxbrew` — skip when ownership already correct (probe file ownership, e.g. `bin/brew`). Cheap; removes 100-300s on every fresh VM disk (§7.1). Touches `internal/templates/entrypoint.sh` only; Docker path unaffected (volume already warm)
- [ ] Entrypoint: introduce a per-backend host alias variable (`CONSTRUCT_HOST_ALIAS`) replacing hard-coded `host.docker.internal` in SSH socat + loopback relays. Docker compose sets `host.docker.internal`; the msb backend sets `host.microsandbox.internal`. Keeps entrypoint backend-agnostic
- [ ] Decide `USER construct` (Dockerfile:107): gosu drop (current) vs msb `-u` direct. Record decision here and in §5
- [x] Record base-image decision: `debian:trixie-slim` stays. No RPM/musl/minimal alternative accepted (linuxbrew requires glibc + apt ecosystem; base is 75 MB of a 3.4 GB image — size irrelevant). Resolved 2026-08-18

**Step 3 — Primitive inventory + conformance tests.**

- [ ] Extract full primitive list from the 11 non-test `internal/runtime` importers (~30 primitives per §4.1)
- [ ] Write `Backend` conformance test suite (contract: run, exec, state, mounts, labels, volumes, staleness, exit codes). Docker = reference implementation; suite must pass unchanged against Docker BEFORE interface extraction
- [ ] Include in the suite: exit-code fidelity (126/127 PATH hint, `engine.go:451`), ordered Env semantics (`engine.go:623`), stdin handling (msb stdin trap, §7.1), exec-as-user (`ResolveExecUser` equivalent)
- [ ] Gate: CI runs conformance suite on every PR from here on

**Step 4 — Interface extraction.**

- [ ] Create `backend.go` (interface per §4.2, finalized against Step 3 inventory) + `backend_docker.go` (move current code, no behavior change)
- [ ] Update `runtime_test.go` (1818 lines) and `runner_test.go` (1530) alongside — they assert the current API
- [ ] Gate: zero behavior change — full `make check` green, conformance suite green on Docker, no new config keys

**Step 5 — Split `Prepare()`.**

- [ ] Separate backend-agnostic half (templates, install script, topgrade) from Docker-only half (compose override, `EnsureCustomNetwork`, sudo chown/config perms). Six callers updated
- [ ] Gate: Docker path byte-identical output (compose files unchanged for same inputs)

**Step 6 — MVP msb backend (experimental, opt-in).**

- [ ] `backend_msb.go` on `sdk/go`; `[runtime] backend = "msb"`, fail closed with install hint (§4.3). No fallback
- [ ] Image path automated: build → `docker save` → `msb load` (later `msb pull` from ghcr, Step 8)
- [ ] Disk-backed named volume for `/home/linuxbrew` (mandatory: chown cost, §7.1) + volume equivalents for packages/home (`~/.config/construct-cli/home`)
- [ ] Mounts: project dir, home, conditional auto-mounts (`--mount-file` for gitignore/qmd)
- [ ] Network: permissive = `--net public`; strict = in-guest filter via SDK exec as construct user (msb policy layer default OFF until stacking verified, §9); offline = `--no-net`
- [ ] Egress rule on every sandbox: `allow@host:tcp:<bridge-port>` + `allow@dns` (transport, §3.1)
- [ ] Env: ordered masking (`MaskEnv`) → SDK exec env; exit codes surfaced from SDK exec results
- [ ] First-run agent install inside VM (remaining unknown): `InstallAgentsAfterBuild` equivalent via SDK, log capture, non-TTY. GATE for the whole step — if brew/npm install fails in-guest, stop and reassess
- [ ] Entrypoint overrides: update-all.sh, install bash, sha256 verify (`runner.go:251`) via SDK entrypoint/cmd options
- [ ] Doctor: msb check set (binary, libkrunfw, HVF/KVM presence, image loaded, volumes present); Docker checks behind backend dispatch (`internal/doctor/doctor.go:227-406`)
- [ ] Clear "unsupported in msb yet" errors for daemon + bridges until Step 7
- [ ] Gate: conformance suite passes on msb except documented gaps; `construct claude "echo hi"` works end-to-end in the VM

**Step 7 — Daemon + bridges + parity.**

- [ ] `StartPersistent`/`Exec`/`Attach` via SDK; benchmark vs ~100ms Docker baseline; record numbers here
- [ ] Clipboard bridge over `host.microsandbox.internal` + existing token auth (verified §3.1)
- [ ] Host exec bridge: transport done; implement PathMap guest-path→host-path translation per mount
- [ ] SSH agent bridge: socat guest→`host.microsandbox.internal:<port>` (alias var from Step 2.5)
- [ ] Loopback relays: same alias var; verify under `allow@host` rule
- [ ] `sys exec` non-interactive stream; login callback ports via published ports (`engine.go:684`)
- [ ] Verify stacked network enforcement explicitly (in-guest filter × msb policy); document interaction or keep msb layer off
- [ ] Gate: all five bridges pass manual test matrix on msb; daemon overhead acceptable

**Step 8 — ghcr publish + promote.**

- [ ] Base image to ghcr in release workflow; `msb pull` documented as alternative to save+load
- [ ] Docs: README, CONFIGURATION, this file updated
- [ ] Promote from experimental after real usage on both macOS (HVF) and Linux (KVM)

### 7.1 msb operational notes (from Spikes A+B, msb v0.6.10)

Behavior the backend implementation must account for. Verified 2026-08-18 on macOS aarch64/HVF.

- **Exec stdin blocking.** `msb exec <sandbox> -- cmd` hangs forever if the caller's stdin is an open pipe with no EOF; `</dev/null` makes it instant. The Go SDK's exec API manages streams itself (stdout/stderr returned or streamed per call), so this is a CLI-invocation trap only. Any host-side shelling-out to `msb` must close stdin.
- **Output-stream EOF semantics.** msb treats an output stream as complete only when every process holding the write end closes it — not when the foreground command exits. Confirmed on plain alpine: `(sleep 300 &); echo done` hangs the run; with `>/dev/null 2>&1` redirect it returns in 0.2s. All entrypoint background daemons already satisfy this (SSH socat logs to `/tmp/socat.log`, loopback relays to `/tmp/loopback-<port>.log` at entrypoint.sh:123) — a maintained invariant, not new work.
- **Linuxbrew chown cost.** `chown -R construct:construct /home/linuxbrew/.linuxbrew` (entrypoint root block) takes 100-300s on a fresh msb ext4 root disk (uninterruptible D state). Docker hides this behind the `construct-packages` persistent volume. The msb backend must mount `/home/linuxbrew` on a disk-backed named volume (section 6 packages-volume row) so the chown touches a warm volume, not a fresh disk. Additionally worth an idempotence check (skip chown when ownership already correct) in the entrypoint — cheap, benefits both backends.
- **Exec user.** `msb exec` runs as guest root by default; `-u/--user` selects the construct user. The backend's Exec plumbing must always pass the user, mirroring `ResolveExecUser`.
- **Ephemeral one-shot sandboxes.** `msb run image -- cmd` auto-removes the sandbox when the command exits; guest state (e.g. /tmp files) is gone immediately after. Debugging needs `-d` + `msb exec`.
- **Entrypoint override.** `--entrypoint <exe>` replaces the image entrypoint (empty string rejected). CMD substitution via `-- cmd` preserves the image entrypoint as designed; construct's RunOptions.EntrypointOver maps to these flags.
- **`msb load` accepts Docker archives directly** — no conversion step needed for the Docker-to-msb image transition path.

## 8. Effort (realistic)

| Step | Estimate |
|---|---|
| Spikes A+B | 1-2 days |
| Inventory + conformance suite | 3-5 days |
| Interface extraction + test updates | 1-2 weeks |
| `Prepare()` split | 2-3 days |
| MVP msb backend | 2-3 weeks |
| Daemon + bridges + parity | 4-8 weeks |
| **Total to usable-experimental** | **~2-3 months** |

Single maintainer, part-time. Both spikes passed 2026-08-18 (§3.1, §7 Step 1); remaining unknown is the first-run agent install inside a VM (Step 6).

## 9. Risks

- **Transport gate. RESOLVED 2026-08-18 (§3.1).** If no guest→host transport works cleanly under msb egress policy, five differentiating bridges die. `host.microsandbox.internal` + narrow allow rules passed; residual risk is wiring complexity in Step 7, not viability.
- **Single-vendor runtime.** Apache-2.0 but one project. Mitigation: opt-in, Docker default, interface keeps swap-out cheap.
- **Maintainer load doubles.** Two runtimes, two bug-report streams. Experimental label until Step 8.
- **Stacked network enforcement.** In-guest iptables + msb host-side policy can interact badly. Verify explicitly; default to in-guest only.
- **Apple Silicon only on macOS.** Intel stays on Docker; smolvm is the future escape hatch.
- **Windows/WHP untested by us.** Best-effort until someone tests it.

## 10. Non-goals

- Not replacing Docker as default.
- Not bundling microsandbox; documented prerequisite like Docker today.
- Not delegating strict-mode enforcement entirely to msb (in-guest filter stays, preserving live updates).
- Not chasing smolvm `.smolmachine` packing or GPU. Out of scope.

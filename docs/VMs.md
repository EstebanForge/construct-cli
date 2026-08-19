# VM Backend Plan (microsandbox) — rev 2

Status: active implementation (Steps 1-6 complete; Step 7 in progress — the run path is live: `backend = "msb"` executes agents inside a persistent microsandbox daemon, live-verified 2026-08-19). Rev 2 was revised after peer review against the codebase. Target: opt-in second isolation backend using [microsandbox](https://github.com/microsandbox/microsandbox) (msb) microVMs, alongside the existing Docker/Podman/container backend.

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

### 4.1 Primitive inventory (extracted 2026-08-18, non-test importers)

11 non-test files import `internal/runtime` (plain `runtime` alias in 8, `runtimepkg` in doctor/password, `containerruntime` in exec). Full call inventory, by call count:

| Primitive | Calls | Family |
|---|---|---|
| `DetectRuntime` | 18 | host probe |
| `GetContainerState` | 12 | inspect |
| `BuildComposeCommand` | 11 | compose assembly |
| `ContainerStateRunning/Exited/Missing` | 9+8+5 | state enum |
| `Prepare` | 6 | setup |
| `ExecInContainerWithEnv` | 8 | exec |
| `ResolveExecUser` | 5 | exec |
| `CleanupExitedContainer` | 6 | lifecycle |
| `AppendRuntimeIdentityEnv` / `AppendProjectPathEnv` | 5+5 | env assembly |
| `ResolveDaemonMounts` / `MapDaemonWorkdirFromMounts` | 5+2 | mounts |
| `GetCheckImageCommand` | 6 | staleness |
| `StopContainer` | 4 | lifecycle |
| `IsContainerStale` / `IsContainerRunning` | 2+2 | inspect |
| `GetContainerWorkingDir` / `GetContainerMountSource` / `GetContainerLabel` | 2 each | inspect |
| `DaemonMountsLabelKey` | 2 | labels |
| `CwdContainerName` | 2 | naming |
| `BuildImage` | 2 | image |
| `ListContainersByPrefix` | 2 | inspect |
| `GetProjectMountPath` | 2 | mounts |
| `ExecNonInteractiveStream` / `ExecInteractiveAsUser` | 1 each | exec |
| `ContainerState` (type) | 1 | state enum |
| `UsesUserNamespaceRemap` | 1 | host probe |
| `IsRuntimeRunning` / `IsOrbStackRunning` | 1 each | host probe |
| `GenerateDockerComposeOverride` | 1 | compose assembly (Docker-only, stays out of `Backend`) |

Interface grouping: exec (3 fns), inspect (7), lifecycle (3), image/setup (3), mounts (4), env assembly (3), naming/labels (2), state enum + type. Host-probe and compose-assembly families are Docker-specific and stay outside the `Backend` interface; `DaemonMountsLabelKey` moves in as a `Labels` concern.

The conformance test suite (section 7, step 3) defines the contract; both backends must pass it.

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

Guest notes: real kernel, so the entrypoint root block (socat setcap, SSH bridge) runs unmodified. In-guest network filtering works (section 6). UID mapping (`CONSTRUCT_HOST_UID/GID`, userns-remap, SELinux `:z`) is Docker-only dead weight in this path. `USER construct` (Dockerfile:107) stays commented: the entrypoint root block must run as root on every backend, gosu drops to construct after it — decided 2026-08-18.

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

- [x] Entrypoint: idempotence check for `chown -R construct:construct /home/linuxbrew/.linuxbrew` — skip when ownership already correct (probe file ownership, e.g. `bin/brew`). Cheap; removes 100-300s on every fresh VM disk (§7.1). Touches `internal/templates/entrypoint.sh` only; Docker path unaffected (volume already warm). DONE 2026-08-18
- [x] Entrypoint: introduce a per-backend host alias variable (`CONSTRUCT_HOST_ALIAS`) replacing hard-coded `host.docker.internal` in SSH socat + loopback relays. Docker compose sets `host.docker.internal`; the msb backend sets `host.microsandbox.internal`. Keeps entrypoint backend-agnostic. DONE 2026-08-18 (entrypoint defaults to `host.docker.internal`; Docker needs no env change; remaining hard-code in `engine.go:1258` daemon socat is Docker-only and moves with Step 4)
- [x] Decide `USER construct` (Dockerfile:107): gosu drop (current) vs msb `-u` direct. Record decision here and in §5. DECIDED 2026-08-18: keep gosu drop. The entrypoint root block (socat setcap, chown, symlinks) must run as root on every backend; msb `-u` would skip it. Same entrypoint, both backends. `Dockerfile:107` stays commented.
- [x] Record base-image decision: `debian:trixie-slim` stays. No RPM/musl/minimal alternative accepted (linuxbrew requires glibc + apt ecosystem; base is 75 MB of a 3.4 GB image — size irrelevant). Resolved 2026-08-18

**Step 3 — Primitive inventory + conformance tests. DONE 2026-08-19.**

- [x] Extract full primitive list from the 11 non-test `internal/runtime` importers (~30 primitives per §4.1)
- [x] Write `Backend` conformance test suite (contract: run, exec, state, mounts, labels, volumes, staleness, exit codes). Docker = reference implementation; suite must pass unchanged against Docker BEFORE interface extraction
- [x] Include in the suite: exit-code fidelity (126/127 PATH hint, `engine.go:451`), ordered Env semantics (`engine.go:623`), stdin handling (msb stdin trap, §7.1), exec-as-user (`ResolveExecUser` equivalent)
- [x] Gate: CI runs conformance suite on every PR from here on (`.github/workflows/build.yml` job `conformance`; PR + main-push triggers added)

Fixes landed with the suite: `ExecInteractiveAsUser` passes `-t` only when stdin is a real terminal (docker rejects TTY attachment from non-tty callers, collapsing exit codes to 1 — found by the suite's exit-code test); `CwdContainerName` test aligned with the documented hash contract; suite skip logic probes docker/podman via `LookPath` + `IsRuntimeRunning` (DetectRuntime is side-effectful and fails the binary instead of skipping).

**Step 4 — Interface extraction. DONE 2026-08-19.**

- [x] Create `backend.go` (interface per §4.2, finalized against Step 3 inventory) + `backend_docker.go`. Deviation from sketch: facade instead of code move — package-level functions keep their signatures and behavior; `DockerBackend` delegates. Interface trimmed to the backend-agnostic inventory families (exec, inspect, lifecycle, image, naming/labels); host-probe and compose-assembly stay outside as planned. `ResolveUser` dropped from the interface: user resolution needs `*config.Config`, callers resolve and pass `ExecOptions.User`
- [-] `runtime_test.go` (1818 lines) and `runner_test.go` (1530): no update needed — current API unchanged (facade approach), all existing tests pass as-is
- [x] Gate: zero behavior change — full `make check` green, conformance suite green on Docker (new `TestConformanceBackendInterface` runs the suite through the interface), no new config keys

**Step 5 — Split `Prepare()`. DONE 2026-08-19.**

- [x] Split into exported `PrepareBackendAgnostic(cfg, configPath)` (mounted helper templates, user-packages install script, topgrade config) and `PrepareDockerSpecific(cfg, containerRuntime, configPath)` (config dir permissions, strict-mode `EnsureCustomNetwork`, `GenerateDockerComposeOverride`). `Prepare` remains as the Docker composition of both halves
- [-] Six callers updated: not needed — `Prepare` keeps its signature, all callers (daemon, runner ×2, ops ×2, password) unchanged. The msb backend (Step 6) calls `PrepareBackendAgnostic` only
- [x] Gate: Docker path byte-identical output (compose files unchanged for same inputs; all runtime/daemon/agent/sys tests green, `make check` green, conformance green)

**Step 6 — MVP msb backend (experimental, opt-in). IN PROGRESS.**

- [ ] `backend_msb.go` on the msb Go SDK; `[runtime] backend = "msb"`, fail closed with install hint (§4.3). No fallback — SKELETON LANDED 2026-08-19: `backend_msb.go` implements the Backend interface (exec with exit-code fidelity via SDK ExecOutput, state mapping, stop/cleanup, list, image save→load transition) with `ErrMsbUnsupported` stubs for inspect/stream/staleness; `DetectBackend(cfg)` is the fail-closed factory; config key `[runtime] backend` added (default "docker"). SDK import path correction: `github.com/superradcompany/microsandbox/sdk/go` (module moved; nested module, own tags, `go get github.com/superradcompany/microsandbox/sdk/go@v0.6.10`) — the plan's `sdk/go` under the old org path no longer resolves
- [x] Wire `DetectBackend` guard into the entry paths — DONE 2026-08-19: `runtime.ValidateBackendSelected(cfg)` fails closed in `runner.Run`, `runner.RunWithProvider`, `daemon.Start`; `backend = "msb"` now yields a clear Step-6 error instead of silently running Docker. Verified live with a built binary. Remaining entry paths (sys exec/ops, network manager, clipboard-debug) still need the guard when their msb stories land
- [x] Image path automated: build → `docker save` → `msb load` (implemented in EnsureImage; image-loaded probe verified live, save→load transition verified manually in Spike A)
- [x] Fail-closed guards extended to ALL entry paths — DONE 2026-08-19: guard centralized in `runtime.Prepare` (covers sys ops ×4, password, clipboard paths) plus explicit `ValidateBackendSelected` in daemon Stop/Restart/Attach/Status, network manager AddRule/RemoveRule/ShowStatus, sys exec, clipboard-debug
- [x] Disk-backed named volume for `/home/linuxbrew` (mandatory: chown cost, §7.1) + volume equivalents for packages/home (`~/.config/construct-cli/home`) — LANDED + LIVE-VERIFIED 2026-08-19: `EnsureMsbVolumes` (construct-packages = disk with explicit 20 GiB size — msb requires `.size` on disk volumes), `msbSandboxMounts` (volumes + project bind + conditional auto-mounts with RW qmd cache), `BuildMsbRunSpec` pure builder, `CreateMsbSandbox` (+ `msbSeedAutoFiles`). Live test `backend_msb_live_test.go` (env-gated `CONSTRUCT_MSB_LIVE=1`) verifies create→exec→exit-code fidelity→mount propagation→linuxbrew PATH→gitignore seed→stop→cleanup against a real daemon
- [x] Network: permissive = default-allow egress; strict = same (in-guest filter, msb policy layer default OFF, §9); offline = deny-by-default — implemented in `msbNetworkConfig`; every profile carries `allow@host:tcp:<port>` + DNS(udp/53) transport rules (§3.1)
- [x] Egress rule on every sandbox: `allow@host:tcp:<bridge-port>` + `allow@dns` (transport, §3.1) — in `msbNetworkPublic`/`msbNetworkOffline`
- [x] Env: ordered masking (`MaskEnv`) → SDK exec env; exit codes surfaced from SDK exec results — DONE 2026-08-19: `MsbBackend.Exec` converts the ordered env slice via `envSliceToMap` (repeated keys, later assignment wins — the semantics `engine.go:623` in-place masking relies on; covered by `TestEnvSliceToMap`) and returns `ExecOutput.ExitCode()` verbatim; live exit-code fidelity verified in `backend_msb_live_test.go`
- [x] First-run agent install inside VM (gate reached): `MsbInstallAgents` runs the entrypoint's installer via `sb.ExecDefault` (the SDK does NOT auto-run the default workload at create — the CLI does that wiring; the SDK caller must invoke it). The one-shot `CreateMsbSandbox` with `Cmd={"echo", "Installation complete"}` blocks until the entrypoint finishes; non-zero exit propagates. The packages disk volume was removed: msb does not copy image content into empty named volumes on first mount (Docker does), so a fresh volume shadows the image's `/home/linuxbrew/.linuxbrew` entirely. State instead lives in the sandbox root disk; named sandboxes persist across stop/start (verified 2026-08-19), which matches the plan's daemon model. Live gate (`backend_msb_live_test.go::TestMsbLiveAgentInstall`) is intentionally scoped to a single `npm install -g http-server@14` payload, not the full `GenerateInstallScript` output: the full script contains an unguarded `curl -fsSL https://bun.sh/install | bash` with `set -e`, which aborts on the first network blip and the entrypoint's `|| echo warn` swallows the failure. Docker's pre-warmed packages volume masks this; the msb one-shot has no retry. The full install is Step 7's persistent-sandbox retry loop's problem; tracked in `docs/VMs.md §7.1` and not part of Step 6's gate.
- [x] Entrypoint overrides: update-all.sh, install bash, sha256 verify (`runner.go:251`) via SDK entrypoint/cmd options — DONE 2026-08-19: `MsbRunSpec.Entrypoint` (empty = keep image entrypoint) maps to `msb.WithEntrypoint` in `CreateMsbSandbox`; msb rejects empty overrides so the option is passed only when set. Engine-side callers land with the Step 7 run-path wiring
- [x] Doctor: msb check set (binary, libkrunfw, HVF/KVM presence, image loaded, volumes present); Docker checks behind backend dispatch (`internal/doctor/doctor.go:227-406`) — DONE 2026-08-19: `msbBackendCheck` probes binary/version, daemon reachability (`msb list`), image loaded, platform (Intel-mac warning, HVF framework, /dev/kvm), and delegates libkrunfw/root-clone checks to `msb doctor`. Volume probe dropped (packages volume removed). Backend dispatch: `backend = "msb"` blanks `runtimeName` after the Runtime Check so every container-runtime check (compose network/fixes, image fixes, daemon container state) takes its skipped branch
- [x] Clear "unsupported in msb yet" errors for daemon + bridges until Step 7 — DONE 2026-08-19: `ValidateBackendSelected` guards runner (Run, RunWithProvider), daemon (Start/Stop/Restart/Attach/Status), sys exec, clipboard-debug, network manager (AddRule/RemoveRule/ShowStatus), and centrally `runtime.Prepare` (sys ops, password, clipboard paths) — bridges reach the host only through these paths, so none can silently fall through to Docker
- [x] Gate: live install path proven (npm → host home bind). End-to-end `construct claude --version` verified in Step 7 (2026-08-19): agent binary resolves on PATH and reports its version from inside the VM.

**Step 7 — Daemon + bridges + parity. IN PROGRESS.**

- [x] `StartPersistent`/`Exec`/`Attach` via SDK; benchmark vs ~100ms Docker baseline; record numbers here — DONE 2026-08-19, live-verified on macOS aarch64/HVF: `Backend.ExecInteractive` added to the interface (Docker delegates to `ExecInteractiveAsUser`; msb uses SDK `ExecStream` + `WithExecTTY` + `WithExecStdinPipe`, raw mode + SIGWINCH resize forwarding). Engine `Execute()` dispatches `backend = "msb"` to `execViaMsbDaemon` (persistent sandbox `construct-cli-daemon`, first-run `MsbInstallAgents`, `buildDaemonExecEnv` + `MaskEnv` env contract, exit-code fidelity incl. 126/127 hint). Runner entry points branch to `runMsbWithArgs` (`PrepareBackendAgnostic`, no Docker prepare/setup chain). `sys daemon start/stop/restart/attach/status` all dispatch to the sandbox (attach = interactive shell via ExecInteractive). **Benchmarks**: warm run `construct claude --version` = 0.29-0.38s (Docker daemon baseline ~100ms; acceptable). Cold sandbox create = 6-11 min (chown grind, below); sandbox stop→start = ~6 min (virtiofs ownership overlay resets on reboot → home chown re-runs; daemon persists across construct runs so this is rare — idle timeout and max duration are set to unlimited at create)
- [ ] Clipboard bridge over `host.microsandbox.internal` + existing token auth (verified §3.1) — PARTIAL: engine already builds `CONSTRUCT_CLIPBOARD_URL` with the msb host alias when backend = msb (bridge servers bind the alias); end-to-end clipboard paste test pending
- [ ] Host exec bridge: transport done; implement PathMap guest-path→host-path translation per mount — PARTIAL: hostexec server starts with the msb alias and the /workspace PathMap already matches `msbSandboxMounts`' project bind; explicit per-mount translation pending
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

Behavior the backend implementation must account for. Verified 2026-08-18 on macOS aarch64/HVF; Step 7 additions verified 2026-08-19.

- **Attached sandboxes die with their creator.** `CreateSandbox` without `WithDetached()` powers the VM down when the creating process exits ("creator process exited; stopping attached sandbox" in `msb logs --source system") — and killing the creator also kills in-flight exec sessions' guest processes. Daemon sandboxes MUST use `WithDetached()` + `Detach()` (never `Close()`, which stops a detached VM).
- **Default lifecycle limits kill daemons.** msb defaults reboots the sandbox after idle inactivity; `WithIdleTimeout(0)` + `WithMaxDuration(0)` (both = unlimited) are set at create for daemon semantics.
- **Entrypoint readiness marker.** The entrypoint touches `/tmp/.construct_entrypoint_ready` right before `exec "$@"`. It lives on tmpfs: absent on every fresh boot, so the backend uses it to decide whether to (re-)run the default workload (`msbRunDefaultAsync`) and waits for it (`msbWaitKeeper`, 10-min budget) before handing the sandbox to an agent exec. Do NOT probe with `pgrep -f "sleep infinity"`: every msb exec wraps in `sh -c`, so the pattern matches the probe itself.
- **Recursive chown goes through the xattr overlay and takes minutes.** The entrypoint's `chown -R /home/construct` (root phase AND the non-root sudo phase) grinds 4-6 min per pass on a populated home. Both phases now carry idempotence probes (stat a stable dir: `/home/construct/.local`, `/home/linuxbrew/.linuxbrew/bin/brew`) and skip when warm. Caveat: the virtiofs ownership overlay resets on sandbox stop/start, so a daemon restart re-runs the home chown (~6 min); linuxbrew (real root-disk ext4) stays warm.
- **Exec stdin blocking.** `msb exec <sandbox> -- cmd` hangs forever if the caller's stdin is an open pipe with no EOF; `</dev/null` makes it instant. The Go SDK's exec API manages streams itself (stdout/stderr returned or streamed per call), so this is a CLI-invocation trap only. Any host-side shelling-out to `msb` must close stdin.
- **Output-stream EOF semantics.** msb treats an output stream as complete only when every process holding the write end closes it — not when the foreground command exits. Confirmed on plain alpine: `(sleep 300 &); echo done` hangs the run; with `>/dev/null 2>&1` redirect it returns in 0.2s. All entrypoint background daemons already satisfy this (SSH socat logs to `/tmp/socat.log`, loopback relays to `/tmp/loopback-<port>.log` at entrypoint.sh:123) — a maintained invariant, not new work.
- **Linuxbrew chown cost.** `chown -R construct:construct /home/linuxbrew/.linuxbrew` (entrypoint root block) takes 100-300s on a fresh msb ext4 root disk (uninterruptible D state). Docker hides this behind the `construct-packages` persistent volume. The msb backend must mount `/home/linuxbrew` on a disk-backed named volume (section 6 packages-volume row) so the chown touches a warm volume, not a fresh disk. Additionally worth an idempotence check (skip chown when ownership already correct) in the entrypoint — cheap, benefits both backends.
- **Exec user.** `msb exec` runs as guest root by default; `-u/--user` selects the construct user. The backend's Exec plumbing must always pass the user, mirroring `ResolveExecUser`.
- **Single-file bind mounts fail fatally.** agentd bind-mounts the target only if it already exists in the guest image; a missing target aborts sandbox start with a silent `exit status: 0` / `ENOENT` in `msb logs --source system` (unlike Docker, which auto-creates it). No parent-dir trick helps: even `/etc/<file>` fails if the file itself does not exist. Consequence: the global-gitignore auto-mount is seeded post-boot via SDK `FS().CopyFromHost` (`msbSeedAutoFiles`), not bind-mounted. Dir mounts (qmd models) are unaffected.
- **Disk volumes need an explicit size.** `msb.CreateVolume` with `VolumeKindDisk` requires `WithVolumeSize`; construct-packages uses 20 GiB (`msbPackagesVolumeSizeMiB`).
- **Default workload must not exit.** The image CMD (`/bin/bash`) exits immediately without a TTY (Docker's compose keeps it alive via `stdin_open`+`tty`; msb has no equivalent). `CreateMsbSandbox` sets `WithCmd("sleep", "infinity")`.
- **Host paths need symlink resolution.** msb binds the literal path; macOS `/var/folders` temp dirs (symlinked to `/private/var`) fail with `ENOTDIR`. `msbSandboxMounts` runs `filepath.EvalSymlinks` on the project dir.
- **Sandbox teardown states.** Status passes `running → draining → stopped`; `Remove` refuses anything but `stopped`. `Cleanup` does stop → `Refresh`-poll until `stopped` → remove.
- **Default workload is NOT auto-run by the SDK.** `WithCmd`/`WithEntrypoint` only configure the default workload — the SDK never starts it at create. The CLI does that wiring for `msb run`; SDK callers must invoke `sb.ExecDefault` (one-shot, blocks to exit) or run it in a goroutine for persistent sandboxes. `CreateMsbSandbox` does this for both cases. Without it, the guest boots with `init.krun` and `agentd` only — the entrypoint and CMD never run.
- **Named sandboxes persist across stop/start.** The root disk (and linuxbrew on it) survives a `RequestStop` + `Start` cycle (verified 2026-08-19). This is the foundation for keeping agent state across `construct` invocations on msb, replacing the Docker packages volume which msb cannot replicate (no copy-image-content-on-first-mount).
- **Named volumes do NOT auto-populate from image content.** Unlike Docker's `compose run` first-mount copy, an empty msb named volume shadows the image's contents for the mount target. Anything stateful that lives there at build time needs a different home — either the root disk (preferred for linuxbrew) or a host bind (used for `~/.config/construct-cli/home`).
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

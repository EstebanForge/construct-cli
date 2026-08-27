# AGENTS.md

## Build & Test
- Build: `make build` (outputs `bin/construct`, unsigned local build)
- Sign (optional, macOS): `make sign` or `make build-signed`
- Lint: `make lint` (golangci-lint)
- Full checks: `make check` or `./scripts/checks.sh` (`fmt -> vet -> lint -> test -> build`)
- CI alias: `make ci` (same as `make check`)
- Test: `make test` (`go mod download`, `go mod verify`, then `go test ./...`; `-race` when CGO is enabled)
- Unit only: `make test-unit` or `go test ./internal/...`
- Integration only: `make test-integration`
- Single test: `go test -run TestName ./internal/path/...`
- Single package: `go test -v ./internal/config`
- Coverage: `make test-coverage` (outputs `coverage.html`)

## Code Style
- Format: `make fmt` (`go fmt ./...`, then `goimports -w .` if installed)
- Naming: MixedCaps (no underscores), Uppercase=exported, lowercase=unexported
- Errors: Always check, use `fmt.Errorf("context: %w", err)` for wrapping
- Comments: `// Package name` at top, godoc for exported funcs
- Interfaces: Single-method interfaces end with `-er` suffix
- Testing: `TestXxx` funcs, `BenchmarkXxx` for benches
- Line length: No limit, let `gofmt` wrap

## PATH Construction
- PATH is hardcoded and must be kept in sync across these files:
- `internal/env/env.go` (BuildConstructPath)
- `internal/templates/entrypoint.sh`
- `internal/templates/docker-compose.yml`
- `internal/templates/Dockerfile`

## Conditional Host Mounts
- `internal/runtime/runtime.go` (`GenerateDockerComposeOverride`) adds host→container bind-mounts that activate only when the host path exists (no config flag): host global gitignore → `/home/construct/.config/git/ignore:ro` (`getGlobalGitIgnorePath`), and host qmd GGUF model cache `~/.cache/qmd/models` → `/home/construct/.cache/qmd/models` (`getQmdModelsPath`, RW so lazily-fetched reranker/generation models write back to the shared host cache).
- Every conditional mount must also be added to the `overrideInputs` struct and `hashOverrideInputs`, or `docker-compose.override.yml` will not regenerate when the host path appears/disappears.
- To add a new auto-mount: write a `getXPath() (string, bool)` helper (resolve `$HOME`/XDG, `os.Stat` + `IsDir`), add the field + hash line, and append the mount in BOTH the `linux` and `darwin` volume blocks (linux carries `selinuxSuffix`, darwin omits it).

## Terminal Identity Forwarding
- `internal/agent/engine.go` (`terminalIdentityEnvFlags`) auto-forwards host terminal-identity markers (`KITTY_WINDOW_ID`, `GHOSTTY_RESOURCES_DIR`, `TERM_PROGRAM`) into the container as `-e` flags so in-container TUIs / pi extensions can detect the outer terminal (kitty-graphics inline images, etc.).
- Wired into BOTH launch paths: `e.buildRunFlags` (direct `compose run`) and `startDaemonBackground` (daemon `compose run -d`, so `docker exec` sessions inherit it).
- `TERM` is intentionally NOT forwarded (terminfo mismatch risk; identity vars suffice for detection). Users who need it: add `TERM` to `env_passthrough` and install `ncurses-term`/`kitty-terminfo` in the image.

## Host Loopback Forwarding (browser → host dev sites)
- Chromium hardcodes `localhost` and `*.localhost` to `127.0.0.1` (RFC 6761), bypassing `/etc/hosts`, DNS, `dnsmasq`, and `--host-resolver-rules`. So DNS-layer fixes (extra_hosts, host_aliases) CANNOT make a headless browser (agent-browser) reach host dev sites like `http://hyperpress.localhost`. They only help non-browser tools (curl/git/MCP).
- Solution: blind TCP relays on the container's `127.0.0.1` → `host.docker.internal`, launched by `internal/templates/entrypoint.sh` (socat, next to the SSH bridge). Blind relay preserves HTTP Host header + TLS SNI, so host vhost routers and certs see the real hostname.
- Config: `[sandbox] host_loopback_ports` (list of ints, default `[80, 443]`). Same port both sides. Add non-standard ports (e.g. `3000`) as needed. Empty list disables.
- Plumbing:
  - `internal/config/config.go` — `HostLoopbackPorts` field + default.
  - `internal/runtime/runtime.go` (`GenerateDockerComposeOverride`) — emits `CONSTRUCT_LOOPBACK_PORTS` env + `cap_add: NET_BIND_SERVICE` (consolidated with strict-mode `NET_ADMIN` via a `caps` slice — never write two `cap_add:` blocks). `LoopbackPorts` is part of `overrideInputs`/`hashOverrideInputs` so changing the list regenerates the override.
  - `internal/templates/Dockerfile` — installs `libcap2-bin` (provides the `setcap` binary). The file cap is **not** applied at build time: BuildKit's default sandbox blocks file-capability writes during `docker build` (`Invalid file 'setcap' for capability operation`). Instead, `internal/templates/entrypoint.sh` runs `setcap cap_net_bind_service+ep /usr/bin/socat` as root in its startup block before dropping to the construct user (idempotent + best-effort). The file cap still needs the cap in the bounding set, hence `cap_add` too — both required.
- Platform caveat: macOS host-gateway routes to host `127.0.0.1` (reaches loopback-bound dev servers). Linux host-gateway is the bridge IP — host services must bind `0.0.0.0`/bridge, not `127.0.0.1`-only.
- `localhost` itself is NOT remapped (would shadow `127.0.0.1 localhost` or break in-container loopback services). For host `localhost` services, the relay on port 80/443 covers `http://localhost`/`https://localhost`; for other host-loopback use cases prefer `host.docker.internal`.

## MicroVM Daemon (msb backend)

- Daemon flock: `internal/runtime/daemon_lock.go` (`acquireDaemonLock`). Any code that stops, recreates, or makes count-based decisions about the msb daemon must hold the flock while reading `LiveSessionCount()` AND acting (`internal/runtime/idle_watch.go` `StopMsbDaemonBestEffort` is the reference pattern). Count reads outside the lock race concurrent watchers and fresh `EnsureMsbDaemon` calls. The 250ms "Waiting for another construct invocation" notice measures the acquisition wait only; disarm fires on acquire, never on release.
- Daemon recreate labels: the daemon is stamped with `construct.daemon.*` labels (`mounts_hash`, `skills_hash`) by `BuildMsbRunSpec`; `msbDaemonNeedsRecreate` (`internal/runtime/backend_msb_run.go`) compares them against current config to decide recreate-with-reason. A new daemon-affecting runtime config knob MUST add its label + recreate check, or toggling it on a running daemon stays invisible until manual recreate (the skills_hash gap shipped exactly this way and needed a review round to catch).
- Mounts: configured roots (`daemon.mount_paths`) + learned roots (`requestLearnRoot`, capped, oldest evicted) feed ONE combined hash into `construct.daemon.mounts_hash`. A cwd outside the set returns `ErrMsbDaemonWorkdirUnmapped` (error, never destructive). `ResolveDaemonMountsWithLearned` is a deliberate no-op wrapper over `ResolveDaemonMounts`; do not "fix" it away.
- Prepull: `internal/runtime/prepull.go` pulls `ghcr.io/estebanforge/construct-box:latest` detached, spawned only after an ACTUAL self-update (the "already on latest version" no-op returns before the spawn). The deterministic foreground path is `construct sys prepull`. Opt-out: `runtime.prepull_image = false`.
- Design + peer-review trail: [docs/VMsv2.md](docs/VMsv2.md). Dogfood procedures: [docs/DOGFOODING-1.16.3.md](docs/DOGFOODING-1.16.3.md).

## Run-Path Output (stdout is the agent's)

Anything printed BEFORE or DURING agent execution must go to stderr (`ui.Info`, `ui.InfoLn`, `ui.InfoF` in `internal/ui`). Harnesses spawn agents in RPC modes that stream line-delimited JSON on stdout; any banner printed there corrupts the protocol stream. The interactive attach prompt in `engine.go` and explicit CLI output (`construct agents`, help screens) are the only intentional stdout on agent paths.

## Harness Path-Arg Staging

`internal/agent/arg_staging.go` stages orchestrator host path args into the construct home before any run path branches (`engine.Prepare`). When touching agent spawn behavior, keep these invariants: flags per agent live in `agentPathFlags`; staged files land under `.construct-staging/<run-id>` (0700); allowed roots are temp trees, the agent host config dir, and the caller cwd; values outside the roots stay untouched; `--session` values get a Teardown copy-back. Details: [docs/HARNESS-STAGING.md](docs/HARNESS-STAGING.md).

## Release Builds (cgo constraint)

The microsandbox SDK's FFI bridge is an untagged cgo file, so plain `GOOS`/`GOARCH` cross builds (cgo off) drop it and fail with "build constraints exclude all Go files" in `internal/ffi`. Release artifacts are built where a cgo toolchain exists (`.github/workflows/release.yml`): darwin on a macOS runner (clang `-arch` for amd64, native `lipo`), linux/amd64 native, linux/arm64 via `gcc-aarch64-linux-gnu`. Do not reintroduce a single-runner cross-compile matrix; `make cross-compile` remains local-dev only and cannot produce release artifacts for foreign platforms.

## Image Publish (decoupled from releases)

The `construct-box` GHCR image is NOT built by the release workflow. The CLI always pulls `construct-box:latest` (no version coupling), so image publishes are a separate manual workflow: `.github/workflows/image.yml` (`workflow_dispatch`, optional `version` input pins `ghcr.io/estebanforge/construct-box:<version>` alongside `:latest`). Dispatch it when the image definition changes: `internal/templates/Dockerfile` plus the files it COPYs (`entrypoint.sh`, `update-all.sh`, `network-filter.sh`, `clipper`, `clipboard-x11-sync.sh`, `osascript`, `construct-host-exec`). Multi-arch (amd64 + arm64 via QEMU) with GitHub Actions cache; dispatches are serialized by a concurrency group.

## Version Bumping
- **NEVER** modify the `VERSION` file - it's managed by GitHub Actions
- **NEVER** modify the `VERSION-BETA` file manually - it's managed by GitHub Actions for prereleases
- The release workflow triggers on TAG PUSH (`git push origin <version>`). A `chore(release)` commit alone ships nothing: no tag push means no GitHub release, no artifacts, no VERSION bump, and stable users stay on the old version
- When asked to bump version: update `internal/constants/constants.go` only
- When asked to add CHANGELOG entry: add new section with current version from constants.go
- `VERSION` is updated by release workflow for stable tags (e.g. `1.3.8`)
- `VERSION-BETA` is updated by release workflow for prerelease tags (e.g. `1.3.9-beta.1`)
- Version strings and release tags are plain semver/prerelease values with **no** `v` prefix (use `1.4.0-beta.3`, never `v1.4.0-beta.3`)
- Keep `internal/constants/constants.go` version exactly aligned with the tag being released (stable or prerelease), or `make release` fails `check-version`
- Stable users track `VERSION`; beta users track `VERSION-BETA` when `runtime.update_channel = "beta"`

## Adding/Removing CLI Agents

### Adding an Agent
1. Add package to `internal/templates/packages.toml` under the correct section (`[npm]`, `[bun]`, or `[brew]`).
2. Register agent mount in `internal/agent/agent.go` (Name, Slug, ConfigPath).
3. Register AGENTS.md rules path in `internal/sys/memories.go` and update `internal/sys/memories_test.go` (bump count + add assertion).
4. Add slug to the available agents list in `internal/ui/help.go`.
5. Add slug to post-update verification loop in `internal/templates/update-all.sh`.
6. Add slug to post-install verification loop in `internal/config/packages.go` (`GenerateInstallScript`).
7. Update docs:
   - `README.md` — "Available AGENTS" list + yolo_agents comment.
   - `docs/ARCHITECTURE-DESIGN.md` — Section 5 agent list.
   - `AGENTS.md` — Agent Additions Log (below).
8. If the agent needs setup commands, add them in `[post_install].commands` in `internal/templates/packages.toml`.
9. If the agent requires first-run setup that should not be automated, gate the run in `internal/agent/runner.go` and use a marker file under Construct home (e.g., `~/.config/<agent>/.construct_configured`) to prompt once and record completion.

### Removing an Agent
Reverse the steps above: remove the package from `packages.toml`, unregister from `agent.go`, remove from `memories.go` + test, remove from `help.go`, remove from both verification loops (`update-all.sh` and `packages.go`), and remove from docs (`README.md`, `ARCHITECTURE-DESIGN.md`). Add a removal note to the Agent Additions Log.

## Agent Additions Log
- Kilo Code CLI
  - Command: `npm install -g @kilocode/cli` (run as `kilocode`)
  - Rules path: `~/.kilocode/rules/AGENTS.md`
  - Files updated: `internal/templates/packages.toml`, `internal/agent/agent.go`, `internal/sys/memories.go`, `internal/sys/memories_test.go`, `internal/ui/help.go`, `README.md`
- Crush CLI
  - Command: `npm install -g @charmland/crush` (run as `crush`)
  - Rules path: `~/.config/crush/AGENTS.md`
  - Files updated: `internal/templates/packages.toml`, `internal/agent/agent.go`, `internal/sys/memories.go`, `internal/sys/memories_test.go`, `internal/ui/help.go`, `internal/templates/update-all.sh`, `internal/config/packages.go`, `internal/agent/runner.go`, `internal/templates/config.toml`, `README.md`, `docs/ARCHITECTURE-DESIGN.md`
- Antigravity CLI (replaced Gemini CLI)
  - Command: `curl -fsSL https://antigravity.google/cli/install.sh | bash` (run as `agy`)
  - Rules path: `~/.antigravity/AGENTS.md`
  - Binary: `~/.local/bin/agy`
  - Install method: curl (not npm)
  - Files updated: All Go source files (agent, sys, constants, env, config, runtime, engine, help, shell, packages, tests), all shell templates (entrypoint, update-all, agent-patch, config.toml, clipper, packages.toml), all docs (README, AGENTS, ARCHITECTURE-DESIGN, CONFIGURATION, CLIPBOARD, TODO, PROVIDERS), .gitignore
  - Removed: `patch_gemini_paste_wrapper()` function from agent-patch.sh (~220 lines), GEMINI.md symlink from entrypoint.sh, `@google/gemini-cli` from packages.toml npm section, `gemini-cli-main` and `.gemini-clipboard` from .gitignore
  - Renamed: `GEMINI_API_KEY` → `ANTIGRAVITY_API_KEY` throughout

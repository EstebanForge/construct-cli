# Construct CLI – Design Notes
**Tagline:** The Secure Loading Program for AI Agents.

---

## 1. Executive Summary
Construct CLI is a single-binary tool that launches an isolated, ephemeral container preloaded with AI agents. It embeds templates (Dockerfile, compose, entrypoint, network/clipboard shims), writes them to `~/.config/construct-cli/` on first run, builds the image, and installs tools via a `packages.toml`-driven script into a persistent volume. Its runtime engine is configurable (auto-detecting `container`, `podman`, or `docker` with macOS 26+ native support), and migrations keep templates/config aligned across versions. Network isolation supports `permissive`, `strict`, and `offline` modes with allow/block lists, plus a clipboard bridge for text/image paste, login callback forwarding, SSH agent forwarding (Linux socket mount, macOS TCP bridge) with optional key import, global AGENTS.md management, host alias management (sandboxed + ns-), and yolo-mode flags per agent. It also ships a headless Agent Browser CLI and Worktrunk for parallel agent workflows.

---

## 2. Goals & Requirements
- **Zero-trace host**: Containers run with `--rm`; only named volumes persist installs/state.
- **Flexible runtime support**: User-configurable engine with auto-detection and auto-start capabilities.
- **Fast subsequent runs**: Agents and tools live in a persistent named volume (`construct-packages`); optional daemon mode enables instant agent startup (~100ms) via container reuse.
- **Network control**: Allow/deny lists with `permissive/strict/offline`; strict mode creates a custom bridge network.
- **Single config**: TOML at `~/.config/construct-cli/config.toml` with `[runtime]`, `[sandbox]`, `[network]`, `[maintenance]`, `[agents]`, `[daemon]`, `[claude]`.
- **SSH access**: Forward SSH agent when available (configurable); `sys ssh-import` can copy host keys into the persistent home volume.
- **Git identity propagation**: Optional `user.name`/`user.email` injection into container env.
- **Packages customization**: `packages.toml` drives tool installs (apt, brew, npm, pip, cargo, gems), post-install hooks, and topgrade config generation.
- **Clear UX**: Gum-based prompts/spinners; `--ct-*` global flags avoid agent conflicts; `ct` symlink creation is attempted on basic help/sys invocations.
- **Agent rules + aliases**: Global AGENTS.md management, plus host alias installation (sandboxed and ns- for direct host use).
- **Yolo mode**: Optional per-agent or global "yolo" flags injected on launch.
- **Flexible Claude Integration**: Configurable provider aliases for Claude Code with secure environment management.
- **Safe upgrades**: Versioned migrations refresh templates and merge config with backups; daemon container is properly handled during upgrades.
- **Self-Update**: Automatic checks against the published release marker file (`VERSION` for stable, `VERSION-BETA` for beta channel); updates use tarball install with backup/rollback, and Homebrew installs can self-update via a user-local override binary.
- **Log maintenance**: Configurable cleanup of old log files under `~/.config/construct-cli/logs/`.
- **Daemon management**: Optional background daemon for instant agent execution with auto-start on login/boot via system services (launchd/systemd), plus opt-in multi-root mounts for cross-workspace reuse.
- **Toolchain**: Default `packages.toml` installs brew/cargo/npm tools like `ripgrep`, `fd`, `eza`, `bat`, `jq`, `yq`, `sd`, `fzf`, `gh`, `git-delta`, `git-cliff`, `shellcheck`, `yamllint`, `neovim`, `uv`, `vite`, `webpack`, agent-browser, language runtimes (Go, Rust, Python, Node, Java, PHP, Swift, Zig, Kotlin, Lua, Ruby, Dart, Perl, Erlang, etc.), and agents/tools (`gemini-cli`, `opencode`, `block-goose-cli`, `@openai/codex`, `@qwen-code/qwen-code`, `@github/copilot`, `cline`, `@kilocode/cli`, `@mariozechner/pi-coding-agent`, `mcp-cli-ent`, `md-over-here`, `url-to-markdown-cli-tool`, `worktrunk`).

---

## 3. Architecture
- **Entrypoint**: `cmd/construct/main.go`
  - Embeds templates (Dockerfile, docker-compose.yml, entrypoint.sh, update-all.sh, network-filter.sh, config.toml, packages.toml, and clipboard shims).
  - Handles namespaces: `sys`, `network`, `daemon`, `cc`, and agent passthrough.
  - Migration check before config load; log cleanup; passive update check.
  - Runtime detection/startup, build, update, reset, doctor, and agent execution.
  - Applies yolo flags per-agent based on config.
  - Login-bridge toggle via a flag file to enable localhost callback forwarding.
  - Claude provider system for configurable API endpoints with environment variable management.
  - Self-update mechanism with channel-aware version marker checks, release download, and atomic binary replacement (including Homebrew installs via user-local override).
- **Templates**: `internal/templates/`
  - Dockerfile uses `debian:trixie-slim` + Homebrew (non-root) for tools; installs Chromium and Puppeteer-compatible deps; disables brew auto-update.
  - docker-compose.yml plus auto-generated override for OS/network specifics.
  - entrypoint installs tools on first run based on generated `install_user_packages.sh`, enforces PATH, shims clipboard tools, and starts login forwarders when enabled.
  - network-filter script for strict mode; update-all for maintenance.
  - clipper, clipboard-x11-sync.sh, osascript shim, and powershell.exe for clipboard bridging.
  - packages.toml template used to generate the install script (apt/brew/npm/pip/cargo/gems + post-install hooks).
- **PATH Construction**
  - PATH is hardcoded and must be kept in sync across these files:
  - `internal/env/env.go` (BuildConstructPath)
  - `internal/templates/entrypoint.sh`
  - `internal/templates/docker-compose.yml`
  - `internal/templates/Dockerfile`
  - Runtime injects `PATH` and `CONSTRUCT_PATH` defensively for non-daemon runs, daemon exec sessions, and daemon startup.
  - entrypoint exports `CONSTRUCT_PATH` and writes `~/.construct-path.sh`, which is sourced by `~/.profile` for login shells.
- **Scripts**: `scripts/`
  - install.sh (curl-able installer to system bins), reset helpers, integration tests.
- **Agent Documentation**: `AGENTS.md`
  - Developer-focused guidance for working with the Construct CLI codebase
- **Config/Data layout** (`~/.config/construct-cli/`)
  - `config.toml` (runtime, sandbox, network, and claude provider configuration)
  - `packages.toml` (user-defined packages and tools)
  - `container/` (Dockerfile, compose, overrides, scripts)
  - `home/` (mounted home for agent configs/state)
  - `home/.config/topgrade.toml` (generated update configuration)
  - `agents-config/<agent>/` (host-side config mounts)
  - `logs/` (timestamped build/update logs)
  - `config.toml.backup` / `packages.toml.backup` (migration backups)
  - `.logs_cleanup_last_run` (log cleanup marker)
  - `cache/` (binary backups for self-update rollback)
  - `last-update-check` (timestamp for rate-limiting update checks)
  - `.version` (installed version for migrations)
  - `.packages_template_hash` / `.entrypoint_template_hash` (template tracking)
  - `home/.local/.entrypoint_hash` (entrypoint patch tracking)
  - `.login_bridge` (temporary login callback forwarding flag)
  - `home/.construct-path.sh` (construct-managed PATH exports for login shells)
- **Volumes**: `construct-packages` (persists Homebrew installs, npm globals, cargo, and caches). `construct-agents` is legacy and only referenced for cleanup in reset scripts.

---

## 4. Runtimes & Isolation
- **Runtime Detection**: The container runtime engine is determined by the `engine` setting in `config.toml` (e.g., `container`, `podman`, `docker`). If set to `auto` (the default), it checks for an active runtime by checking all runtimes **in parallel** (container, podman, docker) with a 500ms timeout, returning the first available one. If no runtime is active, it attempts to start one in the order of `container`, then `podman`, then `docker` (macOS launches OrbStack when available).
- **Parallel detection optimization**: Multiple runtimes are checked concurrently using goroutines and channels, reducing detection time from 500ms-1.5s (sequential) to ~50-500ms (parallel) when multiple runtimes are installed.
- **Linux specifics**:
  - Docker compose startup and daemon runs propagate host `CONSTRUCT_HOST_UID`/`CONSTRUCT_HOST_GID` on Linux to keep mounted home/config ownership aligned.
  - `exec_as_host_user=true` applies at exec-time only; if host UID is missing in container `/etc/passwd`, Construct logs a warning, keeps host UID:GID mapping, and forces `HOME=/home/construct`.
  - Config/migration ownership checks prompt for confirmation before repair; Linux remediation is runtime-aware (`podman unshare chown -R 0:0` in rootless/userns contexts where applicable, then sudo fallback), with explicit manual commands when repair is declined or fails.
  - `construct sys doctor --fix` can repair ownership/permissions, rebuild stale/missing images, clean stale session containers, and recreate daemon containers.
  - `construct sys doctor --fix` compose actions propagate runtime identity context (`CONSTRUCT_USERNS_REMAP`) to avoid immediate post-fix ownership drift on recreated daemon containers.
  - SELinux adds `:z` to mounts unless `sandbox.selinux_labels` disables it.
  - Podman rootless runs as the construct user by default.
- **macOS specifics**: Native `container` runtime supported on macOS 26+; Docker runs as root then drops to construct via gosu in entrypoint.
- **Mounts**:
  - Ephemeral runs: host project directory → `/projects/<folder_name>`.
  - Daemon runs with `daemon.multi_paths_enabled = true`: host paths mount under deterministic roots at `/workspaces/<hash>/...` and the runtime maps the current host `cwd` into that tree.
  - Host config/agents mount under `~/.config/construct-cli/agents-config/<agent>/` and `~/.config/construct-cli/home/`.
- **Isolation**: Each agent run is isolated within its container; only the active project/workspace mount (`/projects/...` or `/workspaces/...`) bridges host project files.
- **SSH agent forwarding**: Linux mounts the host socket directly; macOS uses a TCP bridge exposed to the container.
- **Network modes**:
  - `permissive`: full egress
  - `strict`: custom `construct-net` bridge + UFW rules; allow/block lists via env
  - `offline`: `network_mode: none`

---

## 5. Agents (installed in container)
- claude (Claude Code) - with configurable provider support
- gemini (Gemini CLI)
- amp (Amp CLI)
- qwen (Qwen Code)
- copilot (GitHub Copilot CLI)
- opencode
- cline
- codex (OpenAI Codex CLI)
- droid (Factory Droid CLI)
- goose (Block Goose CLI)
- kilocode (Kilo Code CLI)
- pi (Pi Coding Agent)

### 5.1 Claude Provider Aliases (CC System)
Construct supports configurable provider aliases for Claude Code, enabling seamless switching between different API endpoints:

- **Primary Usage**: `construct cc <provider> [args...]`
- **Fallback Usage**: `construct claude <provider> [args...]`
- **Configuration**: `[claude.cc.<provider>]` sections in config.toml
- **Environment Variables**: All use `CNSTR_<PROVIDER>_API_KEY` naming convention

**Supported Providers:**
- **Z.AI** (`zai`) - `CNSTR_ZAI_API_KEY`
- **MiniMax** (`minimax`) - `CNSTR_MINIMAX_API_KEY`
- **Kimi** (`kimi`) - `CNSTR_KIMI_API_KEY`
- **Qwen** (`qwen`) - `CNSTR_QWEN_API_KEY`
- **Mimo** (`mimo`) - `CNSTR_MIMO_API_KEY`

**Key Features:**
- **Environment Variable Expansion**: Supports `${VAR_NAME}` syntax referencing host environment
- **Auto-Reset**: Automatically cleans existing Claude environment variables before injection
- **Security**: Sensitive values masked in debug output
- **Isolation**: Provider-specific environment injection prevents conflicts

**Example Configuration:**
```toml
[claude.cc.zai]
ANTHROPIC_BASE_URL = "https://api.z.ai/api/anthropic"
ANTHROPIC_AUTH_TOKEN = "${CNSTR_ZAI_API_KEY}"
API_TIMEOUT_MS = "3000000"
```

---

## 6. Install / Update Behavior
- **First run** (`construct sys init`):
  - Writes embedded templates to `~/.config/construct-cli/`.
  - Builds `construct-box` image.
  - Generates `container/install_user_packages.sh` from `packages.toml` and installs tools on first container start (including post-install hooks).
  - Attempts to create `ct` symlink/alias (also triggered by `construct`, `construct sys`, and `construct sys help`).
  - Sets up Claude provider configuration template with `CNSTR_` prefixed environment variables.
- **Updates** (`sys update` → `templates/update-all.sh`):
  - apt update/upgrade (via topgrade or fallback).
  - `claude update` (fallback path when topgrade is missing).
  - Homebrew: `brew update/upgrade/cleanup` (all packages).
  - Topgrade: runs if installed with generated config; falls back to manual updates.
  - npm: `npm update -g` (all globals).
- **Update Check** (`sys check-update` / automatic):
  - Passively checks the configured channel marker file on a configurable interval (default 24h): `VERSION` for `runtime.update_channel="stable"` and `VERSION-BETA` for `runtime.update_channel="beta"`.
  - Actively checks when `construct sys check-update` is run.
  - If a new version is found, it notifies the user to run the (currently manual) update command.
- **Reset**: `sys reset` removes persistent volumes for a clean reinstall (including legacy `construct-agents`).
- **Migrate**: `sys config --migrate` refreshes templates, merges config, regenerates install scripts/topgrade config, and marks the image for rebuild.

---

## 7. Build & Test
```bash
make build           # build binary
go build -o construct

make test            # unit + integration
# includes a final combined summary (unit + integration + overall status)
make test-unit       # unit only
make test-integration# integration only
make lint            # gofmt + go vet + golangci-lint
make cross-compile   # all platforms
```

---

## 8. UX Notes
- Global flags: `-ct-v/--ct-verbose`, `-ct-d/--ct-debug`, `-ct-n/--ct-network`.
- Claude provider commands: `construct cc <provider>` and `construct cc --help`.
- `aliases --install`: One-step command to add `claude`, `gemini`, etc., and `cc-*` aliases to host shell.
- `aliases --update`: Reinstall/update host aliases if they already exist.
- `aliases --uninstall`: Remove Construct alias block from host shell.
- `sys check-update`: Manual command to check for new versions.
- `sys self-update`: Update the Construct binary itself.
- `sys doctor --fix`: Apply Linux-focused remediation for ownership/permission and stale runtime/image startup issues.
- `sys packages`: Open `packages.toml` for customization.
- `sys packages --install`: Regenerate/install user packages into the running container.
- `sys agents-md`: Manage global instruction files (rules) per agent.
- `sys config --migrate`: Refresh templates/config to match the running binary.
- `sys login-bridge`: Temporarily forward localhost login callbacks (ports default to 1455/8085).
- `sys config --restore`: Restore config from backup.
- `sys set-password`: Change the container user password (default is `construct`).
- `sys ssh-import`: Import SSH keys into `~/.config/construct-cli/home/.ssh` when no agent is used.
- `sys daemon install`: Install daemon as auto-start service (launchd on macOS, systemd on Linux).
- `sys daemon uninstall`: Remove daemon auto-start service.
- `sys daemon status`: Show daemon runtime + auto-start service status.
- `sys daemon start|stop|attach|status`: Keep a background container running for faster agent spins (~100ms startup vs 2-5s).
- **Daemon auto-start**: When `daemon.auto_start = true` (default), daemon automatically starts on first agent run if not already running.
- **System service integration**: Daemon can be installed as a user service that starts on login/boot; macOS uses `~/Library/LaunchAgents/`, Linux uses `~/.config/systemd/user/`.
- Long operations use gum spinners; logs go to `~/.config/construct-cli/logs/` (timestamped).
- Containers are ephemeral; volumes persist `/home/construct` installs/state.
- Environment management: Claude providers automatically reset environment variables to prevent conflicts.

---

## 9. Implementation Details
- Version string is defined in `internal/constants/constants.go` (`Version` constant) and must match the release tag.
- Homebrew auto-update disabled (`HOMEBREW_NO_AUTO_UPDATE=1`); updates are explicit.
- Network override file (`docker-compose.override.yml`) is generated per run for UID/GID, SELinux, and network mode.
- Error reporting via `ConstructError` with categories; doctor command aggregates checks.
- Migrations track installed version and template hashes, merge config files with backups, and regenerate topgrade config.
 - Yolo mode injects agent-specific flags (`--dangerously-skip-permissions`, `--allow-all-tools`, `--yolo`) based on `[agents]` config.
 - Login bridge stores ports in `.login_bridge`; entrypoint forwards ports via socat with an offset.
 - Homebrew installs can self-update by installing a user-local override binary in `~/.local/bin/construct`.
 - Release workflow updates marker files automatically: stable tags update `VERSION`, prerelease tags update `VERSION-BETA`.

### 9.1 Self-Update Implementation
Self-update checks the configured channel marker file (`VERSION` or `VERSION-BETA`), downloads the matching release tarball, replaces the binary atomically, and leaves a `.backup` binary for rollback on failure.

### 9.2 Claude Provider System Implementation
- **Config Schema**: `ClaudeConfig` struct with `Providers map[string]map[string]string` for TOML parsing
- **Command Routing**: `cc` command and `claude` wrapper with provider detection
- **Environment Management**:
  - `resetClaudeEnv()` filters Claude-related variables before injection
  - `expandProviderEnv()` handles `${VAR_NAME}` expansion with warnings for missing vars
  - `maskSensitiveValue()` protects sensitive data in debug output
- **Container Execution**: `runWithProviderEnv()` unified function with optional provider environment injection
- **Test Coverage**: Comprehensive unit tests for config parsing, variable expansion, and environment reset

### 9.3 Daemon Mode Implementation
Construct supports an optional daemon mode that keeps a background container running for instant agent execution (~100ms startup vs 2-5s for cold start).

**Daemon Container Architecture:**
- **Container name**: `construct-cli-daemon` (distinct from ephemeral `construct-cli` containers)
- **Image**: Shares the same `construct-box:latest` image
- **Lifecycle**: Managed via `daemon start|stop|attach|status` commands
- **Execution**: Uses `docker exec` instead of `docker-compose run` for instant startup

**Auto-Start Configuration:**
```toml
[daemon]
# Automatically start daemon on first agent run (default: true)
auto_start = true
```

**Multi-Root Daemon Mounts (Opt-In):**
```toml
[daemon]
# Enable multi-root daemon mounts (advanced)
multi_paths_enabled = false
# Host directories the daemon can serve
mount_paths = ["~/Dev/Projects", "/work/client-repos"]
```

**Multi-Root Behavior:**
- When enabled, the daemon mounts multiple host roots into deterministic container paths.
- Container mount destinations use `/workspaces/<hash>` (hash derived from each normalized host root path).
- Each run maps the host `cwd` to the first matching mount root; if none match, it falls back to the normal (non-daemon) path with a user-facing hint.
- Invalid/nonexistent mount paths are skipped with warnings; >32 entries warn and >64 are capped.
- Mount root overlaps are allowed but warned (first match wins).
- The compose override hash includes `multi_paths_enabled` and a normalized mount hash to keep mounts in sync.
- Daemon containers carry a mounts hash label to detect stale mount configurations.

**System Service Integration:**
- **macOS (launchd)**: Creates `~/Library/LaunchAgents/com.construct-cli.daemon.plist`
  - Label: `com.construct-cli.daemon`
  - RunAtLoad: true (starts on login)
  - KeepAlive: false (manual control)
  - Logs: `/tmp/construct-daemon.log` and `/tmp/construct-daemon.err`

- **Linux (systemd)**: Creates `~/.config/systemd/user/construct-daemon.service`
  - Type: oneshot with RemainAfterExit=yes
  - WantedBy: default.target (starts on login)
  - After: network.target docker.service podman.service
  - Logs via systemd journal

**Upgrade Safety:**
- Migration system stops and removes both `construct-cli` and `construct-cli-daemon` containers during template updates
- Staleness detection compares container image ID against current image; warns user if daemon is outdated
- User can manually restart daemon: `construct sys daemon stop && construct sys daemon start`

**Daemon Fallback Messaging:**
- When daemon mounts don’t include the current directory, show:
  - "Daemon workspace does not include the current directory; running without daemon."
  - "Tip: Enable multi-root daemon mounts in config for always-fast starts."

**Performance Impact:**
| Operation | Without Daemon | With Daemon |
|-----------|---------------|-------------|
| Agent startup | 2-5s | ~100ms |
| Subsequent runs | 2-5s each | ~100ms each |
| Memory overhead | 0 MB | ~100-200 MB (warm container) |

**Test Coverage:**
- Path generation tests for launchd plist and systemd unit files
- Binary path resolution tests
- Container state detection tests
- Environment variable setup tests (clipboard, COLORTERM, agent-specific)

### 9.4 Cross-Boundary Clipboard Architecture
Construct implements a secure "Host-Wrapper" bridge to enable rich media (images) and text pasting into isolated, headless containers.

- **Host-Side Resource Server**:
  - A secure Go HTTP server starts on a random port at launch.
  - Implements OS-native clipboard access: `pbpaste`/Cocoa (macOS), `wl-paste`/`xclip` (Linux), PowerShell/.NET (Windows).
  - Uses ephemeral authentication tokens injected into the container via environment variables (`CONSTRUCT_CLIPBOARD_TOKEN`).
- **In-Container Shimming**:
  - **Binary Interception**: System-wide shims for `xclip`, `xsel`, and `wl-paste` redirect all clipboard calls to the bridge script (`clipper`).
  - **Dependency Shimming**: `entrypoint.sh` recursively finds and shims bundled clipboard binaries inside `node_modules` (e.g., Gemini/Qwen's `clipboardy` dependency).
- **Tool Mocks**: A fake `osascript` shim allows macOS-centric agents to use their native "save image" logic while running on Linux; a fake `powershell.exe` enables Codex WSL clipboard fallback.
- **WSL Path Mapping for Codex**: The entrypoint creates `/mnt/c` aliases for container paths used by fallback image paste (`/projects`, `/workspaces`, and `/tmp`) so Windows-style paths returned by the shim resolve correctly inside the container.
- **Dynamic Content Bridging**:
  - **Image-First Behavior**: The bridge always attempts to fetch image data first; if present, it returns resized/normalized image bytes for most agents.
  - **Agent-Specific Paths**: Gemini, Qwen, and Codex receive an `@path` pointing to `.construct-clipboard/` instead of raw bytes; text is returned only when no image is available.
  - **Runtime Patching**: `entrypoint.sh` automatically patches agent source code at launch to bypass `process.platform !== 'darwin'` checks that would otherwise disable image support on Linux.

---

## 10. Future Notes / Risks
- Windows native binary deferred; WSL works via Docker/Podman detection.
- Network strict mode relies on UFW in-container; ensure compatibility with runtime/host firewall expectations.
- Claude provider system maintains backward compatibility but may need updates for new Claude API features.
- Environment variable naming convention (`CNSTR_` prefix) should be consistently applied to prevent conflicts.
- Self-update update to considers rollback command in future releases (user can manually restore from cache).

---

## 11. Startup Performance Analysis Summary

### 11.1 Current Timing Profile

| Phase | Fast Path | First Run | With Daemon |
|-------|-----------|-----------|-------------|
| CLI parsing + config | 50-150ms | 50-150ms | 50-150ms |
| Runtime detection | 50-500ms | 50-500ms | **50-500ms (parallel)** |
| Runtime.Prepare() | 200-800ms | 200-800ms | 200-800ms |
| Setup hash check | 50-200ms | 50-200ms | 50-200ms |
| Container collision check | 100-500ms | 100-500ms | 100-500ms |
| Env + services startup | 50-100ms | 50-100ms | 50-100ms |
| **docker-compose run** | **2-5s** | **2-5s** | **~100ms** (docker exec) |
| entrypoint.sh | 200-500ms | **30-120s** (package install) | **200-500ms (cached)** |
| Override generation | 50-100ms | 50-100ms | **Skip (cached)** |
| **Total** | **3.5-8s** | **40-130s** | **~0.8-1.2s** |

**Savings with optimizations:**
- **Daemon mode:** 2.5-7s per agent invocation when daemon is running
- **Parallel runtime detection:** 500ms-1s when multiple runtimes installed
- **Override caching:** 50-100ms per invocation after first run in same directory
- **Entrypoint caching:** 200-800ms per invocation when hash matches (most runs)

---

### 11.2 Optimization Routes (Backwards Compatible)

#### 11.2.1 Daemon Mode Enhancement (High Impact) ✅ IMPLEMENTED

**Current:** `daemon start|stop|attach|status` exists but underutilized.

**Opportunity:** Keep container warm in background. Agent execution becomes `docker exec` (~100ms) instead of `docker-compose run` (~3s).

```
Current:  ct gemini  →  compose run  →  entrypoint  →  gemini
Proposed: ct gemini  →  docker exec  →  gemini (container already running)
```

**Implementation:**
- Modify `runWithProviderEnv()` to detect running daemon container
- Use `runtime.ExecInContainer()` path (already exists for collision handling)
- Entrypoint already ran; skip entirely
- **Savings: 2.5-5.5s per invocation**

**Backwards compatible:** Daemon is opt-in; default behavior unchanged.

##### ⚠️ Prerequisites (Migration Gap)

**Problem:** `markImageForRebuild()` in `internal/migration/migration.go:592-654` only stops/removes `construct-cli` container. It does **NOT** handle `construct-cli-daemon`.

**Impact without fix:**
- User upgrades construct-cli binary
- Migration runs, updates templates, removes old image
- Daemon container keeps running with OLD entrypoint/code
- Daemon exec optimization would exec into stale container

**Required fixes before implementing daemon exec (choose one):**

**Option A: Fix migration to include daemon (Recommended)**

Location: `internal/migration/migration.go:592-654`

```go
func markImageForRebuild() {
    // Add daemon to container list
    containerNames := []string{"construct-cli", "construct-cli-daemon"}
    imageName := "construct-box:latest"

    for _, containerName := range containerNames {
        // Stop and remove each container...
    }
    // Remove image...
}
```

**Option B: Verify daemon freshness at exec time**

Location: `internal/agent/runner.go` (new code in daemon exec path)

```go
func isDaemonStale(containerRuntime, daemonName string) bool {
    // Compare daemon's image ID against current construct-box:latest
    // Or check entrypoint hash label on running container
}

// In runWithProviderEnv(), before exec:
if runtime.GetContainerState(containerRuntime, daemonName) == runtime.ContainerStateRunning {
    if isDaemonStale(containerRuntime, daemonName) {
        fmt.Println("⚠️  Daemon running outdated version. Restarting...")
        daemon.Stop()
        daemon.Start()
    }
    // Then exec...
}
```

**Option C: Prompt user on staleness detection**

```go
if isDaemonStale(containerRuntime, daemonName) {
    fmt.Println("⚠️  Daemon is running an outdated version.")
    fmt.Println("Run 'construct sys daemon stop && construct sys daemon start' to update.")
    // Fall through to regular compose run path
}
```

**Recommendation:** Implement Option A first (simple, covers all cases), then optionally add Option B as defense-in-depth.

##### Implementation Status

**Implemented in this version:**

1. **Migration fix (Option A):** `markImageForRebuild()` in `internal/migration/migration.go` now stops/removes both `construct-cli` and `construct-cli-daemon` containers during upgrades.

2. **Daemon staleness detection (Option B):** `IsContainerStale()` function in `internal/runtime/runtime.go` compares container's image ID against current image. If stale, warns user and falls back to normal startup path.

3. **Daemon exec path:** `execViaDaemon()` function in `internal/agent/runner.go` detects running daemon, verifies freshness, starts clipboard server, and uses `ExecInteractive()` for agent execution.

4. **Interactive exec:** `ExecInteractive()` function in `internal/runtime/runtime.go` provides stdin/stdout/stderr passthrough for interactive agent sessions.

**Files changed:**
- `internal/migration/migration.go` - Added daemon to container cleanup list
- `internal/runtime/runtime.go` - Added `ExecInteractive()`, `GetContainerImageID()`, `GetImageID()`, `IsContainerStale()`
- `internal/agent/runner.go` - Added `execViaDaemon()` and daemon detection in `runWithProviderEnv()`
- `internal/runtime/runtime_test.go` - Added tests for new functions

---

#### 11.2.2 Parallel Runtime Detection (Medium Impact) ✅ IMPLEMENTED

**Current:** `DetectRuntime()` checks runtimes sequentially with 500ms+ timeout each.

**Location:** `internal/runtime/runtime.go:34-72`

**Opportunity:** Check all runtimes in parallel, use first responder.

```go
// Current (sequential)
for _, rt := range runtimes {
    if IsRuntimeRunning(rt) { return rt }  // 500ms timeout each
}

// Proposed (parallel)
ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
results := make(chan string, 3)
for _, rt := range runtimes {
    go func(r string) {
        if IsRuntimeRunning(r) { results <- r }
    }(rt)
}
select {
case rt := <-results: return rt
case <-ctx.Done(): // try starting
}
```

**Savings: 500ms-1s** when multiple runtimes installed but only one running.

##### Implementation Status

**Implemented in this version:**

1. **Parallel detection:** `checkRuntimesParallel()` checks all runtimes (container, podman, docker) concurrently using goroutines.

2. **Context with timeout:** 500ms timeout ensures we don't wait too long; falls through to sequential startup if no runtime is ready.

3. **Channel-based results:** Buffered channel collects results; first positive result wins.

4. **Two-phase approach:**
   - First pass: Parallel check for already-running runtimes (fastest)
   - Second pass: Sequential startup attempt (keeps original behavior if none running)

5. **Backwards compatible:** No behavior changes for users with single runtime; only optimizes multi-runtime setups.

**Files changed:**
- `internal/runtime/runtime.go` - Modified `DetectRuntime()`, added `checkRuntimesParallel()`, added `context` import
- `internal/runtime/runtime_test.go` - Added 4 new tests for parallel detection

---

#### 11.2.3 Lazy Override Generation (Medium Impact) ✅ IMPLEMENTED

**Current:** `GenerateDockerComposeOverride()` runs every invocation, even if inputs unchanged.

**Location:** `internal/runtime/runtime.go:565-697`

**Operations:**
- SELinux check: `getenforce` (5-10ms)
- Git config queries: 2x `git config` (20-40ms)
- File write (5ms)

**Opportunity:** Cache override file; only regenerate if inputs change.

```go
type overrideInputs struct {
    Version       string  // Added binary version to cache key
    UID, GID      int
    SELinux       bool
    NetworkMode   string
    GitName       string
    GitEmail      string
    ProjectPath   string
    SSHAuthSock   string
    ForwardSSH    bool
    PropagateGit  bool
}

// Hash inputs, compare to stored hash
// Skip regeneration if unchanged
```

**Savings: 50-100ms per invocation** (after first run in same directory).

##### ⚠️ Caveat: Override Format Changes on Upgrade

**Problem:** If a new construct version adds/changes fields in `docker-compose.override.yml` but the input hash (UID, GID, etc.) hasn't changed, the optimization would skip regeneration and use the stale override file.

**Example scenario:**
1. Version 1.0 generates override with fields A, B, C
2. Version 1.1 adds field D to override
3. User upgrades, same UID/GID/network/etc.
4. Input hash matches → skip regeneration → field D missing

**Required fix (choose one):**

**Option A: Include version in hash inputs (Recommended)**

```go
type overrideInputs struct {
    Version     string  // ← Add binary version to cache key
    UID, GID    int
    SELinux     bool
    NetworkMode string
    GitName     string
    GitEmail    string
    ProjectPath string
}
```

**Option B: Clear hash file during migration**

Location: `internal/migration/migration.go` in `RunMigrations()`

```go
// Add after updateContainerTemplates()
clearOverrideHash()
```

**Option C: Include override template hash in inputs**

```go
type overrideInputs struct {
    TemplateHash string  // ← Hash of override generation code/template
    // ... other fields
}
```

**Recommendation:** Option A is simplest - any version change invalidates cache automatically.

##### Implementation Status

**Implemented in this version:**

1. **Hash-based caching (Option A):** `overrideInputs` struct includes `Version` field, so any binary version change automatically invalidates the cache.

2. **Comprehensive input tracking:** All inputs that affect override generation are tracked:
   - Version (binary version)
   - UID/GID (Linux user mapping)
   - SELinux status
   - Network mode
   - Git identity (name/email)
   - Project path
   - SSH agent socket
   - SSH forwarding enabled
   - Git propagation enabled

3. **Migration integration:** `clearOverrideHash()` in `internal/migration/migration.go` removes the hash during template updates, ensuring regeneration.

4. **Efficient hashing:** SHA256-based hash of all inputs in deterministic order, stored in `.override_hash` file.

5. **Skip logic:** `GenerateDockerComposeOverride()` checks hash at start, returns early if unchanged (50-100ms savings).

**Files changed:**
- `internal/runtime/runtime.go` - Added `overrideInputs` struct, `hashOverrideInputs()`, `readOverrideHash()`, `writeOverrideHash()`, modified `GenerateDockerComposeOverride()`
- `internal/runtime/runtime_test.go` - Added tests for hash functions
- `internal/migration/migration.go` - Added `clearOverrideHash()` and integration with migration flow

---

#### 11.2.4 Combined Image Inspection (Low Impact) - NOT APPLICABLE

**CURRENT IMPLEMENTATION ANALYSIS:**

After code review, this optimization is **not applicable** to the current architecture:

**What actually happens:**
1. **Image existence check** (`internal/agent/runner.go:452`):
   ```go
   checkCmdArgs := runtime.GetCheckImageCommand(containerRuntime)
   // Returns: docker/podman image inspect construct-box:latest
   ```
   Uses `docker image inspect` to check if image exists.

2. **Entrypoint hash retrieval** (`internal/agent/runner.go:247-271`):
   ```go
   cmd := exec.Command(runtimeCmd, "run", "--rm", "--entrypoint", "sha256sum", imageName, "/usr/local/bin/entrypoint.sh")
   ```
   **Runs a CONTAINER** to hash the entrypoint.sh file inside the image.

**Key finding:** The entrypoint hash is **NOT stored as a Docker image label**.

**How entrypoint hash actually works:**
- Computed at runtime by running a container
- Stored in file on host: `~/.config/construct-cli/home/.local/.entrypoint_hash`
- Compared by entrypoint.sh itself on container start
- NOT retrieved via `docker image inspect`

**To implement this "optimization" would require:**
1. **Major architectural change**: Store entrypoint hash as image label during build
2. **Migration complexity**: Update hash on every build/migration
3. **Code changes**: Modify hash retrieval to use image inspect instead of container run
4. **Build system changes**: Update Dockerfile to store hash as label

**Risk assessment:**
- Effort: **HIGH** (requires build system, migration, and runtime changes)
- Risk: **HIGH** (touches core build/publish process)
- Savings: **50-150ms** (minimal)

**Recommendation: SKIP THIS OPTIMIZATION**

The current architecture is reasonable:
- Image existence check: Fast metadata inspect (necessary)
- Entrypoint hash: Retrieved by running container (necessary, not easily optimizable)

**Status:** NOT APPLICABLE - Current design doesn't use two inspect calls

---

#### 11.2.5 Entrypoint Optimization (Medium Impact) ✅ IMPLEMENTED

**Current entrypoint.sh bottlenecks:**

| Operation | Time | Can Optimize? |
|-----------|------|---------------|
| Permission fixes (chown) | 100-500ms | Cache/skip if unchanged |
| PATH construction | 50-200ms | Pre-compute, cache |
| Clipboard lib patching | 100-500ms | Run async, cache results |
| Xvfb startup + wait | 100-2000ms | Start earlier, reduce wait |

**Opportunities:**

a) **Skip permission fixes if not needed:**
```bash
# Add marker file after successful fix
PERM_MARKER="$HOME/.local/.permissions_fixed"
if [ ! -f "$PERM_MARKER" ] || [ "$FORCE_PERMISSIONS" = "1" ]; then
    # do permission fixes
    touch "$PERM_MARKER"
fi
```

b) **Cache clipboard patching:**
```bash
CLIP_MARKER="$HOME/.local/.clipboard_patched_$(sha256sum /usr/local/bin/clipper | cut -d' ' -f1)"
if [ ! -f "$CLIP_MARKER" ]; then
    fix_clipboard_libs
    touch "$CLIP_MARKER"
fi
```

c) **Parallel Xvfb + clipboard bridge:**
```bash
# Start both in background, don't wait
Xvfb :0 &
clipboard-x11-sync.sh &
# Continue immediately
```

**Savings: 200-800ms per invocation**

##### ⚠️ Caveats: Marker File Persistence

**Problem 1: Markers persist across upgrades**

Markers are stored in persistent volume (`~/.config/construct-cli/home/.local/`). Migration clears `.entrypoint_hash` and sets `.force_entrypoint`, but does NOT clear optimization markers.

| Marker | Risk on Upgrade |
|--------|-----------------|
| `.permissions_fixed` | Skips permission fixes even if new version needs different ones |
| `.clipboard_patched_*` | Skips patching even if new packages installed or search paths changed |
| `.agent_code_patched` | Skips patching new agents' JS files |

**Problem 2: Package installation invalidation**

The proposed clipboard marker only tracks clipper hash:
```bash
CLIP_MARKER="$HOME/.local/.clipboard_patched_$(sha256sum /usr/local/bin/clipper | cut -d' ' -f1)"
```

If a NEW npm package is installed (new agent in packages.toml), its clipboardy dependency won't be shimmed because the marker still exists (clipper didn't change).

**Problem 3: Existing partial optimization**

Lines 312-313 of entrypoint.sh already skip re-shimming if symlink is correct:
```bash
if [ -L "$xsel_bin" ] && [[ "$(readlink "$xsel_bin")" == *"/clipper"* ]]; then
    : # Already shimmed
```

The expensive part is the `find` command searching node_modules, not the shimming itself.

**Required fixes (choose one):**

**Option A: Include multiple hashes in marker**
```bash
# Include clipper hash + packages hash + entrypoint version
CLIP_MARKER="$HOME/.local/.clipboard_patched_${CLIPPER_HASH}_${PACKAGES_HASH}_${ENTRYPOINT_VERSION}"
```

**Option B: Clear markers during migration**

Location: `internal/migration/migration.go`
```go
func clearEntrypointMarkers() {
    markers := []string{
        ".permissions_fixed",
        ".clipboard_patched_*",
        ".agent_code_patched",
    }
    localDir := filepath.Join(config.GetConfigDir(), "home", ".local")
    // Remove each marker...
}

// Call in RunMigrations() after clearEntrypointHash()
```

**Option C: Tie markers to setup hash (Recommended)**

Only skip operations if `.entrypoint_hash` matches current hash. Migration already clears this hash, so operations run on any version/package change.

```bash
HASH_FILE="$HOME/.local/.entrypoint_hash"
if [ -f "$HASH_FILE" ] && [ "$(cat "$HASH_FILE")" = "$CURRENT_HASH" ]; then
    # Skip expensive find operations - setup already ran with this exact config
    SKIP_PATCHING=1
fi

if [ -z "$SKIP_PATCHING" ]; then
    fix_clipboard_libs
    patch_agent_code
fi
```

**Recommendation:** Option C leverages existing hash mechanism. All patching operations run whenever setup is triggered (version change, package change, force flag), and are skipped only on unchanged subsequent runs.

##### Implementation Status

**Implemented in this version:**

1. **Hash-based caching (Option C):** Moved `fix_clipboard_libs()` and `patch_agent_code()` inside the hash-check block in entrypoint.sh. These expensive `find` operations now only run when `.entrypoint_hash` doesn't match current hash.

2. **Sys update integration:** Added hash clearing in `sys.UpdateAgents()` after package updates complete. This ensures newly installed agents get patched on next run.

3. **What's cached:**
   - Clipboard lib patching (finds clipboardy fallbacks in node_modules)
   - Agent code patching (finds and patches platform checks in JS files)
   - Both operations use expensive `find` commands that traverse large directory trees

4. **What still runs every time:**
   - Permission fixes (chown) - kept as-is for safety (fast enough)
   - PATH construction - minimal overhead
   - SSH bridge, Xvfb, login forwarders - as-needed services

**Files changed:**
- `internal/templates/entrypoint.sh` - Moved patching functions inside hash-check block
- `internal/sys/ops.go` - Added entrypoint hash clearing after `sys update`

**Safety:**
- Hash is cleared on: version changes (template hash), template updates (migration), and package updates (sys update)
- Patching happens automatically whenever agents are updated
- Backwards compatible - first run and updates work exactly as before

**Savings: 200-800ms per invocation** when entrypoint hash matches (most runs after first setup)

---

#### 11.2.6 Config Caching (Low Impact) - NOT RECOMMENDED

**REGRESSION ANALYSIS:**

**Current Implementation:**
- main.go already implements lazy caching: loads once at line 74, reuses via `if cfg == nil` checks
- config.Load() is called 8+ times in main.go, but only the first call actually loads from disk
- Subsequent calls reuse the already-loaded `cfg` variable

**Config Modification During Runtime:**
Config IS modified during runtime via network commands:
- `construct network allow <domain>` - AddRuleToConfig() → cfg.Save()
- `construct network block <domain>` - AddRuleToConfig() → cfg.Save()
- `construct network remove <domain>` - RemoveRuleFromConfig() → cfg.Save()
- `construct network clear` - ClearRules() → cfg.Save()

**Regression Risk with sync.Once Caching:**
1. **Stale config problem**: If config is cached globally with sync.Once:
   - Network rule changes write to file via cfg.Save()
   - But cached config in memory would remain stale
   - Other parts of application would have inconsistent config state

2. **Multiple config instances**:
   - internal/network/manager.go loads its own config instances
   - internal/agent/runner.go loads its own config instances
   - internal/daemon/daemon.go loads its own config instances
   - These would NOT use the global cache

3. **File vs memory inconsistency**:
   - File would have updated network rules
   - Cached config would have old rules
   - Application would behave inconsistently

**Actual Performance:**
The current lazy caching pattern (load once in main.go, reuse via nil checks) is already optimal for this use case. The 40-80ms estimate assumes every call re-reads from disk, but that's not what happens.

**Recommendation: SKIP THIS OPTIMIZATION**

**Status:** NOT RECOMMENDED - Current lazy caching is optimal; sync.Once would introduce regressions for minimal gain

---

#### 11.2.7 Preemptive Service Startup (Minimal Impact - Already Optimized)

**Current:** Clipboard server and SSH bridge appear to start sequentially, but internally both already use goroutines.

**Analysis:** Both `StartServer()` and `StartSSHBridge()` already:
1. Bind a port (< 5ms each)
2. Spawn a goroutine for actual serving
3. Return immediately

```go
// clipboard/server.go - already async
func StartServer(host string) (*Server, error) {
    // ... bind port ...
    go server.serve()  // ← Already non-blocking
    return server, nil
}

// agent/ssh_bridge.go - already async
func StartSSHBridge() (*SSHBridge, error) {
    // ... bind port ...
    go bridge.serve(sshAuthSock)  // ← Already non-blocking
    return bridge, nil
}
```

**Actual savings: ~10ms at best** (just port binding, which is already fast)

**Recommendation:** Deprioritize or skip. The proposed optimization would wrap already-fast calls in additional goroutines for negligible benefit. Focus effort on higher-impact optimizations.

---

#### 11.2.8 Reduced Entrypoint Hash Verification (Low Impact) - NOT RECOMMENDED

**REGRESSION ANALYSIS:**

**Current Dual Verification:**

**Host side** (`internal/agent/runner.go:170-236`):
```go
func ensureSetupComplete(cfg, containerRuntime, configPath) {
    // Calculate expected hash from embedded template
    h := sha256.Sum256([]byte(templates.Entrypoint))
    entrypointHash := hex.EncodeToString(h[:])

    // Add install_user_packages.sh hash if exists
    if scriptHash, err := getFileHash(userScriptPath); err == nil {
        expectedHash = fmt.Sprintf("%s-%s", entrypointHash, scriptHash)
    }

    // Compare with stored hash
    if currentHash, err := os.ReadFile(hashFile); err == nil {
        if actualHash == expectedHash {
            return nil // Already up to date
        }
    }

    // If mismatch, run setup
    return runSetup(cfg, containerRuntime, configPath)
}
```

**Container side** (`internal/templates/entrypoint.sh:186-202`):
```bash
# Calculate hash from actual files on container filesystem
CURRENT_HASH=$(sha256sum "$0" | awk '{print $1}')
if [ -f "$USER_INSTALL_SCRIPT" ]; then
    INSTALL_HASH=$(sha256sum "$USER_INSTALL_SCRIPT" | awk '{print $1}')
    CURRENT_HASH="${CURRENT_HASH}-${INSTALL_HASH}"
fi

# Compare with stored hash
if [ "$CURRENT_HASH" != "$PREVIOUS_HASH" ]; then
    # Run setup...
fi
```

**Key Difference:**
- **Host**: Uses embedded template hash (`templates.Entrypoint` - compiled into binary)
- **Container**: Uses actual file hash (`sha256sum "$0"` - reads from filesystem)

**Why Dual Verification Matters:**

1. **Template vs file mismatch protection**:
   - If entrypoint.sh on container filesystem is modified/corrupted
   - Host would calculate hash from embedded template
   - Container would calculate hash from actual file
   - Mismatch would trigger setup, ensuring consistency

2. **Container-only modifications**:
   - If someone manually modifies entrypoint.sh inside running container
   - Host verification would pass (using embedded template)
   - Container verification would fail (using modified file)
   - This forces setup to run, reverting modifications

3. **Build vs runtime consistency**:
   - If image is rebuilt with wrong entrypoint.sh
   - Host's embedded template hash wouldn't match container's file hash
   - Dual check catches this discrepancy

**Regression Risk:**
If we skip container hash check when host verification passes:
1. Manual modifications to container entrypoint.sh would go undetected
2. Corrupted container files wouldn't trigger recovery
3. Image build inconsistencies wouldn't be caught

**Actual Performance:**
The 10-20ms estimate is for sha256sum calculation. This is negligible compared to the 200-800ms savings from entrypoint caching (optimization #5).

**Recommendation: SKIP THIS OPTIMIZATION**

The dual verification is a safety mechanism. The hash calculation is fast (sha256sum is highly optimized). Removing one check saves 10-20ms but introduces risk of undetected container filesystem inconsistencies.

**Status:** NOT RECOMMENDED - Dual verification is a safety feature; savings are negligible

---

### 11.3 Priority Matrix

| Optimization | Impact | Effort | Risk | Status |
|--------------|--------|--------|------|--------|
| 1. Daemon mode exec | **High (2.5-7s)** | Medium | Low | ✅ **DONE** |
| 2. Parallel runtime detection | Medium (0.5-1s) | Low | Low | ✅ **DONE** |
| 3. Lazy override generation | Medium (50-100ms) | Medium | Low | ✅ **DONE** |
| 5. Entrypoint caching | Medium (200-800ms) | Low | Low | ✅ **DONE** |
| 4. Combined image inspect | Low (50-150ms) | High | High | ❌ NOT APPLICABLE |
| 6. Config caching | Low (40-80ms) | Low | **High** | ❌ **NOT RECOMMENDED** |
| 7. Preemptive services | Low (20-50ms) | Low | Low | Already Optimized |
| 8. Skip hash recheck | Low (10-20ms) | Low | **Medium** | ❌ **NOT RECOMMENDED** |

---

### 11.4 Recommended Implementation Order

**Completed:**
1. ✅ **Daemon mode exec path** - Biggest win; implemented with auto-start and system service integration (2.5-7s savings)
2. ✅ **Parallel runtime detection** - Concurrent runtime checks with goroutines and channels (0.5-1s savings)
3. ✅ **Lazy override generation** - Hash-based caching skips regeneration when inputs unchanged (50-100ms savings)
4. ✅ **Entrypoint caching** - Moved expensive clipboard/agent patching inside hash-check block (200-800ms savings)

**Not applicable (architectural mismatch):**
- ❌ **Combined image inspect** - Current design uses container run to retrieve entrypoint hash, not image inspect. Would require major architectural changes for minimal gain (see Section 11.2.6).

**Not recommended (regression risk):**
- ❌ **Config caching** - Current lazy caching pattern is optimal; sync.Once would introduce stale config issues when network rules are modified. Savings are minimal since config is already loaded once and reused (see Section 11.2.7).
- ❌ **Skip hash recheck** - Dual verification (host + container) is a safety mechanism. Skipping container hash check would allow undetected container filesystem inconsistencies. Savings (10-20ms) are negligible compared to risk (see Section 11.2.8).

**Current improvement:** 3.5-8s → **~0.8-1.2s** (with warm daemon + all implemented optimizations)

**Status:** **NO FURTHER PRACTICAL OPTIMIZATIONS REMAINING**

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
- **Self-Update**: Automatic checks against the published VERSION file; Homebrew installs defer to `brew upgrade`; manual installs use tarball update with backup/rollback.
- **Log maintenance**: Configurable cleanup of old log files under `~/.config/construct-cli/logs/`.
- **Daemon management**: Optional background daemon for instant agent execution with auto-start on login/boot via system services (launchd/systemd).
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
  - Self-update mechanism with VERSION check, release download, and atomic binary replacement (Homebrew installs defer to brew).
- **Templates**: `internal/templates/`
  - Dockerfile uses `debian:trixie-slim` + Homebrew (non-root) for tools; installs Chromium and Puppeteer-compatible deps; disables brew auto-update.
  - docker-compose.yml plus auto-generated override for OS/network specifics.
  - entrypoint installs tools on first run based on generated `install_user_packages.sh`, enforces PATH, shims clipboard tools, and starts login forwarders when enabled.
  - network-filter script for strict mode; update-all for maintenance.
  - clipper, clipboard-x11-sync.sh, osascript shim, and powershell.exe for clipboard bridging.
  - packages.toml template used to generate the install script (apt/brew/npm/pip/cargo/gems + post-install hooks).
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
  - `.config_template_hash` / `.packages_template_hash` / `.entrypoint_template_hash` (template tracking)
  - `home/.local/.entrypoint_hash` (entrypoint patch tracking)
  - `.login_bridge` (temporary login callback forwarding flag)
- **Volumes**: `construct-packages` (persists Homebrew installs, npm globals, cargo, and caches). `construct-agents` is legacy and only referenced for cleanup in reset scripts.

---

## 4. Runtimes & Isolation
- **Runtime Detection**: The container runtime engine is determined by the `engine` setting in `config.toml` (e.g., `container`, `podman`, `docker`). If set to `auto` (the default), it checks for an active runtime by checking all runtimes **in parallel** (container, podman, docker) with a 500ms timeout, returning the first available one. If no runtime is active, it attempts to start one in the order of `container`, then `podman`, then `docker` (macOS launches OrbStack when available).
- **Parallel detection optimization**: Multiple runtimes are checked concurrently using goroutines and channels, reducing detection time from 500ms-1.5s (sequential) to ~50-500ms (parallel) when multiple runtimes are installed.
- **Linux specifics**: UID/GID mapping; SELinux adds `:z` to mounts unless `sandbox.selinux_labels` disables it; Podman rootless runs as the construct user by default.
- **macOS specifics**: Native `container` runtime supported on macOS 26+; Docker runs as root then drops to construct via gosu in entrypoint.
- **Mounts**: host project directory → `/projects/<folder_name>`; host config/agents under `~/.config/construct-cli/agents-config/<agent>/` and `~/.config/construct-cli/home/`.
- **Isolation**: Each agent run is isolated within its container; the `/projects/<folder_name>` mount is the only bridge to host project files.
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
- **Update Check** (`sys update-check` / automatic):
  - Passively checks the published VERSION file on a configurable interval (default 24h).
  - Actively checks when `construct sys update-check` is run.
  - If a new version is found, it notifies the user to run the (currently manual) update command.
- **Reset**: `sys reset` removes persistent volumes for a clean reinstall (including legacy `construct-agents`).
- **Migrate**: `sys migrate` refreshes templates, merges config, regenerates install scripts/topgrade config, and marks the image for rebuild.

---

## 7. Build & Test
```bash
make build           # build binary
go build -o construct

make test            # unit + integration
make test-unit       # unit only
make test-integration# integration only
make lint            # format + go vet
make cross-compile   # all platforms
```

---

## 8. UX Notes
- Global flags: `-ct-v/--ct-verbose`, `-ct-d/--ct-debug`, `-ct-n/--ct-network`.
- Claude provider commands: `construct cc <provider>` and `construct cc --help`.
- `install-aliases`: One-step command to add `claude`, `gemini`, etc., and `cc-*` aliases to host shell.
- `update-aliases`: Reinstall/update host aliases if they already exist.
- `uninstall-aliases`: Remove Construct alias block from host shell.
- `sys update-check`: Manual command to check for new versions.
- `sys self-update`: Update the Construct binary itself.
- `sys packages`: Open `packages.toml` for customization.
- `sys install-packages`: Regenerate/install user packages into the running container.
- `sys agents-md`: Manage global instruction files (rules) per agent.
- `sys migrate`: Refresh templates/config to match the running binary.
- `sys login-bridge`: Temporarily forward localhost login callbacks (ports default to 1455/8085).
- `sys restore-config`: Restore config from backup.
- `sys set-password`: Change the container user password (default is `construct`).
- `sys ssh-import`: Import SSH keys into `~/.config/construct-cli/home/.ssh` when no agent is used.
- `sys daemon-install`: Install daemon as auto-start service (launchd on macOS, systemd on Linux).
- `sys daemon-uninstall`: Remove daemon auto-start service.
- `sys daemon-status`: Show daemon auto-start service status.
- `daemon start|stop|attach|status`: Keep a background container running for faster agent spins (~100ms startup vs 2-5s).
- **Daemon auto-start**: When `daemon.auto_start = true` (default), daemon automatically starts on first agent run if not already running.
- **System service integration**: Daemon can be installed as a user service that starts on login/boot; macOS uses `~/Library/LaunchAgents/`, Linux uses `~/.config/systemd/user/`.
- Long operations use gum spinners; logs go to `~/.config/construct-cli/logs/` (timestamped).
- Containers are ephemeral; volumes persist `/home/construct` installs/state.
- Environment management: Claude providers automatically reset environment variables to prevent conflicts.

---

## 9. Implementation Details
- Version string: `Version = "1.0.1"` in `internal/constants/constants.go`.
- Homebrew auto-update disabled (`HOMEBREW_NO_AUTO_UPDATE=1`); updates are explicit.
- Network override file (`docker-compose.override.yml`) is generated per run for UID/GID, SELinux, and network mode.
- Error reporting via `ConstructError` with categories; doctor command aggregates checks.
- Migrations track installed version and template hashes, merge config files with backups, and regenerate topgrade config.
 - Yolo mode injects agent-specific flags (`--dangerously-skip-permissions`, `--allow-all-tools`, `--yolo`) based on `[agents]` config.
 - Login bridge stores ports in `.login_bridge`; entrypoint forwards ports via socat with an offset.
 - Homebrew installs skip self-update in favor of `brew upgrade`.

### 9.1 Self-Update Implementation
Self-update checks the published VERSION file, downloads the matching release tarball, replaces the binary atomically, and leaves a `.backup` binary for rollback on failure.

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
- User can manually restart daemon: `construct daemon stop && construct daemon start`

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

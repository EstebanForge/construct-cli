# Construct CLI – Design Notes
**Tagline:** The Secure Loading Program for AI Agents.

---

## 1. Executive Summary
Construct CLI is a single-binary tool that launches an isolated, ephemeral container preloaded with AI agents. It embeds templates (Dockerfile, compose, entrypoint, network/clipboard shims), writes them to `~/.config/construct-cli/` on first run, builds the image, and installs tools via a generated `packages.toml`-driven script into persistent volumes. Its runtime engine is configurable (auto-detecting `container`, `podman`, or `docker`), and migrations keep templates/config aligned across versions. Network isolation supports `permissive`, `strict`, and `offline` modes with allow/block lists, plus a clipboard bridge for text/image paste and optional login callback forwarding.

---

## 2. Goals & Requirements
- **Zero-trace host**: Containers run with `--rm`; only named volumes persist installs/state.
- **Flexible runtime support**: User-configurable engine with auto-detection and auto-start capabilities.
- **Fast subsequent runs**: Agents and tools live in a persistent named volume (`construct-packages`).
- **Network control**: Allow/deny lists with `permissive/strict/offline`; strict mode creates a custom bridge network.
- **Single config**: TOML at `~/.config/construct-cli/config.toml` with `[runtime]`, `[sandbox]`, `[network]`, `[maintenance]`, `[claude]`.
- **Packages customization**: `packages.toml` drives tool installs and topgrade config generation.
- **Clear UX**: Gum-based prompts/spinners; `--ct-*` global flags avoid agent conflicts; `ct` symlink creation is attempted on basic help/sys invocations.
- **Flexible Claude Integration**: Configurable provider aliases for Claude Code with secure environment management.
- **Safe upgrades**: Versioned migrations refresh templates and merge config with backups.
- **Self-Update**: Automatic checks against the published VERSION file; self-update downloads release tarballs with backup/rollback.
- **Pro Toolchain**: Preloaded with utilities including `url-to-markdown-cli-tool`, `ripgrep`, `bat`, `fzf`, `eza`, `zoxide`, `tree`, `httpie`, `gh`, `neovim`, `uv`, `prettier`, programming languages (Go, Rust, Python, Node.js, Java, PHP, Swift, Zig, Kotlin, Lua, Ruby, Dart, Perl, Erlang), cloud tools (AWS CLI, Terraform), and development utilities (git-delta, git-cliff, shellcheck, yamllint, webpack, vite, and many more).

---

## 3. Architecture
- **Entrypoint**: `cmd/construct/main.go`
  - Embeds templates (Dockerfile, docker-compose.yml, entrypoint.sh, update-all.sh, network-filter.sh, config.toml, packages.toml, and clipboard shims).
  - Handles namespaces: `sys`, `network`, `daemon`, `cc`, and agent passthrough.
  - Migration check before config load; log cleanup; passive update check.
  - Runtime detection/startup, build, update, reset, doctor, and agent execution.
  - Claude provider system for configurable API endpoints with environment variable management.
  - Self-update mechanism with VERSION check, release download, and atomic binary replacement.
- **Templates**: `internal/templates/`
  - Dockerfile uses `debian:trixie-slim` + Homebrew (non-root) for tools; disables brew auto-update.
  - docker-compose.yml plus auto-generated override for OS/network specifics.
  - entrypoint installs tools on first run based on generated `install_user_packages.sh`.
  - network-filter script for strict mode; update-all for maintenance.
  - clipper, clipboard-x11-sync.sh, osascript shim, and powershell.exe for clipboard bridging.
  - packages.toml template used to generate the install script.
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
  - `cache/` (binary backups for self-update rollback)
  - `last-update-check` (timestamp for rate-limiting update checks)
  - `.version` (installed version for migrations)
  - `.config_template_hash` / `.packages_template_hash` / `.entrypoint_template_hash` (template tracking)
  - `.login_bridge` (temporary login callback forwarding flag)
- **Volumes**: `construct-packages` (persists Homebrew installs, packages, and caches).

---

## 4. Runtimes & Isolation
- **Runtime Detection**: The container runtime engine is determined by the `engine` setting in `config.toml` (e.g., `container`, `podman`, `docker`). If set to `auto` (the default), it checks for an active runtime in the order of `container`, then `podman`, then `docker`. If no runtime is active, it attempts to start one in the same order.
- **Linux specifics**: UID/GID mapping; SELinux adds `:z` to mounts.
- **Mounts**: host project directory → `/projects/<folder_name>`; host config/agents under `~/.config/construct-cli/agents-config/<agent>/` and `~/.config/construct-cli/home/`.
- **Isolation**: Each agent run is isolated within its container; the `/projects/<folder_name>` mount is the only bridge to host project files.
- **Network modes**:
  - `permissive`: full egress
  - `strict`: custom `construct-net` bridge + UFW rules; allow/block lists via env
  - `offline`: `network_mode: none`

---

## 5. Agents (installed in container)
- claude (Claude Code) - with configurable provider support
- gemini (Gemini CLI)
- qwen (Qwen Code)
- copilot (GitHub Copilot CLI)
- opencode
- cline
- codex (OpenAI Codex CLI)

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
  - Generates `container/install_user_packages.sh` from `packages.toml` and installs tools on first container start.
  - Attempts to create `ct` symlink/alias (also triggered by `construct`, `construct sys`, and `construct sys help`).
  - Sets up Claude provider configuration template with `CNSTR_` prefixed environment variables.
- **Updates** (`sys update` → `templates/update-all.sh`):
  - apt update/upgrade.
  - claude installer via curl.
  - mcp-cli-ent installer via curl.
  - Homebrew: `brew update/upgrade/cleanup` (all packages).
  - Topgrade: runs if installed with generated config; falls back to manual updates.
  - npm: `npm update -g` (all globals).
- **Update Check** (`sys update-check` / automatic):
  - Passively checks the published VERSION file on a configurable interval (default 24h).
  - Actively checks when `construct sys update-check` is run.
  - If a new version is found, it notifies the user to run the (currently manual) update command.
- **Reset**: `sys reset` removes volumes for a clean reinstall.
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
- `sys update-check`: Manual command to check for new versions.
- `sys self-update`: Update the Construct binary itself.
- `sys packages`: Open `packages.toml` for customization.
- `sys install-packages`: Regenerate/install user packages into the running container.
- `sys migrate`: Refresh templates/config to match the running binary.
- `sys login-bridge`: Temporarily forward localhost login callbacks (ports default to 1455/8085).
- `daemon start|stop|attach|status`: Keep a background container running for faster agent spins.
- Long operations use gum spinners; logs go to `~/.config/construct-cli/logs/` (timestamped).
- Containers are ephemeral; volumes persist `/home/construct` installs/state.
- Environment management: Claude providers automatically reset environment variables to prevent conflicts.

---

## 9. Implementation Details
- Version string: `Version = "0.11.2"` in `internal/constants/constants.go`.
- Homebrew auto-update disabled (`HOMEBREW_NO_AUTO_UPDATE=1`); updates are explicit.
- Network override file (`docker-compose.override.yml`) is generated per run for UID/GID, SELinux, and network mode.
- Error reporting via `ConstructError` with categories; doctor command aggregates checks.
- Migrations track installed version and template hashes, merge config files with backups, and regenerate topgrade config.

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

### 9.3 Cross-Boundary Clipboard Architecture
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

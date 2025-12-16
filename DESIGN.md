# Construct CLI – Design Notes
**Tagline:** The Secure Loading Program for AI Agents.

---

## 1. Executive Summary
Construct CLI is a single-binary tool that launches an isolated, ephemeral container preloaded with AI agents. It embeds its own Dockerfile, docker-compose, and helper scripts, writes them to `~/.config/construct-cli/` on first run, builds the image, and installs agents into persistent volumes. Runtime detection prefers macOS `container`, then `podman`, then `docker` (OrbStack compatible). Network isolation supports `permissive`, `strict`, and `offline` modes with allow/block lists.

---

## 2. Goals & Requirements
- **Zero-trace host**: Containers run with `--rm`; only named volumes persist installs/state.
- **One binary, all runtimes**: Auto-detect `container` → `podman` → `docker`.
- **Fast subsequent runs**: Agents and tools live in volumes (`construct-agents`, `construct-packages`).
- **Network control**: Allow/deny lists with `permissive/strict/offline`; strict mode creates a custom bridge network.
- **Single config**: TOML at `~/.config/construct-cli/config.toml` with `[runtime]`, `[sandbox]`, `[network]`, `[claude]`.
- **Clear UX**: Gum-based prompts/spinners; `--ct-*` global flags avoid agent conflicts.
- **Flexible Claude Integration**: Configurable provider aliases for Claude Code with secure environment management.
- **Self-Update**: Automatic checks and secure self-updates from GitHub releases with SHA256 verification.

---

## 3. Architecture
- **Entrypoint**: `main.go`
  - Embeds templates (Dockerfile, docker-compose.yml, entrypoint.sh, update-all.sh, network-filter.sh, config.toml).
  - Handles namespaces: `sys`, `network`, `daemon`, `cc`, and agent passthrough.
  - Runtime detection/startup, build, update, reset, doctor, and agent execution.
  - Claude provider system for configurable API endpoints with environment variable management.
  - Self-update mechanism with GitHub Releases API, fallback to VERSION file, SHA256 verification, and atomic binary replacement.
- **Templates**: `templates/`
  - Dockerfile uses Debian slim + Homebrew (non-root) for tools; disables brew auto-update.
  - docker-compose.yml plus auto-generated override for OS/network specifics.
  - entrypoint installs agents/tools on first run; network filter script for strict mode; update-all for maintenance.
- **Scripts**: `scripts/`
  - install.sh (curl-able installer to system bins), reset helpers, integration tests.
- **Config/Data layout** (`~/.config/construct-cli/`)
  - `config.toml` (runtime, sandbox, network, and claude provider configuration)
  - `container/` (Dockerfile, compose, overrides, scripts)
  - `home/` (mounted home for agent configs/state)
  - `agents-config/<agent>/` (host-side config mounts)
  - `logs/` (timestamped build/update logs)
  - `cache/` (binary backups for self-update rollback)
  - `last-update-check` (timestamp for rate-limiting update checks)
- **Volumes**: `construct-agents`, `construct-packages` (persist installs/caches).

---

## 4. Runtimes & Isolation
- **Detection order**: `container` (macOS Tahoe) → `podman` → `docker`.
- **Linux specifics**: UID/GID mapping; SELinux adds `:z` to mounts.
- **Mounts**: current workdir → `/app`; host config/agents under `~/.config/construct-cli/agents-config/<agent>/` and `~/.config/construct-cli/home/`.
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
  - Installs agents/tools into volumes via `entrypoint.sh` on first container start.
  - Attempts to create `ct` symlink/alias.
  - Sets up Claude provider configuration template with `CNSTR_` prefixed environment variables.
- **Updates** (`sys update` → `templates/update-all.sh`):
  - apt update/upgrade.
  - claude installer via curl.
  - mcp-cli-ent installer via curl.
  - Homebrew: `brew update/upgrade/cleanup` (all packages).
  - npm: `npm update -g` (all globals).
- **Self-Update** (`sys self-update` / `sys update-check`):
  - Passive: Automatic background checks (configurable, default 24h interval)
  - Active: Explicit `sys update-check` and `sys self-update` commands
  - Downloads platform-specific binaries from GitHub releases
  - SHA256 checksum verification against checksums.txt
  - Atomic binary replacement with backup to `~/.config/construct-cli/cache/`
  - Smart permission handling (tries without sudo first)
  - Fallback to raw VERSION file when GitHub API unavailable
- **Reset**: `sys reset` removes volumes for a clean reinstall.

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
- `sys self-update`: Binary self-update with gum prompts, progress indicators, and permission handling.
- `sys update-check`: Manual update checking with clear version comparison.
- Long operations use gum spinners; logs go to `~/.config/construct-cli/logs/` (timestamped).
- Containers are ephemeral; volumes persist `/home/construct` installs/state.
- Environment management: Claude providers automatically reset environment variables to prevent conflicts.

---

## 9. Implementation Details
- Version string: `version = "0.3.0"` in `main.go`.
- Homebrew auto-update disabled (`HOMEBREW_NO_AUTO_UPDATE=1`); updates are explicit.
- Network override file (`docker-compose.override.yml`) is generated per run for UID/GID, SELinux, and network mode.
- Error reporting via `ConstructError` with categories; doctor command aggregates checks.

### 9.1 Self-Update Implementation
- **Version Comparison**: Semantic versioning with padding for incomplete versions
- **Update Sources**: GitHub Releases API (primary) → Raw VERSION file (fallback)
- **Rate Limiting**: Timestamp-based checks with configurable interval (default 24h)
- **Security**: SHA256 checksum verification against checksums.txt before installation
- **Platform Detection**: Auto-detects OS (darwin/linux) and arch (amd64/arm64) with validation
- **Download Flow**: Platform-specific tar.gz download → checksum verification → extraction → binary replacement
- **Permission Handling**: Tries update without sudo first; provides helpful error with instructions
- **Atomic Replacement**: Uses `os.Rename()` for atomic binary swap with automatic backup
- **Verification**: Post-install verification that new binary works correctly
- **Config Integration**: `auto_update_check` and `update_check_interval` in `[runtime]` section

### 9.2 Claude Provider System Implementation
- **Config Schema**: `ClaudeConfig` struct with `Providers map[string]map[string]string` for TOML parsing
- **Command Routing**: `cc` command and `claude` wrapper with provider detection
- **Environment Management**:
  - `resetClaudeEnv()` filters Claude-related variables before injection
  - `expandProviderEnv()` handles `${VAR_NAME}` expansion with warnings for missing vars
  - `maskSensitiveValue()` protects sensitive data in debug output
- **Container Execution**: `runWithProviderEnv()` unified function with optional provider environment injection
- **Test Coverage**: Comprehensive unit tests for config parsing, variable expansion, and environment reset

---

## 10. Future Notes / Risks
- Windows native binary deferred; WSL works via Docker/Podman detection.
- Network strict mode relies on UFW in-container; ensure compatibility with runtime/host firewall expectations.
- Claude provider system maintains backward compatibility but may need updates for new Claude API features.
- Environment variable naming convention (`CNSTR_` prefix) should be consistently applied to prevent conflicts.
- Self-update considers rollback command in future releases (user can manually restore from cache).

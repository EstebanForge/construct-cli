# Construct CLI

**Construct** is a single-binary CLI that boots a clean and isolated container, preloaded with AI agents. It keeps your host free of dependency sprawl, adds optional network isolation, and works with Docker, Podman, or macOS native container runtime.

But, **most importantly**, it keeps your local machine safe from LLM prompt injection attacks, malware distributed this way, credentials stolen this way, and dangerous derps still being committed by AGENTS that can leave you [without any of your files](https://www.reddit.com/r/ClaudeAI/comments/1pgxckk/claude_cli_deleted_my_entire_home_directory_wiped/).

## Highlights
- One command to use any AGENT inside a secured, isolated sandbox. Agents spawn from the path where you call them, without a path escape.
- Self-building: embedded Dockerfile/compose/config templates are written on first run, then built automatically. First `construct sys init` will build the containers and install the agents. Subsequent uses will be instant.
- Runtime auto-detection: if macOS is detected and `container` exists, it will use it. Then, on Linux, WSL, and macOS, `podman`, then `docker` (compatible with OrbStack).
- Persistent volumes for agent installs, but ephemeral containers so your host stays clean.
- Your agents’ configuration lives outside of the containers, so you never lose them.
- Optional network isolation (`permissive`, `strict`, `offline`) with allow/block lists. Configurable list of domains and IPs to blacklist and/or whitelist.
- Live application of network rules while the AGENT is running.

## Available AGENTS

- **Claude Code** (`claude`) – Full-code agent with strong editing/refactoring.
- **Gemini CLI** (`gemini`) – Google Gemini models with CLI UX.
- **Qwen Code** (`qwen`) – Alibaba Qwen models tuned for coding.
- **GitHub Copilot CLI** (`copilot`) – GitHub Copilot with terminal helpers.
- **OpenCode** (`opencode`) – General code assistant.
- **Cline** (`cline`) – Agentic workflow helper.
- **OpenAI Codex** (`codex`) – Codex-style code generation.
- **Claude Code** with several other 3rd party providers with Anthropic compatible API: Zai GLM, MiniMax M2, Kimi K2, Qwen, Mimo.

## Install & Quick Start
Prerequisites: a container runtime (Docker Desktop/OrbStack, Podman, or macOS 26+ with native containers).

**Install via script (recommended):**

```bash
curl -fsSL https://raw.githubusercontent.com/EstebanForge/construct-cli/main/scripts/install.sh | bash
```

Tip: `construct sys init` will create a `ct` symlink/alias when possible, so `ct` works as a shortcut for `construct`.

## Common Examples
```bash
# Run with strict network isolation (allowlist only)
ct claude -ct-n strict

# Offline run (no network at all)
ct gemini --ct-network offline

# Update all agents in the persistent volume
ct sys update

# Install host aliases for seamless agent access (claude, gemini, etc.)
ct sys install-aliases

# Rebuild everything from scratch (cleans volumes, reinstalls)
ct sys reset

# Open user config in your terminal editor
ct sys config

# Use Claude with different providers (after configuration)
ct cc zai "Debug this API"
ct cc minimax --resume session_123
ct claude kimi "Refactor this code"  # Fallback syntax also works
```

## Updating Construct

Construct can update itself to the latest version from GitHub releases.

### Check for updates
```bash
construct sys update-check
```

### Update to latest version
```bash
construct sys self-update
```

### Automatic update checks
Construct automatically checks for updates once per day (configurable in `config.toml`):

```toml
[runtime]
auto_update_check = true          # Enable/disable automatic checks
update_check_interval = 86400     # Check interval in seconds (24 hours)
```

When an update is available, you'll see a notification like:
```
ℹ Update available: 0.3.0 → 0.4.0 (run 'construct sys self-update')
```

## Configuration
Main configuration lives at `~/.config/construct-cli/config.toml`. Key sections are:

```toml
[runtime]
# auto | container | podman | docker
engine = "auto"
auto_update_check = true
update_check_interval = 86400  # seconds (24 hours)

[sandbox]
mount_home = false # keep false unless you really need your whole home dir (dangerous)
shell = "/bin/bash"

[network]
# permissive | strict | offline
mode = "permissive" # by default, it has unrestricted network access
allowed_domains = [
  "*.anthropic.com",
  "*.openai.com",
  "api.googleapis.com",
  "huggingface.co",
  "*.github.com"
]
allowed_ips = ["1.1.1.1/32", "8.8.8.8/32"]
blocked_domains = [
  "*.example-malware.com",
  "*.crypto-miner.net"
]
blocked_ips = ["203.0.113.0/24", "198.51.100.25"]
```

Agent and sandbox config directories on the host live inside `~/.config/construct-cli/home`.

## Architecture (What Happens Under the Hood)
- **Embedded templates**: Dockerfile, docker-compose.yml, entrypoint, network filter, and default config are bundled inside the binary and written to `~/.config/construct-cli/container` on first `construct sys init`.
- **Runtime prep**: `detectRuntime` chooses `container` → `podman` → `docker`, starting the runtime if needed. SELinux labels and Linux UID/GID mappings are applied in a generated `docker-compose.override.yml`.
- **Image build + agents**: If the `construct-box` image or agent marker is missing, `construct sys init` builds the image and installs agents into named volumes (`construct-agents`, `construct-packages`). First init will be slow. Subsequent runs will be instant.
- **Execution**: The current directory where the AGENT is called mounts to `/app` inside the sandbox. Optional network mode is injected via env vars. Containers use `--rm` so each session is fresh while volumes persist tools.

## Security Expectations
- Containers are ephemeral; named volumes persist Homebrew installs and `/home/construct` state (logs/config you write there stick around), while the rest of the container filesystem is wiped on exit.
- `strict` and `offline` modes reduce network exposure; `permissive` allows full egress.
- Do not mount sensitive host paths you do not want an agent to access.

## Claude Code with other Providers (CC)

Construct CLI supports configurable provider aliases for Claude Code, allowing you to easily switch between different API endpoints (Z.AI, MiniMax, Kimi, etc.) with custom authentication and model settings.

### Quick Setup

1. **Configure your provider** in `~/.config/construct-cli/config.toml`:

```toml
[claude.cc.zai]
ANTHROPIC_BASE_URL = "https://api.z.ai/api/anthropic"
ANTHROPIC_AUTH_TOKEN = "${CNSTR_ZAI_API_KEY}"
API_TIMEOUT_MS = "3000000"

[claude.cc.minimax]
ANTHROPIC_BASE_URL = "https://api.minimax.io/anthropic"
ANTHROPIC_AUTH_TOKEN = "${CNSTR_MINIMAX_API_KEY}"
ANTHROPIC_MODEL = "MiniMax-M2"
```

2. (Optional) **Set your environment variables** on the host, if you don't want to type your keys in the Construct configuration file:

```bash
export CNSTR_ZAI_API_KEY="sk-z-..."
export CNSTR_MINIMAX_API_KEY="sk-mm-..."
```

3. **Use your configured providers**:

```bash
# Primary usage
ct cc zai
ct cc minimax --resume

# Fallback wrapper (also works)
ct claude zai "Refactor this code"
ct claude minimax --help

# List configured providers
ct cc --help
```

### Supported Providers

| Provider    | API Endpoint                                                 | Environment Variable        | Notes                     |
| ----------- | ------------------------------------------------------------ | --------------------------- | ------------------------- |
| **Z.AI**    | `https://api.z.ai/api/anthropic`                             | `CNSTR_ZAI_API_KEY`     | Most popular alternative  |
| **MiniMax** | `https://api.minimax.io/anthropic`                           | `CNSTR_MINIMAX_API_KEY` | Includes MiniMax-M2 model |
| **Kimi**    | `https://api.moonshot.ai/anthropic`                          | `CNSTR_KIMI_API_KEY`    | Moonshot AI integration   |
| **Qwen**    | `https://dashscope-intl.aliyuncs.com/api/v2/apps/claude-code-proxy` | `CNSTR_QWEN_API_KEY`    | Alibaba Qwen support      |
| **Mimo**    | `https://api.xiaomimimo.com/anthropic`                       | `CNSTR_MIMO_API_KEY`    | Xiaomi's AI service       |

### Full Configuration Examples

```toml
# Z.AI GLM Provider
[claude.cc.zai]
ANTHROPIC_BASE_URL = "https://api.z.ai/api/anthropic"
ANTHROPIC_AUTH_TOKEN = "${CNSTR_ZAI_API_KEY}"
API_TIMEOUT_MS = "3000000"
CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC = "1"

# MiniMax M2 Provider
[claude.cc.minimax]
ANTHROPIC_BASE_URL = "https://api.minimax.io/anthropic"
ANTHROPIC_AUTH_TOKEN = "${CNSTR_MINIMAX_API_KEY}"
API_TIMEOUT_MS = "3000000"
ANTHROPIC_MODEL = "MiniMax-M2"
ANTHROPIC_SMALL_FAST_MODEL = "MiniMax-M2"
ANTHROPIC_DEFAULT_SONNET_MODEL = "MiniMax-M2"
ANTHROPIC_DEFAULT_OPUS_MODEL = "MiniMax-M2"
ANTHROPIC_DEFAULT_HAIKU_MODEL = "MiniMax-M2"
CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC = "1"

# Moonshot Kimi K2 Provider
[claude.cc.kimi]
ANTHROPIC_BASE_URL = "https://api.moonshot.ai/anthropic"
ANTHROPIC_AUTH_TOKEN = "${CNSTR_KIMI_API_KEY}"
API_TIMEOUT_MS = "3000000"
CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC = "1"

# Alibaba Qwen Provider
[claude.cc.qwen]
ANTHROPIC_BASE_URL = "https://dashscope-intl.aliyuncs.com/api/v2/apps/claude-code-proxy"
ANTHROPIC_AUTH_TOKEN = "${CNSTR_QWEN_API_KEY}"
API_TIMEOUT_MS = "3000000"
CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC = "1"

# Xiaomi Mimo Provider
[claude.cc.mimo]
ANTHROPIC_BASE_URL = "https://api.xiaomimimo.com/anthropic"
ANTHROPIC_AUTH_TOKEN = "${CNSTR_MIMO_API_KEY}"
API_TIMEOUT_MS = "3000000"
```

### Environment Variables

- **Reference Host Variables**: Use `${VAR_NAME}` syntax to reference environment variables from your host system
- **Direct Values**: You can also specify values directly. Maybe less secure for API keys. Your call
- **Auto-Reset**: Construct automatically cleans any existing Claude environment variables before injecting provider-specific ones

### Benefits

- **Clean Switching**: No conflicts between providers - environment is automatically reset
- **Secure**: API keys referenced from host environment, not stored in config (if you don't want to)
- **Flexible**: Support for any Claude-compatible API endpoint

## Troubleshooting
- **"No container runtime found"**: Install Docker Desktop/OrbStack on macOS, Podman/Docker on Linux. Ensure macOS is 26+ for the native runtime use.
- **Build is slow**: First sys init install can take several minutes; check logs under `~/.config/construct-cli/logs/`. Be patient.
- **SELinux volume issues (Linux)**: `docker-compose.override.yml` adds `:z` automatically; if problems persist, verify SELinux policies or temporarily relax them during testing.

## Security & Build Integrity

Construct-CLI uses automated, reproducible builds through GitHub Actions:

- **CI/CD Pipeline**: All releases built automatically via GitHub Actions
- **No Manual Builds**: Prevents tampering by never building locally
- **Reproducible Builds**: Every build traceable to source commits
- **Automated Testing**: Each build passes comprehensive tests
- **Cryptographic Verification**: Release artifacts include SHA256 checksums

### Verify Downloads

```bash
# Verify checksum (provided in release notes)
sha256sum construct
```

**Trust, but verify.** Always download from official GitHub releases and verify checksums.

## Contributing
Issues and PRs are welcome. See `CONTRIBUTING.md` for guidelines.

## License
MIT – see `LICENSE.md`.

# Made...

With (L) for my kids. Go wild!

# Configuration Guide

Complete reference for configuring The Construct CLI via `config.toml`.

## Table of Contents

- [Configuration Location](#configuration-location)
- [Config File Structure](#config-file-structure)
- [Runtime Settings](#runtime-settings)
- [Sandbox Settings](#sandbox-settings)
- [Network Settings](#network-settings)
- [Security Settings](#security-settings)
- [Agent Settings](#agent-settings)
- [Daemon Settings](#daemon-settings)
- [Provider Configuration](#provider-configuration)
- [Package Management](#package-management)
- [Environment Variables](#environment-variables)
- [Examples](#examples)

## Configuration Location

The main configuration file lives at:

```
~/.config/construct-cli/config.toml
```

**Configuration loading order:**
1. User config: `~/.config/construct-cli/config.toml`
2. Defaults from `internal/config/defaults.go`
3. Templates from `internal/templates/config.toml`

## Config File Structure

```toml
# Runtime Configuration
[runtime]
engine = "auto"              # Container runtime: auto|podman|docker|container
auto_update_check = true     # Check for updates automatically
update_check_interval = 86400 # Update check interval (seconds)
update_channel = "stable"     # Release channel: stable|beta

# Sandbox Configuration
[sandbox]
mount_home = false           # Mount home directory into container
forward_ssh_agent = true     # Forward SSH agent socket
propagate_git_identity = true # Propagate git host identity
non_root_strict = false      # Enforce non-root user in container
exec_as_host_user = true     # Run commands as host user (when possible)
env_passthrough = ["GITHUB_TOKEN"] # Env vars to always pass through
shell = "/bin/bash"          # Default shell in container

# Network Configuration
[network]
mode = "permissive"          # Network mode: permissive|strict|offline
allowed_domains = []         # Domain allowlist (strict mode)
allowed_ips = []             # IP allowlist (strict mode)
blocked_domains = []         # Domain blocklist
blocked_ips = []             # IP blocklist

# Security Configuration
[security]
hide_secrets = false         # Enable secret redaction (experimental)
hide_secrets_mask_style = "hash" # Mask style: hash|fixed
hide_secrets_deny_paths = [] # Force-scan these files
hide_secrets_allow_paths = [] # Never redact these files (dangerous!)
hide_secrets_passthrough_vars = [] # Never mask these env vars
hide_secrets_report = true   # Show scan report
hide_git_dir = true          # Hide .git directory

# Agent Configuration
[agents]
yolo_all = false             # Enable all agents without confirmation
yolo_agents = []             # Enable specific agents without confirmation
clipboard_image_patch = true # Patch clipboard for image support

# Daemon Configuration
[daemon]
auto_start = true            # Auto-start daemon on agent run
multi_paths_enabled = false  # Enable multi-root daemon mounts
mount_paths = []             # Additional mount roots for daemon

# Provider Configuration
[claude.cc.provider]
ANTHROPIC_BASE_URL = "https://api.anthropic.com"
ANTHROPIC_AUTH_TOKEN = "${ANTHROPIC_API_KEY}"
```

## Runtime Settings

### Container Runtime

```toml
[runtime]
engine = "auto"  # Options: auto, podman, docker, container
backend = "docker"  # Isolation backend: docker|microvm (experimental)
```

**Options:**
- `auto`: Auto-detect available runtime (recommended)
- `podman`: Use Podman explicitly
- `docker`: Use Docker explicitly
- `container`: Use macOS native runtime (macOS 14+)

**Runtime detection order:**
1. macOS native container runtime (if available)
2. Podman
3. Docker (OrbStack on macOS, then Docker Desktop)

### Isolation Backend

`backend` selects the isolation technology. It fails closed: a configured but missing backend is a hard error, never a silent fallback.

- `docker` (default): the OCI container path (Docker/Podman per `engine`).
- `microvm` (experimental): [microsandbox](https://microsandbox.dev) microVMs (a dedicated Linux guest kernel per agent sandbox). Requires `msb` installed (`curl -fsSL https://msb.sh | sh`), Apple Silicon macOS (Hypervisor.framework) or Linux with KVM (`/dev/kvm`). Allocates 4 vCPUs and 4096 MiB RAM by default. Bridges (clipboard, host-exec, SSH agent proxy, loopback dev-site forwarding) and network enforcement modes (permissive, strict, offline) are fully supported. The daemon sandbox automatically transitions images via GHCR pull or local docker save/load. Complete design, benchmarks, and details: [docs/ARCHITECTURE-DESIGN.md](ARCHITECTURE-DESIGN.md#41-microvm-isolation-engine-microsandbox-backend).

### Update Management

```toml
[runtime]
auto_update_check = true          # Enable automatic update checks
update_check_interval = 86400     # Check interval in seconds (24h)
update_channel = "stable"         # Release channel: stable|beta
```

**Channels:**
- `stable`: Production releases only
- `beta`: Includes pre-release features

## Sandbox Settings

### Home Directory Mount

```toml
[sandbox]
mount_home = false
```

**When `false` (default):**
- Agent cannot access your home directory
- More secure, prevents accidental file access
- Recommended for most use cases

**When `true`:**
- Agent can access your home directory
- Useful for workflows requiring home directory access
- Security implication: Agent can read your files

### Automatic Host Mounts

Independent of `mount_home`, Construct adds a few host→container bind-mounts automatically when the host path exists (no configuration needed):

- **Global gitignore** → `/home/construct/.config/git/ignore` (read-only), so agents inherit your host ignore rules.
- **qmd model cache** (`~/.cache/qmd/models`) → `/home/construct/.cache/qmd/models` (read-write), so the qmd semantic-search backend reuses already-downloaded GGUF models (~1.5GB) instead of re-fetching them per container. Read-write lets any lazily-fetched model write back to the shared host cache.

These are skipped silently when the host path is absent.

### SSH Agent Forwarding

```toml
[sandbox]
forward_ssh_agent = true
```

**Automatically:**
- Detects SSH agent socket
- Mounts into container
- Sets `SSH_AUTH_SOCK` environment variable

**Benefits:**
- Agent can use your SSH keys
- No manual key copying
- Secure forwarding

**Fallback:**
```bash
construct sys ssh-import  # Import host keys manually
```

### Environment Variable Passthrough

```toml
[sandbox]
env_passthrough = [
    "GITHUB_TOKEN",
    "CONTEXT7_API_KEY",
    "CUSTOM_API_KEY"
]

env_passthrough_prefixes = [
    "CNSTR_"
]
```

**Behavior:**
- Listed vars always passed to container
- Prefix matching: `CNSTR_*` passes all vars with that prefix
- Useful for API keys and custom configuration

### Host Service Bridging (`host_service_env`)

Inject an environment variable whose value points at a service reachable from the sandbox, with automatic rewriting of host-loopback addresses to the container gateway.

```toml
[sandbox]
host_service_env = [
    "AGENTMEMORY_URL=http://192.168.10.250:3111",
]
```

**Format:** `VAR_NAME=URL`. Each entry becomes a line in the generated `docker-compose.override.yml` `environment:` block.

**Rewrite rule:** `localhost` and `127.0.0.1` in the value are replaced with `host.docker.internal` (the container gateway that routes back to the host). Any other host (a LAN IP, a DNS name, a hostname) passes through **unchanged**.

**When to use this instead of `env_passthrough`.** `env_passthrough` forwards the host env value verbatim. That is wrong for a service URL when the host value uses a name that does not resolve inside the container. Example: the host may reach a NAS via `AGENTMEMORY_URL=http://whitebox:3111`, where `whitebox` is a host-side mDNS name. That name does not resolve inside the sandbox, so passthrough would inject a URL the agent cannot reach. `host_service_env` lets you declare a container-routable address (the raw LAN IP) that differs from the host's hostname form. Rules of thumb:

- **Secrets / tokens** -> `env_passthrough` (the value is host-independent; you want it hot).
- **Service URLs that already resolve inside the sandbox** -> either mechanism works.
- **Service URLs using a host-only name** -> `host_service_env` with a container-routable address.

**⚠ Lifecycle: cold, not hot.** This is the common trap. `env_passthrough` vars are injected as `-e` flags at **every agent launch**, so they self-heal on each run. `host_service_env` vars are written into the compose `environment:` block and baked into the daemon container **only at container creation**. Editing `host_service_env` regenerates the override file, but a running `construct-cli-daemon` container keeps the environment it was created with. A changed value will not take effect until the container is recreated:

```bash
construct sys daemon restart   # StopContainer removes it, Start() does compose run -d -> fresh container
```

Verify the live value after restart:

```bash
docker exec construct-cli-daemon printenv AGENTMEMORY_URL
```

**Symptom of a stale `host_service_env` value.** An agent reports a service as unreachable or "off", and its effective URL is the extension's fallback default (commonly `http://host.docker.internal:<port>`), not the value in your config. That means the container predates the current override. Run `construct sys daemon restart`.

For copy-paste recipes wiring real services into the sandbox (agentmemory on a LAN/NAS, Asana, Slack, Jira/Confluence, Context7, Brave, Claude-compatible provider aliases), see [Services & Integrations](SERVICES.md).

### Host Exec Bridge (Proxy Binaries to the Host)

Run selected binaries on the **host machine** when the agent invokes them from inside the sandbox, instead of in the container. The agent sees them on PATH and calls them normally; a shim proxies each call to a host-side bridge that runs the real binary as your host user.

```toml
[sandbox]
host_binaries = ["wicket"]
```

**⚠ Security**: each listed binary runs on the host with **full container-controlled argv**. Only list binaries you trust with that. Declaring `docker` grants effective host root to the agent; `aws`/`kubectl` likewise. The bridge is token-gated and allowlist-pinned, but argv is not filtered.

- **Off when empty** (default). No bridge starts, zero attack surface.
- Requires `construct build` after first enabling, so the container shim is baked into the image.
- Non-interactive only: no controlling terminal/PTY. Pipe stdin (one-shot) works; interactive prompts do not. Pass `--no-interactive`/`--json`/`--yes` flags where available.
- A startup banner (`⚠ host exec enabled: ...`) confirms when active.
- Full details: [Host Exec Bridge](HOST-EXEC.md).

### Terminal Identity Forwarding (Automatic)

Host terminal-identity markers (`KITTY_WINDOW_ID`, `GHOSTTY_RESOURCES_DIR`, `TERM_PROGRAM`) are forwarded into the container automatically so in-container TUIs and pi extensions can detect the outer terminal (for example, kitty-graphics inline image rendering). No config is needed; it applies on both the direct run and daemon paths.

`TERM` is intentionally **not** forwarded, because forwarding e.g. `xterm-kitty` into a container that lacks the matching terminfo breaks ncurses apps (less, vim, btop). If you need it, add `TERM` to `env_passthrough` and install `ncurses-term` (or `kitty-terminfo`) in the image.

### Host Loopback Forwarding (Browser -> Host Dev Sites)

Chromium hardcodes `localhost` and `*.localhost` to `127.0.0.1` (RFC 6761), bypassing `/etc/hosts`, DNS, `dnsmasq`, and `--host-resolver-rules`. A DNS-layer fix therefore cannot let a **headless browser** (agent-browser) reach host dev servers like `http://hyperpress.localhost`, even though non-browser tools (curl, git, MCP) reach them fine. Construct relays those connections instead.

```toml
[sandbox]
host_loopback_ports = [80, 443]  # default; same port both sides
```

For each listed port, `entrypoint.sh` starts a blind TCP relay on the container's `127.0.0.1:<port>` that forwards to `host.docker.internal:<port>`. Blind relay preserves the HTTP Host header and TLS SNI, so host vhost routers (valet, Hyperpress) and certs see the real hostname. The container is granted `cap_add: NET_BIND_SERVICE` so the non-root user's socat can bind ports below 1024 (the file cap is applied at runtime as root in the entrypoint, since BuildKit blocks file-cap writes at build time).

- **Default** `[80, 443]` covers `http://` and `https://` on the standard ports.
- Add non-standard ports (e.g. `3000`) as needed.
- **Empty list disables** the feature (no relay, no `NET_BIND_SERVICE` cap).
- **Linux caveat**: the host-gateway is the bridge IP, so host services must bind `0.0.0.0`/bridge (not `127.0.0.1`-only) to be reachable. macOS host-gateway already routes to host `127.0.0.1`.

## Network Settings

### Network Modes

```toml
[network]
mode = "permissive"  # Options: permissive, strict, offline
```

**Modes:**

**Permissive (default):**
- All network traffic allowed
- Domain/IP blocklists still enforced
- Good for general development

**Strict:**
- Only allowlisted traffic allowed
- Must configure `allowed_domains` and/or `allowed_ips`
- Best security for sensitive work

**Offline:**
- No network access
- Agent cannot make external API calls
- Maximum security for air-gapped environments

### Allowlists (Strict Mode)

```toml
[network]
mode = "strict"
allowed_domains = [
    "*.anthropic.com",
    "*.openai.com",
    "*.googleapis.com",
    "api.z.ai"
]
allowed_ips = [
    "1.1.1.1/32",    # Cloudflare DNS
    "8.8.8.8/32"     # Google DNS
]
```

**Patterns:**
- Wildcards supported: `*.example.com`
- CIDR notation for IPs: `192.168.1.0/24`
- Exact matches: `api.example.com`

### Blocklists

```toml
[network]
blocked_domains = [
    "*.malicious-site.example",
    "*.phishing.attempt.com"
]
blocked_ips = [
    "192.168.100.100/32",
    "203.0.113.0/24"
]
```

**Priority:** Blocklists take precedence over allowlists.

## Security Settings

### Secret Redaction (Experimental)

⚠️ **Requires environment gate:** `CONSTRUCT_EXPERIMENT_HIDE_SECRETS=1`

```toml
[security]
hide_secrets = true
hide_secrets_mask_style = "hash"
hide_secrets_deny_paths = ["**/secrets.yml"]
hide_secrets_allow_paths = ["~/.aws/credentials"]
hide_secrets_passthrough_vars = ["PUBLIC_API_URL"]
hide_secrets_report = true
hide_git_dir = true
```

**See:** [Hide Secrets Guide](HIDE-SECRETS.md) for complete documentation.

## Agent Settings

### Confirmation Bypass

```toml
[agents]
yolo_all = false      # Bypass confirmation for ALL agents
yolo_agents = [       # Bypass confirmation for specific agents
    "claude",
    "agy"
]
```

**Security implication:**
- `yolo_all = true`: Agents run without confirmation
- Use only in trusted environments
- Specific agent bypass is safer than global bypass

### Clipboard Support

```toml
[agents]
clipboard_image_patch = true  # Patch agents for image clipboard support
```

**Required for:** Image paste support in Claude, Copilot, Antigravity, etc.

## Daemon Settings

### Auto-Start

```toml
[daemon]
auto_start = true  # Auto-start daemon on first agent run
```

**Benefits:**
- Faster subsequent agent startups
- Persistent daemon across multiple runs
- Resource efficient

### Multi-Root Mounts

```toml
[daemon]
multi_paths_enabled = false
mount_paths = []
```

**Enable when:** Working with multiple projects simultaneously

```toml
[daemon]
multi_paths_enabled = true
mount_paths = [
    "~/Dev/Projects",
    "/work/client-repos"
]
```

## Provider Configuration

### Claude Code Providers

Configure alternative Claude API endpoints:

```toml
[claude.cc.zai]
ANTHROPIC_BASE_URL = "https://api.z.ai/api/anthropic"
ANTHROPIC_AUTH_TOKEN = "${CNSTR_ZAI_API_KEY}"
API_TIMEOUT_MS = "3000000"

[claude.cc.minimax]
ANTHROPIC_BASE_URL = "https://api.minimax.io/anthropic"
ANTHROPIC_AUTH_TOKEN = "${CNSTR_MINIMAX_API_KEY}"
```

**See:** [Providers Guide](PROVIDERS.md) for complete provider reference.

## Package Management

### User-Defined Packages

Configure additional packages in `packages.toml`:

```toml
[brew]
packages = [
    "node",
    "python@3.11"
]

[npm]
packages = [
    "typescript",
    "eslint"
]
```

**See:** [Packages Guide](PACKAGES.md) for detailed package management.

## Environment Variables

### Construct-Specific Variables

**Runtime overrides:**
```bash
CONSTRUCT_EXPERIMENT_HIDE_SECRETS=1  # Enable hide-secrets mode
CONSTRUCT_CONFIG_DIR=/custom/path      # Custom config directory
CONSTRUCT_DATA_DIR=/custom/data        # Custom data directory
```

### Provider Variables

**Common provider keys (auto-forwarded):**
- `ANTHROPIC_API_KEY`
- `OPENAI_API_KEY`
- `ANTIGRAVITY_API_KEY`
- `CNSTR_*` (custom prefix)

**Usage:**
```toml
[claude.cc.custom]
ANTHROPIC_AUTH_TOKEN = "${CUSTOM_API_KEY}"
```

## Examples

### Minimal Configuration

```toml
[runtime]
engine = "auto"

[network]
mode = "permissive"
```

### Development Setup

```toml
[runtime]
engine = "podman"
auto_update_check = true

[sandbox]
mount_home = false
forward_ssh_agent = true
env_passthrough = ["GITHUB_TOKEN", "NPM_TOKEN"]

[network]
mode = "permissive"
blocked_domains = ["*.malicious-site.example"]

[agents]
clipboard_image_patch = true
```

### High-Security Setup

```toml
[runtime]
engine = "podman"

[sandbox]
mount_home = false
forward_ssh_agent = false
exec_as_host_user = true

[network]
mode = "strict"
allowed_domains = [
    "*.anthropic.com",
    "*.openai.com",
    "api.z.ai"
]
blocked_ips = ["0.0.0.0/8"] # Block local network

[security]
hide_secrets = true  # Requires CONSTRUCT_EXPERIMENT_HIDE_SECRETS=1
hide_git_dir = true

[agents]
yolo_all = false
```

### Offline Development

```toml
[runtime]
engine = "container"  # macOS native runtime

[network]
mode = "offline"  # No network access

[sandbox]
mount_home = true  # Need home for offline files
env_passthrough = ["OFFLINE_MODE"]
```

## Configuration Migration

Construct automatically migrates your config when upgrading:

1. **Backs up** existing config to `config.toml.backup`
2. **Merges** new defaults with your settings
3. **Preserves** all your customizations
4. **Validates** configuration after merge

**Manual migration:**
```bash
construct sys config --migrate
```

## Next Steps

- [Installation Guide](INSTALLATION.md)
- [Security Guide](SECURITY.md)
- [Providers Guide](PROVIDERS.md)
- [Packages Guide](PACKAGES.md)

## Getting Help

```bash
construct sys doctor    # Check configuration and runtime
construct sys config    # Open config in editor
construct --help         # Show all options
```

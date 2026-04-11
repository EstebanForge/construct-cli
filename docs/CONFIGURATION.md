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
    "gemini"
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

**Required for:** Image paste support in Claude, Copilot, Gemini, etc.

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
- `GEMINI_API_KEY`
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

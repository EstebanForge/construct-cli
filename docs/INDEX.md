# Construct CLI Documentation

Complete documentation for The Construct CLI.

## Quick Links

- [Installation](INSTALLATION.md) - Get started with Construct
- [Quick Reference](QUICK-REFERENCE.md) - Everyday commands, examples, PATH shims
- [Configuration](CONFIGURATION.md) - Configure all settings
- [Security](SECURITY.md) - Security features and best practices
- [Host Exec Bridge](HOST-EXEC.md) - Proxy host-only binaries into the sandbox
- [Harness Path-Arg Staging](HARNESS-STAGING.md) - Run orchestrator-driven agents (Paseo & co.) inside the sandbox
- [Using Construct with Agent Harnesses](HARNESSES.md) - Setup guide: shims, daemon mounts, PATH, troubleshooting
- [Services & Integrations](SERVICES.md) - Recipes for wiring agentmemory, Slack, Asana, Jira, providers, and more
- [Brew Exit Analysis](BREW-EXIT.md) - package-by-package Homebrew-to-Debian migration analysis
- [Sentinel Credential Proxy](TODO-SENTINEL-PROXY.md) - Host-side egress CONNECT proxy with dynamic CA leaf signing, sentinel key substitution, and raw TCP database tunnels
- [Lessons from v1-v2](LESSONS-TO-V3.md) - What to adopt from v1 and v2: proxy refinements, v3.0 removals, new scope
- [TODO](TODO.md) - GA checklist and parked follow-ups
- [README](../README.md) - Project overview and quick start

## Getting Started

1. **[Install Construct](INSTALLATION.md)**
   - Platform-specific instructions
   - Container runtime setup
   - Post-installation verification

2. **[Configure Construct](CONFIGURATION.md)**
   - Runtime settings
   - Network policy tiers
   - Security options
   - Provider configuration

3. **[Learn Security Features](SECURITY.md)**
   - MicroVM container isolation
   - Sentinel credential proxying
   - Host git repository protection
   - Secret redaction (hide_secrets)
   - Security best practices

## Core Features

### Sentinel Credential Proxy & Network Policy

**[Sentinel Credential Proxy](TODO-SENTINEL-PROXY.md)** - Egress security & credential isolation

Keep real API keys out of the sandbox while allowing agents to execute:
- Shape-mimicking sentinel placeholders for provider keys
- Outbound leak scanner dropping unswapped secret bytes
- Domain fronting guards (CONNECT == TLS SNI == HTTP Host)
- Strict DNS rebinding and private IP refusal
- Policy tiers (`allowlist`, `open`, `internet`, `none`) with ecosystem presets
- Opt-in raw TCP tunnels (`allowed_tcp`) for databases and caches

### Host Git Protection & Disposable Clones

**[Security Guide](SECURITY.md)** - Workspace protection

Protect project repositories from agent corruption:
- OS immutable attributes (`chattr +i` / `chflags uchg`) on host `.git`
- Read-only `.git` masking inside guest mounts
- Disposable execution mode via `--clone` with automatic patch bundle generation

### Modular Toolchains

**[Architecture Design](ARCHITECTURE-DESIGN.md)** - Polyglot language runtimes

On-demand multi-arch OCI toolchains fetched and cached without base image bloat:
- Auto-detection from project markers (`go.mod`, `Cargo.toml`, etc.)
- Multi-arch OCI distribution (Go, Rust, Python, Node, PHP, Ruby, Zig, Dart, Kotlin, Java, Swift)
- Auto-caching at `~/.cache/construct/toolchains`

### Secret Redaction

**[Hide Secrets Mode](HIDE-SECRETS.md)** - Complete guide

Prevents LLM agents from accessing raw secrets in your project:
- File redaction (`.env`, config files, credentials)
- Environment variable masking
- Isolated workspace (OverlayFS/APFS)
- Stream-time output masking

### MicroVM Isolation Engine (Default)

**[MicroVM Backend Architecture](ARCHITECTURE-DESIGN.md#41-microvm-isolation-engine-microsandbox-backend)** - Hardware-level isolation

Run agents inside dedicated Linux microVMs via microsandbox:
- Dedicated Linux guest kernel per agent sandbox
- Hardware hypervisor isolation (Hypervisor.framework / KVM)
- Full bridge support: clipboard, host-exec, SSH agent proxy, loopback relays
- Resource allocation: 4 vCPUs and 4096 MiB RAM

### Package Management

**[Packages Guide](PACKAGES.md)** - Customize your sandbox

Install additional packages:
- apt (Debian/Ubuntu)
- mise (polyglot tool versions)
- bun (Bun package manager)
- npm (Node.js)
- pip (Python)
- cargo (Rust)
- gems (Ruby)

### Provider Configuration

**[Providers Guide](PROVIDERS.md)** - Configure custom Claude endpoints

Supported providers:
- Zai GLM
- MiniMax M2
- Kimi K2
- Qwen
- Mimo
- And more...

## Reference Documentation

### Architecture & Design

**[Architecture Design](ARCHITECTURE-DESIGN.md)** - Technical internals

- System architecture
- Component interactions
- Security model
- Performance considerations
- Extension points

### Clipboard Support

**[Clipboard Bridge](CLIPBOARD.md)** - Clipboard integration

- Text and image paste support
- Per-agent compatibility
- Platform-specific behavior
- Troubleshooting

### Development

**[Development Guide](DEVELOPMENT.md)** - Contributing to Construct

- Build from source
- Run tests
- Code organization
- Pull request process

**[Contributing Guidelines](CONTRIBUTING.md)** - Contribution standards

- Code style guidelines
- Commit message conventions
- Issue reporting
- Feature requests

## Security Documentation

### Container Security

**[Security Guide](SECURITY.md)** - Complete security reference

Topics covered:
- Container isolation
- User permissions
- Filesystem boundaries
- Network isolation
- Build integrity

### Secret Redaction

**[Hide Secrets Mode](HIDE-SECRETS.md)** - User guide for experimental feature

- Quick start guide
- Configuration options
- Use cases and examples
- Troubleshooting
- Limitations and future enhancements

## Advanced Topics

### Configuration

**[Configuration Guide](CONFIGURATION.md)** - Complete reference

All settings explained:
- Runtime configuration
- Sandbox options
- Network isolation
- Security settings
- Agent configuration
- Daemon settings
- Provider setup
- Package management

### Installation

**[Installation Guide](INSTALLATION.md)** - Detailed installation instructions

- macOS (native runtime, Docker Desktop, OrbStack)
- Linux (Podman, Docker)
- Windows (WSL2)
- Troubleshooting
- Uninstallation

## Documentation Index

| Document | Description |
|----------|-------------|
| [**README**](../README.md) | Project overview, highlights, quick start |
| [**Installation**](INSTALLATION.md) | Platform-specific installation guide |
| [**Configuration**](CONFIGURATION.md) | Complete configuration reference |
| [**Security**](SECURITY.md) | Security features and best practices |
| [**Hide Secrets**](HIDE-SECRETS.md) | Secret redaction user guide |
| [**VM Backend**](ARCHITECTURE-DESIGN.md#41-microvm-isolation-engine-microsandbox-backend) | Default microVM isolation architecture and operational design |
| [**Providers**](PROVIDERS.md) | Custom Claude API endpoints |
| [**Agents**](AGENTS.md) | Complete list of supported agents |
| [**Packages**](PACKAGES.md) | User-defined package management |
| [**Architecture**](ARCHITECTURE-DESIGN.md) | Technical design and internals |
| [**Services & Integrations**](SERVICES.md) | Recipes for wiring services (agentmemory, Slack, Asana, providers) |
| [**Clipboard**](CLIPBOARD.md) | Clipboard integration details |
| [**Development**](DEVELOPMENT.md) | Contributing and development |
| [**Contributing**](CONTRIBUTING.md) | Contribution guidelines |

## Getting Help

### CLI Help

```bash
construct --help              # Show all commands
construct sys doctor           # System health check
construct sys config           # Edit configuration
construct sys exec -- <cmd>    # Run command inside container
```

### Documentation

- **Full docs**: [docs/](./)
- **Project README**: [../README.md](../README.md)
- **GitHub Issues**: [Report issues](https://github.com/EstebanForge/construct-cli/issues)
- **GitHub Discussions**: [Ask questions](https://github.com/EstebanForge/construct-cli/discussions)

### Quick Reference

**Installation:**
```bash
curl -fsSL https://raw.githubusercontent.com/EstebanForge/construct-cli/main/scripts/install.sh | bash
```

**First run:**
```bash
construct sys init
```

**Run agent:**
```bash
construct claude "Help me with this code"
```

**Configuration:**
```bash
construct sys config  # Opens config in editor
```

---

**[← Back to README](../README.md)**

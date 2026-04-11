# Construct CLI Documentation

Complete documentation for The Construct CLI.

## Quick Links

- [Installation](INSTALLATION.md) - Get started with Construct
- [Configuration](CONFIGURATION.md) - Configure all settings
- [Security](SECURITY.md) - Security features and best practices
- [README](../README.md) - Project overview and quick start

## Getting Started

1. **[Install Construct](INSTALLATION.md)**
   - Platform-specific instructions
   - Container runtime setup
   - Post-installation verification

2. **[Configure Construct](CONFIGURATION.md)**
   - Runtime settings
   - Network modes
   - Security options
   - Provider configuration

3. **[Learn Security Features](SECURITY.md)**
   - Container isolation
   - Secret redaction (experimental)
   - Security best practices

## Core Features

### Secret Redaction (Experimental)

**[Hide Secrets Mode](HIDE-SECRETS.md)** - Complete guide

Prevents LLM agents from accessing raw secrets in your project:
- File redaction (`.env`, config files, credentials)
- Environment variable masking
- Isolated workspace (OverlayFS/APFS)
- Stream-time output masking

### Provider Configuration

**[Providers Guide](PROVIDERS.md)** - Configure custom Claude endpoints

Supported providers:
- Zai GLM
- MiniMax M2
- Kimi K2
- Qwen
- Mimo
- And more...

### Package Management

**[Packages Guide](PACKAGES.md)** - Customize your sandbox

Install additional packages:
- apt (Debian/Ubuntu)
- brew (Homebrew)
- bun (Bun package manager)
- npm (Node.js)
- pip (Python)

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
| [**Providers**](PROVIDERS.md) | Custom Claude API endpoints |
| [**Packages**](PACKAGES.md) | User-defined package management |
| [**Architecture**](ARCHITECTURE-DESIGN.md) | Technical design and internals |
| [**Clipboard**](CLIPBOARD.md) | Clipboard integration details |
| [**Development**](DEVELOPMENT.md) | Contributing and development |
| [**Contributing**](CONTRIBUTING.md) | Contribution guidelines |

## Getting Help

### CLI Help

```bash
construct --help              # Show all commands
construct sys doctor           # System health check
construct sys config           # Edit configuration
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

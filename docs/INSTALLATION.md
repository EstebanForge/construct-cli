# Installation Guide

Detailed installation instructions for The Construct CLI across different platforms and environments.

## Table of Contents

- [Quick Install](#quick-install)
- [Platform-Specific Installation](#platform-specific-installation)
  - [macOS](#macos)
  - [Linux](#linux)
  - [Windows (WSL)](#windows-wsl)
- [Installation Methods](#installation-methods)
  - [Homebrew](#homebrew)
  - [One-Line Script](#one-line-script)
  - [Manual Binary](#manual-binary)
- [Post-Installation](#post-installation)
  - [First Run Setup](#first-run-setup)
  - [Container Runtime Setup](#container-runtime-setup)
  - [Verification](#verification)
- [Troubleshooting](#troubleshooting)
  - [Common Issues](#common-issues)
  - [Platform-Specific Problems](#platform-specific-problems)
- [Uninstallation](#uninstallation)

## Quick Install

### Fastest Method (Recommended)

```bash
curl -fsSL https://raw.githubusercontent.com/EstebanForge/construct-cli/main/scripts/install.sh | bash
```

This will:
1. Download the latest binary for your platform
2. Install to `/usr/local/bin`
3. Run first-time setup automatically

### Alternative: Homebrew

```bash
brew install EstebanForge/tap/construct-cli
```

## Platform-Specific Installation

### macOS

#### Requirements

- **macOS 14+ (Sonoma)** or later for native container runtime
- **macOS 13+** supported with Docker Desktop or OrbStack
- **Homebrew** (optional, for easier updates)

#### Container Runtime Options

**Option 1: Native Container Runtime (macOS 14+)**
- Built into macOS
- No additional software required
- Best performance
- Auto-detected by Construct

**Option 2: Docker Desktop**
- Download from [docker.com](https://www.docker.com/products/docker-desktop/)
- Start Docker Desktop before running Construct
- Works on macOS 13+

**Option 3: OrbStack**
- Download from [orbstack.dev](https://orbstack.dev/)
- Lightweight alternative to Docker Desktop
- Auto-detected if available

#### Installation Steps

```bash
# Using install script (recommended)
curl -fsSL https://raw.githubusercontent.com/EstebanForge/construct-cli/main/scripts/install.sh | bash

# Or using Homebrew
brew install EstebanForge/tap/construct-cli

# Verify installation
construct version
```

### Linux

#### Requirements

- **Linux kernel 5.15+** recommended
- **Podman** (recommended) or Docker
- **Systemd** for service management (optional)

#### Container Runtime Setup

**Option 1: Podman (Recommended)**

```bash
# Install Podman (Ubuntu/Debian)
sudo apt update
sudo apt install -y podman

# Enable user namespaces
echo "user.max_user_namespaces=15000" | sudo tee -a /etc/sysctl.conf
sudo sysctl -p

# Configure subuid/subgid for your user
sudo usermod --add-subuids --range 100000-165535 $USER
sudo usermod --add-subgids --range 100000-165535 $USER
```

**Option 2: Docker**

```bash
# Install Docker (Ubuntu/Debian)
curl -fsSL https://get.docker.com | sh
sudo usermod -aG docker $USER

# Log out and back in for group changes to take effect
```

#### Installation Steps

```bash
# Download and install
curl -fsSL https://raw.githubusercontent.com/EstebanForge/construct-cli/main/scripts/install.sh | bash

# Or build from source (if you prefer)
go install github.com/EstebanForge/construct-cli@latest
```

### Windows (WSL)

#### Requirements

- **Windows 11** or Windows 10 with WSL2 enabled
- **Ubuntu WSL** or other Debian-based distro
- Follow Linux installation instructions within WSL

#### WSL2 Setup

```powershell
# In PowerShell (Admin)
wsl --install
wsl --set-default-version 2
```

Then follow the Linux installation instructions inside WSL.

## Installation Methods

### Homebrew

**macOS & Linux**

```bash
# Add tap and install
brew install EstebanForge/tap/construct-cli

# Update to latest version
brew upgrade construct-cli

# Uninstall
brew uninstall construct-cli
```

### One-Line Script

**Universal (Bash/Zsh)**

```bash
curl -fsSL https://raw.githubusercontent.com/EstebanForge/construct-cli/main/scripts/install.sh | bash
```

**Script options:**

```bash
# Install specific version
curl -fsSL https://raw.githubusercontent.com/EstebanForge/construct-cli/main/scripts/install.sh | bash -s -- --version 1.6.0

# Install to custom directory
curl -fsSL https://raw.githubusercontent.com/EstebanForge/construct-cli/main/scripts/install.sh | bash -s -- --prefix ~/local/bin

# Install beta version
curl -fsSL https://raw.githubusercontent.com/EstebanForge/construct-cli/main/scripts/install.sh | CHANNEL=beta bash
```

### Manual Binary Download

**Direct download**

```bash
# Download latest release for your platform
wget https://github.com/EstebanForge/construct-cli/releases/latest/download/construct-linux-amd64
chmod +x construct-linux-amd64
sudo mv construct-linux-amd64 /usr/local/bin/construct
```

**Available platforms:**
- `construct-linux-amd64`
- `construct-linux-arm64`
- `construct-darwin-amd64` (macOS Intel)
- `construct-darwin-arm64` (macOS Apple Silicon)

## Post-Installation

### First Run Setup

After installation, run the first-time setup:

```bash
construct sys init
```

This will:
1. Create configuration directory: `~/.config/construct-cli/`
2. Generate default `config.toml`
3. Build container images (first run only, ~5-10 minutes)
4. Install agents to persistent volume
5. Create `ct` alias if possible

### Container Runtime Setup

#### Verify Runtime Detection

```bash
construct sys doctor
```

Expected output:
```
✓ Container runtime detected: podman
✓ Runtime version: 4.9.4
✓ Config directory: ~/.config/construct-cli
✓ Container images: built
✓ Agents installed: 18
```

#### Manual Runtime Configuration

If auto-detection fails, specify runtime explicitly:

```toml
# ~/.config/construct-cli/config.toml
[runtime]
engine = "podman"  # or "docker" or "container"
```

### Verification

Test your installation:

```bash
# Check version
construct version

# Run a simple agent command
construct claude "Say hello"

# Check system health
construct sys doctor
```

## Troubleshooting

### Common Issues

#### "No container runtime found"

**Solution**: Install a container runtime

```bash
# macOS: Install Docker Desktop or use native runtime (macOS 14+)
# Linux: Install Podman or Docker
```

#### "Permission denied" when running construct

**Solution**: Check binary permissions

```bash
chmod +x /usr/local/bin/construct
```

#### "construct: command not found"

**Solution**: Verify installation path

```bash
# Check if construct is in PATH
which construct

# If not found, add to PATH or use full path
export PATH=$PATH:/usr/local/bin
```

### Platform-Specific Problems

#### macOS: "container runtime not available on macOS 13"

**Solution**: Install Docker Desktop or OrbStack

```bash
# Install OrbStack (lightweight)
brew install --cask orbstack
```

#### Linux: "user namespaces not enabled"

**Solution**: Enable user namespaces

```bash
echo "user.max_user_namespaces=15000" | sudo tee -a /etc/sysctl.conf
sudo sysctl -p
```

#### Linux: "Cannot connect to Podman socket"

**Solution**: Start Podman service

```bash
# Systemd systems
sudo systemctl start podman
sudo systemctl enable podman

# Or use rootless podman
podman system service start
```

#### WSL: "Container commands not working"

**Solution**: Ensure WSL2 is enabled

```powershell
# In PowerShell (Admin)
wsl --set-default-version 2
```

## Uninstallation

### Remove Construct Binary

```bash
# If installed via script
sudo rm /usr/local/bin/construct

# If installed via Homebrew
brew uninstall construct-cli

# If installed via go install
rm ~/go/bin/construct
```

### Remove Configuration and Data

```bash
# Remove config directory
rm -rf ~/.config/construct-cli

# Remove persistent volumes (Docker)
docker volume rm construct-cli-agent-data
docker volume rm construct-cli-agent-work

# Remove persistent volumes (Podman)
podman volume rm construct-cli-agent-data
podman volume rm construct-cli-agent-work
```

### Remove Host Aliases

```bash
# Remove aliases installed by Construct
construct sys aliases --uninstall

# Or manually remove from shell config
# Edit ~/.bashrc, ~/.zshrc, etc. and remove construct/ct aliases
```

## Next Steps

After installation:

1. **Read the [Configuration Guide](CONFIGURATION.md)** to customize Construct
2. **Check out [Security Guide](SECURITY.md)** for hardening your setup
3. **Explore [Providers Guide](PROVIDERS.md)** to use custom Claude endpoints
4. **See [Examples](../README.md#common-examples)** for common workflows

## Getting Help

- **Documentation**: See [docs/](../) for full documentation
- **Issues**: Report at [GitHub Issues](https://github.com/EstebanForge/construct-cli/issues)
- **Discussions**: Join [GitHub Discussions](https://github.com/EstebanForge/construct-cli/discussions)

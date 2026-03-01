# Development Guide

Quick reference for building and testing Construct CLI during development.

## Quick Start

```bash
# Clone and build
git clone https://github.com/EstebanForge/construct-cli.git
cd construct-cli
make build

# Run tests
make test
```

## Installation for Testing

Construct provides three installation methods for local testing:

### Method 1: Quick Dev Install (Recommended for Rapid Iteration)

Fastest method for development - installs to `~/.local/bin` without sudo or confirmations.

```bash
# Quick install (no sudo needed)
make install-dev

# Or directly:
./scripts/dev-install.sh

# Verify
construct --version
```

**Pros:**
- ⚡ Very fast (no confirmations)
- 🔓 No sudo required
- ✨ Perfect for rapid iteration

**Cons:**
- No backup of existing binary
- Requires `~/.local/bin` in your PATH

### Method 2: Local Install with Backup (Recommended for Testing)

Full-featured install with backup, verification, and safety checks.

```bash
# Install to ~/.local/bin (default, no sudo)
make install-local

# Or install to system directory (requires sudo)
sudo INSTALL_DIR=/usr/local/bin make install-local

# With options
./scripts/install-local.sh --help
./scripts/install-local.sh --clean        # Clean build first
./scripts/install-local.sh --no-backup    # Skip backup
./scripts/install-local.sh --skip-build   # Use existing binary

# Install to system directory with options
sudo INSTALL_DIR=/usr/local/bin ./scripts/install-local.sh --clean
```

**Pros:**
- 💾 Automatic backup of existing binary
- ✅ Full verification
- 📋 Detailed output
- 🔓 No sudo needed (default)

**Use cases:**
- Testing before committing changes
- Comparing versions
- Need to rollback easily

### Method 3: Standard Install

Simple install without bells and whistles.

```bash
make install
```

## Uninstalling

```bash
# Basic uninstall from ~/.local/bin (no sudo)
make uninstall-local

# List available backups
./scripts/uninstall-local.sh --list-backups

# Restore previous version
./scripts/uninstall-local.sh --restore

# Force removal without confirmation
./scripts/uninstall-local.sh --force

# Uninstall from system directory (requires sudo)
sudo INSTALL_DIR=/usr/local/bin ./scripts/uninstall-local.sh
```

## Development Workflow

### Typical dev cycle:

```bash
# Make changes to code
vim internal/agent/runner.go

# Quick test
make install-dev
construct sys doctor

# If something broke, check backups
./scripts/uninstall-local.sh --list-backups

# Restore previous working version
./scripts/uninstall-local.sh --restore
# If you installed to /usr/local/bin:
sudo INSTALL_DIR=/usr/local/bin ./scripts/uninstall-local.sh --restore
```

### Testing workflow:

```bash
# Full testing before committing
make clean                # Clean old builds
make fmt                  # go fmt (+ goimports if installed)
make vet                  # go vet
make lint                 # golangci-lint
make test-unit            # Unit tests
make install-local        # Install with backup
construct sys doctor      # Smoke test
make test-integration     # Integration tests
```

## Build Commands

```bash
# Basic build
make build

# Build with all tests
make clean test

# Cross-compile for all platforms
make cross-compile

# Create release build
make release

# Development mode (build + init)
make dev
```

## Testing

```bash
# Run all tests
make test

# Unit tests only
make test-unit

# Integration tests only
make test-integration

# With coverage report
make test-coverage
open coverage.html

# Run benchmarks
make bench

# Full local verification (fmt, vet, lint, test, build)
make check

# CI alias (same as make check)
make ci
```

## Cleaning

```bash
# Clean build artifacts only
make clean

# Clean Docker resources (containers, volumes, images)
make clean-docker

# Clean everything (build + Docker + config)
make clean-all
```

## Installation Directories

Different install locations and when to use them:

| Directory | Sudo? | Default? | When to use |
|-----------|-------|----------|-------------|
| `~/.local/bin` | ❌ | ✅ | User-local, development (default) |
| `/usr/local/bin` | ✅ | ❌ | System-wide install, production |
| `~/bin` | ❌ | ❌ | Alternative user directory |
| `/opt/construct` | ✅ | ❌ | Isolated installation |

Set with environment variable:

```bash
# Install to custom location
INSTALL_DIR=~/bin make install-local

# Install to system directory
sudo INSTALL_DIR=/usr/local/bin make install-local
```

## PATH Setup

If binaries aren't found, add installation directory to PATH:

```bash
# For ~/.local/bin (add to ~/.bashrc or ~/.zshrc)
export PATH="$HOME/.local/bin:$PATH"

# For ~/bin
export PATH="$HOME/bin:$PATH"

# Reload shell
source ~/.bashrc   # or source ~/.zshrc
```

## Troubleshooting

### Binary not found after installation

```bash
# Check if installed
which construct

# Check PATH
echo $PATH

# If not in PATH, add it
export PATH="$HOME/.local/bin:$PATH"
```

### Permission denied

```bash
# If installing to /usr/local/bin
sudo INSTALL_DIR=/usr/local/bin make install-local

# Or use user directory
INSTALL_DIR=~/.local/bin make install-dev
```

### Old version still running

```bash
# Check all installed versions
which -a construct

# Remove old versions
sudo rm /usr/local/bin/construct
rm ~/.local/bin/construct

# Reinstall
make install-dev
```

### Tests failing

```bash
# Clean everything and retry
make clean-all
make build
make test

# Check Docker
docker ps
docker images | grep construct

# Check config
cat ~/.config/construct-cli/config.toml
```

### Linux UID/ownership behavior

On Linux, Construct propagates host identity for compose startup and exec paths to prevent ownership drift in mounted config/home paths.

```bash
# See current mapping/doctor details
construct sys doctor
```

- If your host UID is not present in container `/etc/passwd`, Construct warns, keeps numeric `UID:GID` mapping, and forces `HOME=/home/construct`.
- If migrations or runtime prep hit ownership issues, Construct prompts for confirmation and then attempts runtime-aware repair (`podman unshare chown` for rootless/userns modes when applicable, then sudo fallback).
- For recurring Linux permission/ownership issues, use:

```bash
construct sys doctor --fix
```

- `doctor --fix` can repair config ownership/permissions, recycle stale session/daemon containers, and rebuild stale/missing images required for startup fixes.
- If automated repair is declined or fails, use:

```bash
podman unshare chown -R 0:0 ~/.config/construct-cli
sudo chown -R "$(id -u)":"$(id -g)" ~/.config/construct-cli
```

## Debugging

### Verbose mode

```bash
construct --ct-verbose sys shell
construct --ct-debug claude "test"
```

### Check installation

```bash
construct --version
construct sys doctor
```

### Logs

```bash
# Build logs
cat ~/.config/construct-cli/logs/build_*.log

# Package install logs
cat ~/.config/construct-cli/logs/install_packages_*.log

# Update logs
cat ~/.config/construct-cli/logs/update_*.log
```

## Common Tasks

### Update dependencies

```bash
make deps
```

### Format code

```bash
make fmt
```

### Run linters

```bash
make lint
```

### Create release

```bash
make release
ls -lh dist/
```

### Release channels (stable/beta)

```bash
# Stable release example
# 1) set Version in internal/constants/constants.go to 1.3.8
# 2) tag and push
git tag 1.3.8
git push origin main --tags

# Beta release example
# 1) set Version in internal/constants/constants.go to 1.3.9-beta.1
# 2) tag and push
git tag 1.3.9-beta.1
git push origin main --tags
```

- Do not manually edit `VERSION` or `VERSION-BETA`; release workflow updates them.
- Stable tags update `VERSION`.
- Prerelease tags (contain `-`, e.g. `1.3.9-beta.1`) update `VERSION-BETA`.
- `internal/constants/constants.go` must match the exact tag string for `make release` to pass.

## Git Workflow

```bash
# Create feature branch
git checkout -b feature/clipboard-support

# Make changes and test
make install-dev
construct sys doctor

# Run full test suite
make ci

# Commit
git add .
git commit -m "Add clipboard support"

# Push
git push origin feature/clipboard-support
```

## Tips

1. **Use `install-dev` during active development** - fastest iteration cycle
2. **Use `install-local` before committing** - full verification with backup
3. **Always run `make ci` before pushing** - catches issues early
4. **Keep backups** - `install-local` automatically creates timestamped backups
5. **Check logs** - build and install logs are in `~/.config/construct-cli/logs/`

## VS Code Tasks

Add to `.vscode/tasks.json`:

```json
{
  "version": "2.0.0",
  "tasks": [
    {
      "label": "Build",
      "type": "shell",
      "command": "make build",
      "group": {
        "kind": "build",
        "isDefault": true
      }
    },
    {
      "label": "Test",
      "type": "shell",
      "command": "make test"
    },
    {
      "label": "Install Dev",
      "type": "shell",
      "command": "make install-dev"
    }
  ]
}
```

## Help

All Makefile targets with descriptions:

```bash
make help
```

Output:
```
Construct CLI - Build System

Usage: make [target]

Targets:
  help                 Show this help message
  build                Build the binary
  test                 Run all tests
  test-unit            Run Go unit tests
  test-integration     Run integration tests
  install-local        Install with backup and verification
  install-dev          Quick dev install (no sudo)
  uninstall-local      Uninstall with backup options
  clean                Clean build artifacts
  ...
```

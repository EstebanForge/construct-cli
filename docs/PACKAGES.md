# Packages Guide

Customize your Construct sandbox with user-defined packages via `packages.toml`.

## Table of Contents

- [Overview](#overview)
- [Package Managers](#package-managers)
- [Configuration](#configuration)
- [Available Packages](#available-packages)
- [Custom Packages](#custom-packages)
- [Package Installation](#package-installation)
- [Troubleshooting](#troubleshooting)

## Overview

Construct supports installing additional packages inside the sandbox environment through `packages.toml`. This allows you to customize your development environment without rebuilding containers.

**Key features:**
- **Multiple package managers**: apt, brew, bun, npm, pip
- **Persistent installation**: Packages persist across container runs
- **Easy updates**: Apply new packages with `construct sys packages --install`
- **Custom toolchains**: Optional development tools (nix, asdf, mise, vmr, etc.)

## Package Managers

### Supported Package Managers

| Manager | Description | Usage |
|---------|-------------|-------|
| **apt** | Debian/Ubuntu packages | System libraries, CLI tools |
| **brew** | Homebrew (macOS/Linux) | Development tools, languages |
| **bun** | Bun package manager | JavaScript runtime and packages |
| **npm** | Node Package Manager | Node.js packages and CLIs |
| **pip** | Python Package Manager | Python packages and modules |

### Package Manager Priority

**Installation order:**
1. apt (system packages)
2. brew (Homebrew packages)
3. bun (JavaScript runtime)
4. npm (Node.js packages)
5. pip (Python packages)

This order ensures dependencies are installed correctly.

## Configuration

### Packages.toml Location

```
~/.config/construct-cli/packages.toml
```

### Basic Configuration

```toml
# Debian/Ubuntu packages
[apt]
packages = [
    "curl",
    "git",
    "vim",
    "htop"
]

# Homebrew packages
[brew]
packages = [
    "node",
    "python@3.11",
    "go"
]

# Bun packages
[bun]
packages = [
    "typescript",
    "eslint",
    "@antfu/solidity"
]

# npm packages
[npm]
packages = [
    "typescript",
    "prettier",
    "eslint"
]

# pip packages
[pip]
packages = [
    "requests",
    "pytest",
    "black"
]
```

## Available Packages

### System Packages (apt)

**Common system packages:**

```toml
[apt]
packages = [
    "build-essential",  # C/C++ build tools
    "curl",             # HTTP client
    "git",              # Version control
    "vim",              # Text editor
    "htop",             # Process monitor
    "jq",               # JSON processor
    "ripgrep",          # Fast search tool
    "tmux",             # Terminal multiplexer
    "zsh",              # Shell
]
```

**Development tools:**
```toml
[apt]
packages = [
    "clang",            # C/C++ compiler
    "clang-format",     # C/C++ formatter
    "python3",          # Python 3
    "python3-pip",       # Python package manager
    "nodejs",           # JavaScript runtime
    "openjdk-17-jre",   # Java runtime
]
```

### Development Tools (brew)

**Languages and runtimes:**
```toml
[brew]
packages = [
    "node",             # Node.js
    "python@3.11",      # Python 3.11
    "go",               # Go
    "rust",             # Rust
    "java",             # Java
]
```

**Development tools:**
```toml
[brew]
packages = [
    "git",              # Version control
    "gh",               # GitHub CLI
    "jq",               # JSON processor
    "yq",               # YAML processor
    "fd",               # Fast file finder
    "ripgrep",          # Fast content search
    "bat",              # Better cat
    "eza",              # Better ls
    "delta",            # Better git diff
    "tldr",             # Simplified man pages
]
```

### JavaScript/TypeScript (bun)

**Runtime and packages:**
```toml
[bun]
packages = [
    "typescript",       # TypeScript compiler
    "eslint",           # Linter
    "prettier",         # Formatter
    "@antfu/solidity",  # Solidity compiler
]
```

### Node.js Packages (npm)

**Development tools:**
```toml
[npm]
packages = [
    "typescript",
    "prettier",
    "eslint",
    "@typescript-eslint/eslint-plugin",
]
```

**Global CLIs:**
```toml
[npm]
packages = [
    "serverless",       # AWS Lambda framework
    "tfenv",            # Terraform version manager
    "knit",             # Knative CLI
]
```

### Python Packages (pip)

**Development tools:**
```toml
[pip]
packages = [
    "requests",         # HTTP library
    "pytest",           # Testing framework
    "black",            # Code formatter
    "mypy",             # Type checker
    "flake8",           # Linter
]
```

**AWS/cloud tools:**
```toml
[pip]
packages = [
    "boto3",            # AWS SDK
    "awscli",           # AWS CLI
    "terraform",        # Infrastructure as code
]
```

## Custom Packages

### Adding Custom Packages

Simply edit `packages.toml`:

```toml
[apt]
packages = [
    "my-custom-tool",
    "another-package"
]
```

Then apply changes:

```bash
construct sys packages --install
```

### Package Versions

**apt (system packages):**
```toml
[apt]
packages = [
    "python3.11",       # Specific version
    "openjdk-17-jre",    # Specific version
]
```

**brew (versioned packages):**
```toml
[brew]
packages = [
    "python@3.11",      # Version 3.11
    "node@18",           # Node 18.x
    "go@1.20",          # Go 1.20
]
```

**npm (versioned packages):**
```toml
[npm]
packages = [
    "typescript@5.0.0", # Specific version
    "eslint@8.0.0",      # Specific version
]
```

**pip (versioned packages):**
```toml
[pip]
packages = [
    "requests==2.31.0", # Specific version
    "pytest==7.4.0",      # Specific version
]
```

## Package Installation

### First-Time Setup

After editing `packages.toml`, install packages:

```bash
construct sys packages --install
```

This will:
1. Update `packages.toml` in persistent volume
2. Install packages in running container
3. Update containers on next run

### Package Updates

**Update all packages:**
```bash
construct sys update
```

**Update specific packages:**
1. Edit version in `packages.toml`
2. Run `construct sys packages --install`

### Package Removal

**To remove packages:**
1. Edit `packages.toml`, remove unwanted packages
2. Run `construct sys packages --install`
3. Or run `construct sys rebuild` for clean slate

## Optional Tools

### Advanced Development Tools

Configure optional development tools:

```toml
[tools]
phpbrew = true        # PHP version manager
nix = true             # Nix package manager
nvm = true             # Node version manager
asdf = true            # Multi-language version manager
mise = true            # Modern asdf alternative
vmr = true             # V version manager
```

**Tool availability:**
- Tools must be available in package repositories
- Some tools require additional setup
- Check documentation for each tool

### Enable/Disable Tools

```toml
[tools]
phpbrew = false       # Disabled
nix = false           # Disabled
nvm = true            # Enabled
asdf = true           # Enabled
mise = false          # Disabled
vmr = true            # Enabled
```

## Troubleshooting

### Package Installation Failed

**Error:** `Failed to install package: xyz`

**Solutions:**

1. **Check package name is correct**
   ```bash
   # Search for package
   apt search python3
   brew search node
   npm search typescript
   pip search requests
   ```

2. **Check package is available**
   - Different Linux distributions have different packages
   - Some packages may not be available in all repositories

3. **Check system compatibility**
   ```bash
   construct sys doctor
   ```

### Version Conflicts

**Error:** `Package version conflicts`

**Solutions:**

1. **Specify exact versions**
   ```toml
   [apt]
   packages = ["python3.11"]  # Specific version
   ```

2. **Remove conflicting packages**
   ```toml
   [apt]
   packages = ["python3.11"]  # Remove python3.12 if conflicting
   ```

3. **Use container rebuild**
   ```bash
   construct sys rebuild
   ```

### Persistent Volume Issues

**Error:** `Packages not persisting`

**Solutions:**

1. **Check persistent volume exists**
   ```bash
   docker volume ls | grep construct
   ```

2. **Rebuild persistent volume**
   ```bash
   construct sys reset
   ```

3. **Verify packages.toml is in config**
   ```bash
   cat ~/.config/construct-cli/packages.toml
   ```

## Best Practices

### ✅ DO

**1. Keep packages minimal**
- Only install what you need
- Avoid unnecessary packages
- Keep sandbox lightweight

**2. Use specific versions when needed**
- Pin versions for reproducibility
- Document version requirements in project README

**3. Test package installation**
- Install in fresh container first
- Verify package works as expected
- Document any special setup required

**4. Use appropriate package manager**
- apt for system packages
- brew for development tools
- npm for Node.js projects
- pip for Python projects

### ❌ DON'T

**1. Don't install everything**
- Avoid installing all available packages
- Keep sandbox focused on your needs

**2. Don't mix package managers unnecessarily**
- Use npm for Node.js, not apt
- Use pip for Python, not brew
- Avoid version conflicts

**3. Don't forget to update packages**
- Keep packages updated for security
- Update after changing packages.toml
- Test updates before relying on them

## Examples

### Web Development Setup

```toml
[apt]
packages = [
    "curl",
    "git",
    "build-essential"
]

[brew]
packages = [
    "node",
    "yarn"
]

[npm]
packages = [
    "typescript",
    "eslint",
    "prettier",
    "vite"
]
```

### Python Development Setup

```toml
[apt]
packages = [
    "python3",
    "python3-pip",
    "python3-venv",
    "git"
]

[pip]
packages = [
    "requests",
    "pytest",
    "black",
    "mypy",
    "flake8",
    "boto3"
]
```

### Go Development Setup

```toml
[apt]
packages = [
    "build-essential",
    "git",
    "curl"
]

[brew]
packages = [
    "go",
    "gopls",
    "gotools"
]
```

### Full-Stack Development

```toml
[apt]
packages = [
    "curl",
    "git",
    "vim",
    "jq",
    "ripgrep",
    "tmux",
    "build-essential",
    "openjdk-17-jre",
    "python3",
    "python3-pip",
    "nodejs",
    "postgresql-client",
    "redis-tools",
    "mysql-client"
]

[brew]
packages = [
    "node",
    "go",
    "python@3.11",
    "terraform",
    "gh",
    "fd",
    "ripgrep",
    "bat",
    "eza",
    "delta"
]

[npm]
packages = [
    "typescript",
    "eslint",
    "prettier",
    "serverless",
    "aws-cdk"
]

[pip]
packages = [
    "requests",
    "pytest",
    "black",
    "boto3",
    "terraform"
]
```

## Next Steps

- [Configuration Guide](CONFIGURATION.md) - Complete config reference
- [Installation Guide](INSTALLATION.md) - Platform-specific setup
- [Security Guide](SECURITY.md) - Security best practices

## Getting Help

**Package issues:**
- Check package manager documentation
- Verify package is available for your platform
- Test installation in fresh container

**Construct issues:**
```bash
construct sys doctor    # System health check
construct sys config    # Edit configuration
```

**Documentation:**
- [Package manager docs](https://docs.brew.sh/) (Homebrew)
- [npm documentation](https://docs.npmjs.com/)
- [pip documentation](https://pip.pypa.io/)
- [Debian packages](https://packages.debian.org/)

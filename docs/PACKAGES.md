# Packages Guide

Customize your Construct sandbox with user-defined packages via `packages.toml`.

## Table of Contents

- [Overview](#overview)
- [Baked Baseline and the User Layer](#baked-baseline-and-the-user-layer)
- [Restoring Tools Removed from the Image](#restoring-tools-removed-from-the-image)
- [Package Managers](#package-managers)
- [Configuration](#configuration)
- [Available Packages](#available-packages)
- [Custom Packages](#custom-packages)
- [Package Installation](#package-installation)
- [Troubleshooting](#troubleshooting)

## Overview

Construct supports installing additional packages inside the sandbox environment through `packages.toml`. This allows you to customize your development environment without rebuilding containers.

**Key features:**
- **Multiple package managers**: apt, mise, bun, npm, pip, cargo, gems
- **Baked baseline + user layer**: common tools ship in the image; `packages.toml` adds what is yours
- **Applies at guest init**: new installs run when the sandbox boots; `construct sys packages --install` applies them live
- **Custom toolchains**: Optional development tools (nix, asdf, mise, vmr, etc.)

## Baked Baseline and the User Layer

Construct ships a **baked baseline** inside the `construct-box` image: the system
toolchain (apt: awscli, podman, openjdk 25, php 8.4 + composer, ffmpeg, hugo,
neovim, and the full CLI set), vendor-repo tools (`gh`, `nodejs` 24), Go
from the official tarball, rust via the official rustup (pinned stable,
minimal profile + rustfmt/clippy), the mise github: tier (`yq`, `topgrade`,
`git-cliff`, `zola`, `tlrc`, `rtk`, `mcp-cli-ent`, `md-over-here`), the npm tier
(`typescript`, `prettier`, `@ast-grep/cli`, `yarn`, `pnpm`, `gulp-cli`,
`tailwindcss`, `vite`, `webpack`, and the agent CLIs `acpx` and `codegraph`),
jekyll, litellm, bun,
mise, asdf, and the five core agents — `claude`, `codex`, `agy`, `pi`, `opencode`
at `/usr/local/bin`. The baseline updates with image updates, not per-sandbox
installs. See docs/BREW-EXIT.md for the full package-channel mapping.

Everything you list in `packages.toml` is the **user layer**. It installs at guest
init on top of the baseline. Where each manager lands and what persists:

| Manager | Installs into | Persisted across sandbox recreations |
|---------|---------------|--------------------------------------|
| apt | system directories (sandbox disk) | no — reinstalled at next sandbox boot |
| mise | `~/.local/share/mise` (home bind) | yes |
| npm | home directory (`~/.config/construct-cli/home` bind) | yes |
| bun | `~/.bun` (home bind) | yes |
| pip | home bind | yes |

The home bind is a host directory. Sandbox recreation wipes the sandbox disk, never
your home. That is why apt additions re-pour at boot while mise/npm/bun/pip additions
survive untouched. Prefer mise for anything version-pinned.

Rules of thumb:

- **Add new tools here, do not re-list baseline tools.** Listing a baseline package
  is a no-op at best. Check `apt list --installed` and `mise ls` inside the sandbox
  before adding a name.
- **Never list the core agents under `[npm]`.** A user-layer `claude`, `codex`, `agy`,
  `pi`, or `opencode` shadows the baked binary with a bind copy that stops tracking
  image updates. Optional agents (`qwen`, `copilot`, `crush`, `cline`, ...) belong
  there; the core five do not.
- **Changes apply at the next guest init** (fresh sandbox boot) or immediately via
  `construct sys packages --install` in a running sandbox.
- **Idle-window updates keep your layer current.** With `auto_update_packages = true`
  (default), the daemon runs `update-all.sh` over the user layer right before an
  idle stop.

## Restoring Tools Removed from the Image

The image stays lean on purpose: a smaller image pulls faster and msb rejects any
single layer over 10 GiB. Tools cut from the baseline for disk reasons stay one line
away — add them to your `packages.toml` user layer:

| Tool | Removed | Why | Restore with |
|------|---------|-----|--------------|
| `llvm` (clang/clangd/lld) | 2026-09-22 image trim | ~4-7 GB; most users compile with the Debian gcc toolchain or toolchain-managed runtimes | `[apt] packages = ["clang"]` (shared libLLVM) |
| `swift` | 2026-09-22 image trim | ~2.2 GB toolchain; rarely used, heavy | swift.org tarball or the swiftly installer, via `[tools]` |
| `zig` | 2026-09-22 image trim | ~2.6 GB with its llvm dependency | `[mise] packages = ["zig"]` |
| `erlang`, `elixir`, `gleam` | 2026-09-22 image trim | ~3.5 GB with the graphics/llvm chain they pulled | `[apt] packages = ["erlang", "elixir"]`; gleam via `[mise]` |
| `qmd` | 2026-09-22 size trim | ~950 MB semantic-search backend; largest non-agent tree | `[bun] packages = ["@tobilu/qmd"]` (the qmd GGUF cache mount keeps working for user installs) |
| `fastmod` | 2026-09-22 brew exit | no release assets to fetch | `cargo install fastmod` |
| `dart` | 2026-09-22 size trim | ~640 MB, heaviest apt package; nothing in the image depends on it | Google's signed apt repo per dart.dev/get-dart: `curl -fsSL https://dl-ssl.google.com/linux/linux_signing_key.pub \| gpg --dearmor -o /usr/share/keyrings/dart.gpg && echo "deb [signed-by=/usr/share/keyrings/dart.gpg] https://storage.googleapis.com/download.dartlang.org/linux/debian stable main" \| tee /etc/apt/sources.list.d/dart.list && sudo apt-get update && sudo apt-get install -y dart` |
| `kotlin`, `scala`, `groovy`, `gradle` | 2026-09-22 brew exit | language runtimes moved on-demand | `[mise] packages = ["kotlin", "scala", ...]` |

```toml
[mise]
packages = [
  "zig",        # ziglang.org tarball via mise
]

[apt]
packages = [
  "clang",      # shared libLLVM — a fraction of the brew llvm suite
]
```

Notes:

- mise entries install into the home bind at guest init and persist across
  sandbox recreations; first use of a large toolchain downloads over the
  network once.
- The compilers stay baked: `gcc`/`g++` (build-essential) and `clang` is one
  apt line away if you need it.
- Future disk-driven removals will be recorded in this table. Check it after
  image updates if a tool you use stops resolving.

## Package Managers

### Supported Package Managers

| Manager | Description | Usage |
|---------|-------------|-------|
| **apt** | Debian packages | System libraries, CLI tools, dev toolchain |
| **mise** | Runtime and binary manager | Language runtimes, GitHub-release tools |
| **bun** | Bun package manager | JavaScript runtime and packages |
| **npm** | Node Package Manager | Node.js packages and CLIs |
| **pip** | Python Package Manager | Python packages and modules |
| **cargo** | Rust crate manager | Rust binaries and libraries |
| **gems** | Ruby gems | Ruby packages and CLIs |
| **pi** | Pi extensions | Pi coding-agent extensions via `pi install` |

### Package Manager Priority

**Installation order:**
1. apt (system packages)
2. mise (runtimes and release binaries)
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

# mise tools (runtime + GitHub-release syntax)
[mise]
packages = [
    "node@24",
    "python@3.13",
    "github:jesseduffield/lazygit@latest"
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

The lists below are illustrative. Everything in the [baked baseline](#baked-baseline-and-the-user-layer)
is already present — list only **additions** in `packages.toml`. To check what the
sandbox already has: `construct sys exec -- apt list --installed` (apt tier) or
`construct sys exec -- mise ls` (mise tier).

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

### Development Tools (mise)

**Languages and runtimes (on demand — the baked baseline covers common ones):**
```toml
[mise]
packages = [
    "kotlin",           # JVM language
    "erlang",           # or: [apt] packages = ["erlang"]
]
```

**GitHub-release tools:**
```toml
[mise]
packages = [
    "github:jesseduffield/lazygit@latest",
    "github:ajeetdsouza/zoxide@latest",
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

**mise (versioned tools):**
```toml
[mise]
packages = [
    "python@3.13",      # Version 3.13
    "node@24",          # Node 24.x
    "go@1.27",          # Go 1.27
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
1. Read your edited `packages.toml` from `~/.config/construct-cli/`
2. Regenerate the install script and apply it in the running sandbox
3. Apply again automatically on the next sandbox boot

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
volta = true           # JavaScript tool manager
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
volta = false         # Disabled
```

## Troubleshooting

### Package Installation Failed

**Error:** `Failed to install package: xyz`

**Solutions:**

1. **Check package name is correct**
   ```bash
   # Search for package
   apt search python3
   mise ls-remote node
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

### State Not Persisting

**Error:** `Packages not persisting`

**Solutions:**

1. **Check the home bind exists**
   ```bash
   ls ~/.config/construct-cli/home
   ```
   User-layer packages install into the home bind (`~/.config/construct-cli/home` on the host = `/home/construct` in the sandbox).

2. **Reset the sandbox**
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
- apt for system packages, mise for runtimes and release tools
- npm for Node.js projects
- pip for Python projects

### ❌ DON'T

**1. Don't install everything**
- Avoid installing all available packages
- Keep sandbox focused on your needs

**2. Don't mix package managers unnecessarily**
- Use npm for Node.js, not apt
- Use pip for Python, not apt
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

[mise]
packages = [
    "node@24",
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

[mise]
packages = [
    "go@1.27",
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

[mise]
packages = [
    "node@24",
    "go@1.27",
    "python@3.13",
    "terraform"
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
- [mise docs](https://mise.jdx.dev/)
- [npm documentation](https://docs.npmjs.com/)
- [pip documentation](https://pip.pypa.io/)
- [Debian packages](https://packages.debian.org/)

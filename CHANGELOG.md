# Changelog

All notable changes to Construct CLI will be documented in this file.

## [0.9.0] - 2025-12-26

### Optimized
- **Reliable Agent Installation**: Overhauled `entrypoint.sh` to prevent partial installation failures.
  - Split large `brew install` commands into categorized blocks (Core, Dev, Languages, Linters, Web/Build).
  - Unified npm package installation into a single, efficient command.
  - Implemented hash-based change detection for `entrypoint.sh` to automatically trigger re-installation when scripts are updated.
- **Smart Runtime Configuration**:
  - Optimized Linux runtime detection to avoid unnecessary user ID mapping for UID 1000 users, enabling proper permission fixups.
  - Improved `sys migrate` to ensure binary rebuilds are correctly triggered when embedded templates change.

### Fixed
- **Permission Issues**: Fixed permission errors during initial setup by allowing the container to start as root for volume ownership fixes before dropping privileges.
- **Missing Tools**: Resolved issue where tools like `golangci-lint` could be missing due to silent installation failures.
- **Build Caching**: Fixed an issue where Docker build cache would persist stale `entrypoint.sh` versions, preventing updates from being applied.

## [0.8.0] - 2025-12-25

### Added
- **Git Identity Inheritance**: Automatically propagates host git identity (`user.name` and `user.email`) to the container environment.
  - Solves commit attribution issues inside the container.
  - Enabled by default, configurable via `propagate_git_identity` in `config.toml`.
  - Safely injects values as `GIT_AUTHOR_NAME`, `GIT_AUTHOR_EMAIL`, etc., without mounting potentially incompatible host git configs.
- **Improved Shell Prompt**: Container hostname is now set to `sandbox` (was random ID) for a cleaner prompt experience: `construct@sandbox:/workspace$`.
- **Headless Login Bridge**: New `construct sys login-bridge` command to enable local browser login callbacks for headless-unfriendly agents (Codex, OpenCode with OpenAI GPT or Google Gemini).
  - Runs until interrupted and forwards `localhost` OAuth callbacks into the container.

## [0.7.0] - 2025-12-24

### Added
- **Global Agent Rules Management**: New `construct sys agents-md` command to manage rules for all supported agents in one place.
  - Interactive selection UI powered by `gum`.
  - Supports: Gemini, Qwen, OpenCode, Claude, Codex, Copilot, and Cline.
  - **Auto-Initialization**: Automatically creates missing rules files and parent directories on demand.
  - **Open All**: "Open all Agent Rules" option to quickly edit all supported agent rules at once.
  - **Context-Aware Expansion**: Automatically resolves `~` to the Construct persistent home directory (`~/.config/construct-cli/home/`) so rules are correctly applied within the container environment.
  - **Fallback UI**: Seamlessly falls back to a numeric menu if `gum` is not available.

### Changed
- **Generic Workspace Path**: Renamed the internal container mount point and working directory from `/app` to `/workspace`.
  - This reflects the project's evolution into a general-purpose sandbox for any CLI agent, not just those used for "app" development.
  - Automatically updates all embedded templates (Dockerfile, docker-compose) and runtime override generation.
  - Updated design and user documentation to reflect the new path.

### Fixed
- Improved UI grammar and messaging consistency across system commands.
- Updated `.gitignore` to prevent tracking of local debug logs while preserving them for development.

## [0.6.0] - 2025-12-23

### Added
- **Secure SSH Agent Forwarding**: Automatic detection and secure mounting of the local SSH agent into the container.
  - Supports Linux and macOS (OrbStack and Docker Desktop).
  - Implements a TCP-to-Unix bridge for robust macOS/OrbStack connectivity, bypassing common permission and socket quirks.
  - Fully configurable via `forward_ssh_agent` in `config.toml` (enabled by default).
- **SSH Key Import System**: New `construct sys ssh-import` command to securely bring host keys into Construct.
  - Interactive multi-select UI powered by `gum`.
  - Automatic permission fixing (0600) and matching `.pub`/`known_hosts` support.
  - Smart logic to skip selection if only one key is found.
- **Config Restoration**: New `construct sys restore-config` command to immediately recover from configuration backups.
- **Shell Productivity Enhancements**:
  - Automatic management of `.bash_aliases` inside the container.
  - Standard aliases included: `ll`, `la`, `l`, and color-coded `ls`/`grep`.
  - Zsh-like navigation shortcuts: `..`, `...`, `....`.
- **Improved Diagnostics**: `construct sys doctor` now reports SSH Agent connectivity and lists imported local keys.

### Changed
- **Non-Destructive Migration**: Redesigned `sys migrate` logic to be strictly additive.
  - Preserves all user comments and formatting.
  - Automatically identifies and preserves custom Claude Code aliases, moving them to a dedicated "User-defined" section.
  - Prevents TOML section duplication.
- **Unified macOS Magic Path**: Simplified container orchestration to use industry-standard magic paths for macOS container bridges.
- **Entrypoint Reliability**: Container now starts as root to perform critical permission fixes (SSH sockets, home directory ownership) before dropping to the non-privileged `construct` user via `gosu`.

### Fixed
- Fixed "Permission denied" errors when accessing mounted SSH sockets on macOS.
- Fixed "Communication failed" errors by implementing a Go-native TCP bridge for macOS.
- Full compliance with strict `golangci-lint` rules, improving overall code robustness and error reporting.
- Simplified `config.toml` template to reduce clutter and minimize merge conflicts.

## [0.5.0] - 2025-12-22

### Added
- **Self-Update Command**: `construct sys self-update` now downloads and installs the latest version directly from GitHub releases
  - Automatic platform detection (darwin/linux, amd64/arm64)
  - Prompts for confirmation when already on latest version
  - Backup and restore on failure
- **Smart Config Migration**: Config merging now only runs when template structure actually changes
  - Hash-based detection of config template changes
  - Skips unnecessary backup/merge cycles on patch version updates
  - Container templates still update on every version bump (for bug fixes)

### Changed
- **Simplified Update Check**: Version checking now uses lightweight `VERSION` file instead of GitHub API
  - Faster checks, no API rate limits
  - Download URLs constructed directly from version string
- **Install Script Improvements**:
  - Checks for existing installation before downloading
  - Prompts user if same version already installed
  - Uses `VERSION` file for remote version lookup
  - Reads local `.version` file first, falls back to querying binary
  - New `FORCE=1` env var to skip version check

### Fixed
- Version command no longer triggers config initialization
- Update check now uses proper semver comparison (was treating any version difference as "update available")

## [0.4.1] - 2025-12-22

### Added
- `make lint` now runs golangci-lint for parity with CI checks.

### Fixed
- Migration merge now skips incompatible types and validates TOML, preventing corrupted config files after upgrades.
- Improved error handling and warnings for clipboard server, daemon UI rendering, log cleanup, and shell alias flows.

## [0.4.0] - 2025-12-21

### Added
- **Automatic Migration System**: Seamless upgrades with zero user intervention
  - Version tracking via `.version` file in config directory
  - Automatic detection of version changes on startup
  - Smart detection of 0.3.0 → 0.4.0 upgrades (handles missing version file)
  - **Improved Config Merging**: New template-first merge logic that preserves comments and layout while syncing supported user values.
  - Automatic replacement of container templates with new versions
  - Automatic removal of old Docker image (forces rebuild with new Dockerfile)
  - Persistent volumes preserved (agents, packages, configurations)
  - Backup of old config created during migration (`config.toml.backup`)
  - Clear migration progress output with success/error reporting
  - New `construct sys migrate` command for manual config/template refresh (useful for debugging)
- **Expanded Container Toolchain**: Added comprehensive language support to the sandbox:
  - **Languages**: Rust, Go, Java (OpenJDK), Kotlin, Swift, Zig, Ruby, PHP, Dart, Perl, Erlang, COBOL.
  - **Build Tools**: Ninja, Gradle, UV, Composer.
  - **Utilities**: `jq`, `fastmod`, `tailwindcss` CLI.
- **Cross-Boundary Clipboard Bridge**: Unified host-container clipboard for seamless text and image pasting
  - **Secure Host-Wrapper Bridge**: A secure Go HTTP server on the host provides authenticated clipboard access to the container via ephemeral tokens.
  - **Universal Image Support**: Robust support for pasting images directly into agents across macOS, Linux, and Windows (WSL).
  - **Multi-Agent Interception**: Automatic shimming of `xclip`, `xsel`, and `wl-paste` inside the container to redirect calls to the bridge.
  - **Dependency Patching**: `entrypoint.sh` automatically finds and shims nested clipboard binaries in `node_modules` (fixes Gemini/Qwen `clipboardy` issues).
  - **Tool Emulation**: Fake `osascript` shim allows agents to use macOS-native "save image" logic while running on Linux.
  - **Smart Path Fallback**: Automatically saves host images to `.gemini-clipboard/` and returns multimodal `@path` references for agents expecting text.
  - **Runtime Code Patching**: Automatically bypasses agent-level `process.platform` checks that would otherwise disable image support on Linux.
  - **Zero Config**: Transparently handles all platform-specific clipboard complexities with no user setup required.
- **Development Installation Scripts**: New tools for local testing and debugging
  - `install-local.sh`: Full-featured install with automatic backups and verification (defaults to `~/.local/bin`)
  - `dev-install.sh`: Lightning-fast dev install for rapid iteration (no confirmations, no backups)
  - `uninstall-local.sh`: Safe uninstall with backup restoration options
  - New Makefile targets: `install-local`, `install-dev`, `uninstall-local`
- **Comprehensive Development Documentation**
  - New `DEVELOPMENT.md` with complete development workflow guide
  - Detailed installation methods, testing procedures, and troubleshooting
  - VS Code tasks configuration examples

### Changed
- **Help Text Alignment**: All CLI help descriptions now properly aligned for better readability
  - Aligned `#` comments across all help sections (sys, network, daemon, cc)
  - Consistent formatting in main help, network help, daemon help, and provider help
- **Installation Defaults**: Local installation scripts now default to `~/.local/bin` (no sudo required)
  - Users can override with `INSTALL_DIR` environment variable
  - Improved user experience for development workflows
- **Clipboard Integration Architecture**: Upgraded from manual directory sync to a secure, real-time HTTP bridge.
  - Transparent redirection of all terminal clipboard tools to the host system.
  - Integrated support for multimodal agents (Claude, Gemini, Qwen).

### Fixed
- **Runtime Package Conflicts**: Resolved naming collision in `internal/agent/runner.go`
  - Standard library `runtime` package now imported as `runtimepkg`
  - All runtime function calls updated to use proper package reference
- **Version Location**: Updated version references in documentation to reflect actual location
  - Version now correctly documented as `internal/constants/constants.go` (not `main.go`)

### Documentation
- Updated README.md with cross-boundary clipboard bridge instructions
- Added DEVELOPMENT.md with development workflow and testing guide
- Updated AGENTS.md with correct file paths and new clipboard features
- Updated DESIGN.md with detailed clipboard bridge architecture

## [0.3.0] - 2025-12-18

### Added
- **Core CLI Framework**: Single-binary CLI for running AI agents in isolated containers
  - Runtime auto-detection: macOS `container` → `podman` → `docker`
  - Embedded templates (Dockerfile, docker-compose, entrypoint, configs)
  - Self-building on first run with `construct sys init`
- **Network Isolation**: Three modes for security
  - `permissive`: Full network access (default)
  - `strict`: Custom network with domain/IP allowlist/blocklist
  - `offline`: No network access at all
  - Live UFW rule application while agents are running
- **Agent Support**: Pre-configured support for multiple AI agents
  - Claude Code, Gemini CLI, Qwen Code, GitHub Copilot CLI
  - OpenCode, Cline, OpenAI Codex
  - Agent configuration directories mounted from host
- **System Commands** (`construct sys`):
  - `init`: Initialize environment and install agents
  - `update`: Update agents to latest versions
  - `reset`: Delete volumes and reinstall
  - `shell`: Interactive shell with all agents
  - `install-aliases`: Install agent aliases to host shell
  - `version`: Show version
  - `config`: Open config in editor
  - `agents`: List supported agents
  - `doctor`: System health checks
  - `self-update`: Update construct binary
  - `update-check`: Check for available updates
- **Network Commands** (`construct network`):
  - `allow <domain|ip>`: Add to allowlist
  - `block <domain|ip>`: Add to blocklist
  - `remove <domain|ip>`: Remove rule
  - `list`: Show all rules
  - `status`: Show active UFW status
  - `clear`: Clear all rules
- **Daemon Mode** (`construct daemon`):
  - `start`: Start background container
  - `stop`: Stop background container
  - `attach`: Attach to running daemon
  - `status`: Show daemon status
- **Claude Provider Aliases** (`construct cc`):
  - Support for alternative Claude-compatible API endpoints
  - Providers: Z.AI, MiniMax, Kimi, Qwen, Mimo
  - Environment variable expansion and automatic reset
  - Configuration in `config.toml` under `[claude.cc.*]`
- **Persistent Volumes**: Agent installs persist across runs
  - `construct-agents`: Agent binaries and configs
  - `construct-packages`: Homebrew packages
  - Ephemeral containers (`--rm`) for clean host system
- **Auto-Update System**:
  - Passive background update checks (configurable interval)
  - Desktop notifications for available updates
  - Self-update command for binary upgrades
  - Version checking against GitHub releases
- **Platform Support**:
  - macOS (Intel and Apple Silicon)
  - Linux (amd64 and arm64)
  - SELinux support with automatic `:z` labels
  - Linux UID/GID mapping in generated override files
  - WSL2 compatibility

### Configuration
- Main config at `~/.config/construct-cli/config.toml`
- Runtime engine selection (auto, container, podman, docker)
- Network mode configuration with domain/IP lists
- Provider-specific environment variables
- Auto-update check settings

### Infrastructure
- Makefile with comprehensive build targets
- Cross-compilation support for all platforms
- Unit and integration test suites
- CI-ready lint and test commands
- GitHub Actions integration ready
- Install/uninstall scripts

### Documentation
- Comprehensive README.md with examples
- DESIGN.md with architecture details
- AGENTS.md for code agents working on the project
- CONTRIBUTING.md for contributors
- LICENSE.md (MIT)

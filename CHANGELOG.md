# Changelog

All notable changes to Construct CLI will be documented in this file.

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

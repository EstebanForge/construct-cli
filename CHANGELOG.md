# Changelog

All notable changes to Construct CLI will be documented in this file.

## [1.3.9] - 2026-02-24

### Fixed
- **Podman Non-Interactive Setup Runs**: Added `-T` to non-interactive compose `run` flows used by rebuild/setup/update/package install paths to avoid Podman TTY/conmon startup failures on Linux.
- **SELinux Home-Directory Rebuilds**: When SELinux labels are active and Construct is launched from the home directory, compose override generation now falls back container `working_dir` to `/projects` to prevent Linux startup failures while preserving the project mount.
- **Broken Gum Binary Fallback**: UI now validates that `gum` is executable (not only present in `PATH`) before using it, ensuring clean fallback output and readable setup error logs on Linux.

### Added
- **Regression Coverage for SELinux Home Fallback**: Added Linux runtime test coverage validating `/projects` fallback `working_dir` behavior when home-directory SELinux relabeling is skipped.

### Changed
- **Linux Startup Identity Propagation**: Linux compose runs now propagate host `UID:GID` into startup env for setup, interactive runs, daemon startup, doctor compose actions, and package/update operations.
- **Entrypoint Ownership Strategy (Linux)**: Entrypoint now supports host numeric `UID:GID` ownership/runtime mapping (when provided) while preserving `HOME=/home/construct` semantics.

### Fixed
- **Recurring Linux Home Ownership Drift**: Prevented repeated ownership drift on `~/.config/construct-cli/home` caused by container startup user/ownership mismatch across Docker and Podman flows.
- **Config Permissions Doctor Coverage**: `construct sys doctor` now reports ownership mismatch (not only writability) for Linux config directories.
- **Codex Startup Permission Loop**: Resolved repeated permission-fix prompts triggered by home mount ownership mismatch before `ct codex` startup.

### Added
- **Comprehensive Linux `doctor --fix` Remediation**: Added Linux `--fix` flow that repairs config ownership/permissions, rebuilds stale/missing image for startup fixes, recycles stale session container, and recreates daemon container.
- **Regression Coverage for Linux Ownership Fixes**: Added unit tests for ownership-state detection, linux fix flow, host identity env injection, daemon recreation/session cleanup fix paths, and template guards for host identity propagation.
- **Release Channels (`stable` / `beta`)**: Added `runtime.update_channel` to config, beta version marker support (`VERSION-BETA`), and installer channel selection (`CHANNEL=beta`) for selective prerelease adoption.
- **Semver Prerelease Comparison**: Added shared semantic version comparison logic with prerelease support for update checks and migration gating.

---

## [1.3.7] - 2026-02-22

### Added
- **Unified Test Summary (`make test`)**: Added a combined end-of-run summary that reports unit pass/fail/skip counts, unit package pass/fail counts, integration totals, and overall status.
- **Codex Regression Tests**: Added targeted tests for Codex env injection and fallback behavior, including `CODEX_HOME` presence for Codex runs, WSL fallback env injection when clipboard patching is enabled, and guard checks ensuring non-Codex agents do not inherit Codex-only env vars.
- **Attach Execution Regression Tests**: Added focused tests covering attach-session env injection (`HOME`, `PATH`, clipboard vars, Codex vars), shell fallback behavior, and Linux host-UID exec mapping.
- **Podman Compose Selection Tests**: Added unit coverage to verify command selection prefers `podman-compose` when present and falls back to `podman compose` when it is not.

### Changed
- **Test Runner Consolidation**: `make test` and `make test-ci` now run through a shared `scripts/test-all.sh` flow for consistent unit+integration reporting.
- **Color Controls for Test Summaries**: Added status-aware summary coloring (green/yellow/red) with `NO_COLOR` to disable and `FORCE_COLOR=1` to force output coloring.
- **Codex Config Home Resolution**: Force Codex runs (standard + daemon) to use `CODEX_HOME=/home/construct/.codex` so config is loaded from `/home/construct/.codex/config.toml` instead of project-relative `.codex` paths under `/projects/...`.
- **Codex Agent Env Injection Refactor**: Centralized Codex-specific run/daemon environment injection into dedicated helper functions to reduce drift across execution paths.
- **Attach Execution Path Parity**: Attach-to-running-container flows now use interactive exec with the same env protections as normal/daemon runs (construct `PATH`, `HOME=/home/construct`, clipboard vars, and agent-specific env injection).
- **Linux Non-Daemon Host UID Mapping**: Non-daemon Linux Docker agent runs now apply host `UID:GID` mapping when `exec_as_host_user=true` and force `HOME=/home/construct`.
- **Podman Compose Invocation Strategy**: Compose command resolution now uses `podman-compose` when available and otherwise falls back to `podman compose`.
- **Strict Network Naming Consistency**: Strict mode now uses a consistent `construct-net` network name across network precreate checks and compose override generation.
- **Ownership Repair Mapping**: Runtime and migration ownership repair commands now use numeric `uid:gid` mapping for broader Linux compatibility.

### Fixed
- **Entrypoint HOME/Permissions Regression (Linux Docker)**: Resolved a regression where entrypoint privilege drop could run as a raw host uid:gid without a passwd entry, causing `HOME=/` and repeated permission errors writing `~/.ssh`, `~/.bashrc`, and setup files.
- **Daemon Session Startup Reliability**: Restored reliable startup behavior for daemon-backed runs (`construct <agent>`) when host UID does not exist inside the container.
- **Linux Config Ownership Auto-Recovery**: Hardened migration/runtime permission recovery to attempt non-interactive sudo ownership repair first, with clearer remediation when elevation is unavailable.
- **Host UID Exec Fallback Messaging**: Ensured fallback warnings are visible when host UID mapping cannot be used inside the container.
- **Docker Override Host UID Injection**: Removed runtime host UID/GID env injection from generated Docker overrides to avoid reintroducing raw-UID startup regressions.
- **Codex Attach Permission Path Drift (Linux)**: Fixed attach sessions that could miss home/config env injection and fall back to project-relative `.codex` resolution.
- **Podman Runtime/Compose Mismatch**: Fixed runtime detection success followed by compose invocation failure on hosts without `podman-compose`.
- **Linux Exec Documentation Drift**: Updated docs/comments to match current behavior when host UID is missing in container `/etc/passwd` (keep host mapping and force `HOME=/home/construct`).

---

## [1.3.2] - 2026-02-20

### Added
- **Compose Network Health Check**: `construct sys doctor` now detects stale Docker Compose networks that require recreation (for example after Docker daemon/network default changes such as IPv4/IPv6 toggles).

### Fixed
- **One-Step Compose Network Recovery**: `construct sys doctor --fix` now automatically applies targeted compose network recovery by bringing down the compose stack with orphan cleanup and removing the stale compose network(s) when needed.
- **Doctor Fix Regression Safety**: Added unit coverage for compose-network fix flow (success, no-op, and failure cases) to prevent regressions.

---

## [1.3.1] - 2026-02-19

### Added
- **Issue Template Diagnostics**: Added GitHub bug report template fields requiring `construct sys doctor` output and setup/update logs.
- **Doctor Environment Visibility**: `construct sys doctor` now reports host UID/GID, container user mapping, daemon mode details, daemon mount paths, and latest update log path.
- **Setup/Update Diagnostics**: Added richer setup and update diagnostics (UID/GID, PATH, brew/npm/topgrade presence, Homebrew writability checks) plus post-run agent command verification summaries.
- **`exec_as_host_user` Mode**: Added `[sandbox].exec_as_host_user` (default `true`) to run Linux Docker exec sessions as host UID:GID for better host file ownership.

### Changed
- **Docker Linux User Mapping Strategy**: Docker overrides no longer force `user:` by default on Linux; root-bootstrap remains default and user mapping is now podman-only unless strict mode is explicitly enabled.
- **Strict Non-Root Mode Documentation**: Added explicit warnings and limitations for `[sandbox].non_root_strict` in config template, doctor output, and README.
- **NPM Global Prefix Flow**: Setup/update now configure npm global prefix earlier to reduce EACCES failures.

### Fixed
- **Agent Installation Continuation**: Hardened package install script generation so Homebrew failures do not abort later NPM agent installs.
- **Manual `user:` Override Recovery (Docker/Linux)**: Detects unsafe manual `user:` mappings in `docker-compose.override.yml`, warns users, and regenerates override safely (except when `non_root_strict=true`).
- **Entrypoint Shell Setup Noise**: Ensured `.bashrc` is created before alias-source checks to avoid missing-file warnings.
- **Daemon Exec UX**: Added targeted hinting when daemon exec exits with command-not-found.
- **Exec User Fallback Safety**: When `exec_as_host_user=true`, Construct now verifies host UID exists in container `/etc/passwd`; if not, it warns and falls back to the container default user.
- **Stable `ns-*` Alias Paths**: `sys aliases --install` now preserves stable shim paths (for example `/opt/homebrew/bin/...`) instead of resolving to versioned `Cellar`/`Caskroom` paths.

---

## [1.2.10] - 2026-02-06

### Changed
- **Design Docs Mount Model**: Updated `DESIGN.md` to document the active dual mount behavior: `/projects/<folder>` for ephemeral runs and `/workspaces/<hash>/...` for daemon multi-root runs.

### Fixed
- **Clipboard Host Command Timeouts (macOS)**: Added a timeout for `osascript` image reads in the host clipboard bridge to prevent hangs when reading image clipboard data.
- **Clipboard HTTP Server Timeouts**: Added read/write/idle timeouts to the clipboard HTTP server to avoid stuck connections.
- **Codex WSL Fallback Request Timeout**: Added `curl` connection and overall time limits in `powershell.exe` to avoid indefinite waits when fetching clipboard images.
- **Codex Daemon Workspace Path Mapping**: Added `/mnt/c/workspaces -> /workspaces` aliasing and expanded shim path handling so WSL-style fallback paths resolve correctly in daemon sessions.

---

## [1.2.8] - 2026-02-05

### Added
- **Config Defaults Check**: `construct sys doctor` now reports missing default config keys and can append them with `--fix` (with backup).
- **Construct PATH Export**: Entry point now writes a construct-managed PATH profile to stabilize login shells.
- **Non-Daemon PATH Injection**: Inject full Construct PATH for setup, container runs, and daemon exec sessions via `CONSTRUCT_PATH`.
- **PATH Sync Test**: Added a unit test to verify PATH parity across Go and template files.
- **Clipboard Patch Flag**: Added `agents.clipboard_image_patch` to enable/disable clipboard image patching and codex WSL clipboard workaround.
- **Brew Package**: Added `nano` to default Homebrew packages.

### Changed
- **Safe Defaults Overlay**: Defaults are now applied in code when config values are missing.
- **No Config Auto-Merge**: Removed automatic `config.toml` merges and config template hash tracking.

### Fixed
- **Agent PATH Parity**: Hardcoded PATH across env, entrypoint, compose, and Dockerfile to keep agent binaries available everywhere.
- **Setup + Daemon PATH Injection**: Ensure full Construct PATH is injected for setup runs and daemon exec sessions so tools are available everywhere.

---

## [1.2.3] - 2026-02-03

### Added
- **Provider Key Passthrough**: Always forward common provider API keys into containers, preferring `CNSTR_` values and falling back to unprefixed names when empty or missing. Keys: `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GEMINI_API_KEY`, `OPENROUTER_API_KEY`, `ZAI_API_KEY`, `OPENCODE_API_KEY`, `HF_TOKEN`, `KIMI_API_KEY`, `MINIMAX_API_KEY`, `MINIMAX_CN_API_KEY`.

### Fixed
- **Daemon SSH Agent Bridge**: Ensure daemon execs initialize the SSH agent proxy and expose `SSH_AUTH_SOCK` on macOS so agents can access forwarded keys.

---

## [1.2.1] - 2026-01-28

### Fixed
- **Daemon Yolo Flag**: Avoid injecting `--dangerously-skip-permissions` when execing into a root-running daemon to prevent permission errors.
- **Daemon User Enforcement (macOS)**: Run all agent execs inside the daemon as the `construct` user to avoid root exec on macOS.

### Changed
- **Run User (macOS)**: Force non-daemon agent runs to use the `construct` user to avoid root exec on macOS.

---

## [1.2.0] - 2026-01-27

### Added
- **Daemon Control Commands**: Added `construct sys daemon` subcommands to start, stop, attach, and check status of the background daemon.
- **Multi-Root Daemon Mounts**: Support for multiple host root mounts for the daemon with validation, overlap warnings, and deterministic mount hashing.

### Changed
- **Workspace Mount Path**: Removed the legacy `/workspace` fallback; project mounts now always use `/projects/<folder>` via `CONSTRUCT_PROJECT_PATH`.
- **Compose Env Injection**: `CONSTRUCT_PROJECT_PATH` is now injected automatically for all compose-based commands to keep mount paths consistent.

### Fixed
- **Daemon Workdir Mapping**: Improved daemon working directory mapping to resolve host paths against validated daemon mounts.

---

## [1.1.3] - 2026-01-26

### Added
- **Shared Entrypoint Hash Helper**: Introduced a shared script for computing and writing entrypoint hashes, used by both setup and update flows.
- **Agent Patch Script**: Centralized clipboard and agent patching into a reusable script.

### Changed
- **Update Flow Patching**: `sys update` now runs agent patching and writes the entrypoint hash immediately to avoid redundant setup on next run.
- **Mounted Update Scripts**: Update and patch scripts are mounted from the host into the container for faster iteration without rebuilds.
- **Update Fallback Warning**: Warns when falling back to `/usr/local/bin/update-all.sh` and suggests running `construct sys refresh`.

### Fixed
- **Post-Update Double Setup**: Avoids re-running full setup after updates when only patching is needed.
- **Rebuild Template Duplication**: `construct sys rebuild` no longer copies container templates twice during automatic migration + refresh.
- **Daemon Mount Working Dir**: Agent exec in the daemon now maps the host working directory to the correct mounted path.

---

## [1.1.0] - 2026-01-25

### Improved
- Multiple performance optimizations where implemented to make the CLI faster and more efficient. Now it's fast. Really fast.

### Added
- **Daemon Auto-Start Service**: Added `sys daemon install`, `sys daemon uninstall`, and `sys daemon status` for managing a background daemon service (systemd) that can auto-start on login/boot.
- **Daemon Auto-Start Config**: New `[daemon] auto_start` setting to start the daemon on first agent run for faster subsequent startups.

### Changed
- **Faster Runtime Detection**: Runtime detection now checks container, podman, and docker in parallel to reduce startup latency on multi-runtime systems.
- **Daemon Exec Fast Path**: When a daemon container is running, agents run via `exec` instead of `compose run` for much faster startup.
- **Compose Override Caching**: Docker compose override generation is cached to avoid unnecessary regeneration on repeated runs.
- **Parallel Runtime Detection**: Checks container, podman, and docker concurrently to cut detection latency by 0.5-1s.
- **Daemon Exec Path**: Agent runs reuse the warm daemon container for 2.5-7s faster startup.
- **Entrypoint Caching**: Skips expensive clipboard and agent patching when entrypoint hash is unchanged (200-800ms saved).
- **Preemptive Services**: Clipboard server and SSH bridge already start asynchronously; no added latency from sequential startup.

### Fixed
- **Daemon Staleness Guard**: Detect stale daemon containers (old image) and fall back to normal startup with clear guidance to restart the daemon.
- **Daemon Shell Exec**: `construct sys shell` now execs a default shell when attaching to a running daemon, preventing empty `exec` calls.
- **Post-Update Entrypoint Patching**: Clearing the entrypoint hash after updates ensures new agents get patched correctly.

---

## [1.0.1] - 2026-01-21

### Fixed
- **OrbStack/Podman Sudo Compatibility**: Fixed package install/update flows (`sys packages --install` and `sys update`) failing with "PAM account management error" in environments where sudo is unavailable or misconfigured (OrbStack, rootless Podman, minimal containers). Thanks @KingMob for the bug report.
  - Install scripts now detect if running as root (no sudo needed) or test sudo availability before use
  - Gracefully skips privileged apt operations when sudo unavailable instead of failing
  - Applies to both `install_user_packages.sh` (generated) and `update-all.sh` (template)

---

## [1.0.0] - 2026-01-20

### Added
- **Production Ready**: Marked Construct CLI as production ready.

---

## [0.15.11] - 2026-01-18

### Added
- **SELinux Label Control**: Added `sandbox.selinux_labels` config to enable, disable, or auto-detect SELinux mount labels.
- **Doctor Ownership Check**: Added a Linux/WSL config permissions check with a `chown` fix suggestion for `~/.config/construct-cli`.
- **Automatic Config Permission Fix**: On Linux/WSL, automatically detect and fix config directory ownership issues before runtime preparation, with clear messaging and user confirmation before running sudo.
- **Simple Progress Mode**: Added dot-based progress output for non-TTY environments and when `CONSTRUCT_SIMPLE_PROGRESS=1` is set.
- **Podman Rootless Support**: On Linux, container now runs as user (not root) for proper Podman rootless compatibility. macOS continues to use root with gosu drop.
- **Truecolor Support**: Forward host `COLORTERM` to container, defaulting to `truecolor` when unset. Fixes washed-out colors over SSH.

### Fixed
- **SELinux Home Relabeling**: Skip `:z` labels when running from the home directory to avoid relabel errors.
- **Config Write Guidance**: Emit clearer permission warnings when config-generated files cannot be written.

---

## [0.15.2] - 2026-01-15

### Added
- **Goose CLI**: Added `goose` agent, with first-run configure guidance.
- **Droid CLI**: Added `droid` agent.
- **Kilo Code CLI**: Added `kilocode` agent.
- **Yolo Mode Config**: Added `[agents]` config to enable yolo flags per-agent or globally in `config.toml`.
- **Agent Browser**: Added `agent-browser` to default npm packages and configured its post-install dependency setup. Headless browser automation CLI for AI agents. Fast Rust CLI with Node.js fallback. No MCP required.
- **LiteLLM**: Added `litellm` to default pip packages; open-source LLM gateway/SDK with a unified OpenAI-compatible API across 100+ providers, with cost/error handling and failover.
- **Post-Install Hooks**: Added `[post_install].commands` to `packages.toml`, executed after all package managers finish.

---

## [0.14.3] - 2026-01-13

### Added
- **Podman Compose**: Added `podman-compose` to default brew packages in `packages.toml`.

---

## [0.14.2] - 2026-01-11

### Changed
- **Non-Sandboxed Aliases**: `ns-` entries are now shell functions in the RC file, forwarding flags and args without installing extra files.
- **Agent Rules Bulk Replace**: `sys agents-md` now supports replacing all agent rules at once with a single pasted prompt, including Copilot frontmatter.

---

## [0.13.2] - 2026-01-08

### Fixed
- **Stale Entrypoint Detection**: Detect and prompt rebuild when the container image entrypoint is out of date to avoid repeated setup spinners.

---

## [0.13.1] - 2026-01-07

### Added
- **Pi Coding Agent**: Added support for Pi Coding Agent (`@mariozechner/pi-coding-agent`).
  - Config mounted at `/home/construct/.pi`
  - Added to default npm packages in `packages.toml`
  - Auto-creates `~/.pi/agent/auth.json` (empty object) on first run

---

## [0.12.0] - 2026-01-07

### Changed
- **Container User Authentication**: Set fixed password "construct" for the construct user to enable sudo access when running commands inside `sys shell`.
  - All automated operations (init, build, migrate, update, rebuild) remain completely passwordless via NOPASSWD sudoers configuration

---

## [0.11.6] - 2026-01-06

### Added
- **Static Site Generators**: Added Hugo and Zola to default brew packages in `packages.toml`.
- **Ruby Gems Support**: Added support for installing Ruby gems via a new `[gems]` section in `packages.toml`.
  - Includes `jekyll` as default gem
  - Integrated with package installation scripts and Topgrade updates
- **Host System Info**: Added new "Host System" check to `sys doctor` to display OS, architecture, and version details.
  - Shows macOS version and kernel on Apple/Intel Macs
  - Shows Linux distribution name and kernel version
  - Detects and displays WSL environment on Windows

### Fixed
- **Doctor SSH Keys Display**: Fixed SSH key listing in `sys doctor` to exclude non-key files (`known_hosts`, `config`, `agent.sock`, etc.).
  - Renamed check from "Local SSH Keys" to "Construct SSH Keys" for clarity
  - Keys shown are those stored in Construct's SSH directory (whether imported or generated)

---

## [0.11.5] - 2026-01-05

### Fixed
- **Agent PATH Visibility**: Fixed issue where agents (particularly `codex`) couldn't see binaries in PATH when running commands via their Bash tools.
  - Root cause: `/etc/profile` was resetting PATH when bash spawned, wiping out Homebrew paths
  - Patched `/etc/profile` in entrypoint.sh (idempotent, no image rebuild needed)
  - Centralized PATH definition in `internal/env/env.go` (DRY principle)
  - Synchronized PATH configuration across docker-compose.yml, entrypoint.sh, and env.go
  - Ensures all agent subprocesses inherit full PATH including Linuxbrew, Cargo, npm-global, etc.
- **CT Symlink Stability**: `ct` now targets the stable Homebrew path on macOS/Linux, and `sys doctor` self-heals broken Cellar-based symlinks.
- **Agent Detection in Doctor**: Agent install check now verifies binaries inside the container so Homebrew/NPM-based installs are detected correctly.
- **Rebuild Help Clarity**: `sys rebuild` help text now explicitly mentions it runs migrate before rebuilding.

---

## [0.11.3] - 2026-01-03

### Added
- **md-over-here**: Added `md-over-here` to default brew packages in `packages.toml` template.
- **Brew Installation Detection**: `self-update` and update notifications now detect if the CLI was installed via Homebrew and provide appropriate instructions (`brew upgrade estebanforge/tap/construct-cli`) instead of attempting a manual binary overwrite.

### Fixed
- **Package Installation Reliability**: Improved package install flow (`sys packages --install`) to use `run --rm` instead of `exec`, allowing it to work correctly even if The Construct is not already running.
- **Initialization Consistency**: Updated `sys init` and `sys rebuild` to always perform a full migration (syncing config and templates) before building the image.
- **Migration Messaging**: Clarified migration messages to indicate when an image is "marked for rebuild" versus being actively rebuilt.
- **Container Rebuild Reliability**: improved `sys rebuild` to force fresh container images.
  - Now stops and removes running containers and images before rebuilding (not just marking for rebuild)
  - Added support for macOS 26+ native `container` runtime management.
- **Container Image Optimization**: Updated base image to a more stable version.
  - Leaner base image means faster rebuilds and reduced storage footprint.
- **Template Synchronization**: Improved migration flow (`sys config --migrate`) to ensure binary rebuilds are correctly triggered when embedded templates change.
  - Automatic removal of old Docker image (forces rebuild with new Dockerfile)
  - New hash-based template change detection (more reliable than version checks)

---

## [0.11.2] - 2026-01-03

### Fixed
- **Version-Independent Aliases**: Fixed Homebrew alias installation to use version-independent paths.
  - Aliases now use `construct` command instead of hardcoded Cellar paths (e.g., `/opt/homebrew/Cellar/construct-cli/0.11.0/bin/construct`)
  - Aliases remain functional after Homebrew updates without reinstallation
  - Also improves portability for curl-based installations and local builds
- **Chromium Multi-Arch Support**: Fixed Puppeteer-based tools (url-to-markdown, browser automation) on arm64 hosts.
  - Installed system Chromium from Debian repos (automatically matches container architecture)
  - Configured Puppeteer to use system Chromium instead of downloading x86-64 version
  - Prevents "Dynamic loader not found: /lib64/ld-linux-x86-64.so.2" errors on Apple Silicon/arm64
  - Includes all required Chromium dependencies (fonts, GTK, NSS, etc.)


---

## [0.11.1] - 2026-01-02

### Added
- **Homebrew Installation**: Added support for installing via Homebrew (Linux & macOS) using `brew install EstebanForge/tap/construct-cli`.
- **Topgrade Integration**: Automated package updates inside Construct via [Topgrade](https://github.com/topgrade-rs/topgrade)
  - Ensures all system packages, language tools, and development utilities stay current
  - Seamless integration with Construct's containerized environment
  - Configurable via `packages.toml` for custom update policies
- **Worktrunk.dev Support**: Default installation and usage of [Worktrunk](https://worktrunk.dev/) for simultaneous agent collaboration
  - Enables multiple AI agents to work on the same codebase without conflicts
  - Provides intelligent workspace isolation and synchronization
  - Configured by default for optimal multi-agent workflows

---

## [0.10.1] - 2025-12-31

### Added
- **Cargo Package Support**: users can now install Rust-based tools and utilities via a new `[cargo]` section in `packages.toml`.
- **Centralized Debugging**: unified all debug logging under the `CONSTRUCT_DEBUG` environment variable.
  - When enabled (`CONSTRUCT_DEBUG=1`), logs are written to `~/.config/construct-cli/logs/` on the host.
  - Container-side logs (like `powershell.exe` and `clipboard-x11-sync`) are redirected to `/tmp/` for guaranteed visibility and write access.
  - Replaced legacy `CONSTRUCT_CLIPBOARD_LOG` with the new unified system.
- **Non-Sandboxed Agent Aliases**: `sys aliases --install` now creates `ns-*` prefixed aliases for agents found in PATH.
  - Example: `ns-claude`, `ns-gemini`, etc. run agents directly without Construct sandbox
  - Useful for running agents with full host access when needed
- **Alias Re-installation**: `sys aliases --update` now supports updating existing installations.
  - Detects existing aliases and offers to re-install with gum confirmation prompt
  - Automatically removes old alias block before installing fresh
  - Adds missing `ns-*` aliases when updating existing installations
- **Shell Config Backups**: automatic timestamped backups before modifying shell config files.
  - Creates `.backup-YYYYMMDD-HHMMSS` files before any changes
  - Applies to both `.zshrc`, `.bashrc`, and `.bash_profile`
  - Protects users from critical shell configuration errors

### Optimized
- **Sandbox Isolation**: sanitized the `PATH` environment variable across all templates to prevent host-side directory leakage into the sandbox.
- **Streamlined Installation**:
  - Eliminated redundant package installations by removing duplicates between APT and Homebrew phases.
  - Added `DEBIAN_FRONTEND=noninteractive` to silence UI dialogs during container setup.
- **Improved Migration Experience**: refined version detection messaging to accurately distinguish between binary upgrades, downgrades, and template-only syncs.

### Fixed
- **Codex Image Paste**: restored full image support for OpenAI Codex CLI agent after its internal mechanism changed from direct binary data to path-based references.
  - Fixed `Ctrl+V` shortcut by shimming the WSL clipboard fallback with a smart `powershell.exe` emulator that returns workspace-compatible paths.
  - Implemented `/mnt/c/projects` and `/mnt/c/tmp` symlinks within the container to ensure Codex path resolution works seamlessly across all host platforms.
  - Improved "New Flow" paste detection by allowing Codex to receive raw image paths instead of multimodal `@path` references.
- **Self-Update Reliability**: fixed a confusing state where `self-update` would report an upgrade from `0.3.0` due to intentional version file deletion.
- **Clipboard Image Paste**: Centralized file-based paste agent list (`gemini`, `qwen`, `codex`) into a single constant to prevent drift.
- **Container Rebuild Reliability**: improved `sys rebuild` to force fresh container images.
  - Now stops and removes running containers and images before rebuilding (not just marking for rebuild)
  - Added `--no-cache` flag to build command to bypass Docker layer cache
  - Added support for Apple container runtime (macOS 26+) alongside Docker and Podman
- **SSH Key Prioritization**: Enhanced SSH configuration management to ensure correct key selection order for all hosts.
  - Auto-generates `~/.ssh/config` with SSH agent support and key prioritization (`default` and `personal` keys tried first).
  - Applies to all SSH connections (GitHub, GitLab, private servers, etc.), not just specific hosts.
  - Adds RSA key algorithm support (`PubkeyAcceptedAlgorithms +ssh-rsa`) for legacy servers.
  - Falls back to physical SSH keys if present after trying agent keys.
  - Automatic updates on CLI upgrade unless user opts out with `construct-managed: false` flag.
  - Creates backup (`~/.ssh/config.backup`) before each update.

---

## [0.10.0] - 2025-12-30

### Added
- **User-Defined Package Management**: Customize your sandbox environment with `packages.toml` configuration.
  - Install additional APT, Homebrew, NPM, and PIP packages beyond the defaults.
  - Easy activation of specialized version managers: NVM, PHPBrew, Nix, Asdf, Mise, VMR, Volta, and Bun.
  - New `construct sys packages` command to quickly edit package configuration.
  - New package install command `construct sys packages --install` to apply package changes to running containers without restart.
  - Package configuration persists across updates and environments.
- **Dynamic Project Mount Paths**: Agents now mount the current host directory to `/projects/<folder_name>` instead of static `/workspace`.
  - Improves agent contextual awareness and long-term memory.
  - Dynamically calculates mount path based on current directory name.
  - Automatically updates all internal templates (Dockerfile, docker-compose) and helper scripts.
  - Preserves compatibility with non-standard folder names (spaces, special characters).

### Optimized
- **Faster Docker Builds**: Streamlined base image for significantly faster build times.
  - Reduced Dockerfile to only essential build-time dependencies.
  - Moved most packages to runtime installation during first container start.
  - Critical packages verified at startup to ensure reliability.
  - Leaner base image means faster rebuilds and reduced storage footprint.

### Improved
- **Enhanced Code Quality**: Internal refactoring and expanded test coverage.
  - Comprehensive clipboard functionality test suite.
  - Updated linter configuration for stricter code quality enforcement.
  - Improved error handling and package naming conventions.
- **Package Management Refinements**:
  - PHP extensions (PCOV) now installed via Homebrew tap instead of hardcoded scripts for better maintainability.
  - Node.js version unlocked from `node@24` to `node` for automatic latest stable version.
  - Native Node.js module compilation support with automatic compiler symlinks (`g++-11` → `g++-14`).
  - Removed conflicting `bash-completion` package to prevent installation failures.


---

## [0.9.1] - 2025-12-27

### Changed
- **Clipboard Image Pasting**: Fixed image pasting across agents with image-first handling, normalization/resize, and `@path` only for Gemini and Qwen.


---

## [0.9.0] - 2025-12-26

### Optimized
- **Reliable Agent Installation**: Overhauled `entrypoint.sh` to prevent partial installation failures.
  - Split large `brew install` commands into categorized blocks (Core, Dev, Languages, Linters, Web/Build).
  - Unified npm package installation into a single, efficient command.
  - Implemented hash-based change detection for `entrypoint.sh` to automatically trigger re-installation when scripts are updated.
- **Smart Runtime Configuration**:
  - Optimized Linux runtime detection to avoid unnecessary user ID mapping for UID 1000 users, enabling proper permission fixups.
  - Improved migration flow (`sys config --migrate`) to ensure binary rebuilds are correctly triggered when embedded templates change.

### Fixed
- **Permission Issues**: Fixed permission errors during initial setup by allowing the container to start as root for volume ownership fixes before dropping privileges.
- **Missing Tools**: Resolved issue where tools like `golangci-lint` could be missing due to silent installation failures.
- **Build Caching**: Fixed an issue where Docker build cache would persist stale `entrypoint.sh` versions, preventing updates from being applied.


---

## [0.8.0] - 2025-12-25

### Added
- **Git Identity Inheritance**: Automatically propagates host git identity (`user.name` and `user.email`) to the container environment.
  - Solves commit attribution issues inside the container.
  - Enabled by default, configurable via `propagate_git_identity` in `config.toml`.
  - Safely injects values as `GIT_AUTHOR_NAME`, `GIT_AUTHOR_EMAIL`, etc., without mounting potentially incompatible host git configs.
- **Improved Shell Prompt**: Container hostname is now set to `sandbox` (was random ID) for a cleaner prompt experience: `construct@sandbox:/workspace$`.
- **Headless Login Bridge**: New `construct sys login-bridge` command to enable local browser login callbacks for headless-unfriendly agents (Codex, OpenCode with OpenAI GPT or Google Gemini).
  - Runs until interrupted and forwards `localhost` OAuth callbacks into the container.


---

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


---

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
- **Config Restoration**: New `construct sys config --restore` command to immediately recover from configuration backups.
- **Shell Productivity Enhancements**:
  - Automatic management of `.bash_aliases` inside the container.
  - Standard aliases included: `ll`, `la`, `l`, and color-coded `ls`/`grep`.
  - Zsh-like navigation shortcuts: `..`, `...`, `....`.
- **Improved Diagnostics**: `construct sys doctor` now reports SSH Agent connectivity and lists imported local keys.

### Changed
- **Non-Destructive Migration**: Redesigned migration flow (`sys config --migrate`) logic to be strictly additive.
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


---

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


---

## [0.4.1] - 2025-12-22

### Added
- `make lint` now runs golangci-lint for parity with CI checks.

### Fixed
- Migration merge now skips incompatible types and validates TOML, preventing corrupted config files after upgrades.
- Improved error handling and warnings for clipboard server, daemon UI rendering, log cleanup, and shell alias flows.


---

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
  - New migration command `construct sys config --migrate` for manual config/template refresh (useful for debugging)
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


---

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
  - `aliases --install`: Install agent aliases to host shell
  - `version`: Show version
  - `config`: Open config in editor
  - `agents`: List supported agents
  - `doctor`: System health checks
  - `self-update`: Update construct binary
  - `check-update`: Check for available updates
- **Network Commands** (`construct network`):
  - `allow <domain|ip>`: Add to allowlist
  - `block <domain|ip>`: Add to blocklist
  - `remove <domain|ip>`: Remove rule
  - `list`: Show all rules
  - `status`: Show active UFW status
  - `clear`: Clear all rules
- **Daemon Mode** (`construct sys daemon`):
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

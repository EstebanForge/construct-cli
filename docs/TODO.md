# TODO: Move Claude from packages.go to packages.toml

## Background

Claude Code is currently hard-coded in `internal/config/packages.go` as a "Standard Tool (Always installed)" alongside Bun, Amp, imagemagick, topgrade, and cargo-update.

Other agents (agy, copilot, codex, pi, etc.) are installed via `packages.toml` (npm/brew sections).

## Current State

```go
// internal/config/packages.go:201-207
// Standard Tools (Always installed)
script += "echo 'Installing Claude Code...'\n"
script += "if [ -x \"/home/construct/.local/bin/claude\" ]; then\n"
script += "    echo \"Claude already installed; skipping.\"\n"
script += "else\n"
script += "    curl -fsSL https://claude.ai/install.sh | bash\n"
script += "fi\n\n"
```

## Proposed Change

Move to `[post_install].commands` in `internal/templates/packages.toml`:

```toml
[post_install]
commands = [
    "agent-browser install --with-deps",
    "if [ -x \"$HOME/.local/bin/droid\" ]; then echo \"Droid already installed\"; else curl -fsSL https://app.factory.ai/cli | sh; fi",
    "if [ -x \"$HOME/.opencode/bin/opencode\" ]; then echo \"OpenCode already installed\"; else curl -fsSL https://opencode.ai/install | bash; fi",
    "if [ -x \"$HOME/.local/bin/claude\" ]; then echo \"Claude already installed\"; else curl -fsSL https://claude.ai/install.sh | bash; fi",
]
```

Remove hard-coded install from `packages.go`.

## Impact Analysis

| Question | Answer |
|----------|--------|
| **New user setup break?** | ❌ NO — same install script |
| **Upgrades break?** | ❌ NO — idempotency check prevents re-install |
| **PATH issues?** | ❌ NO — `$HOME/.local/bin` already in PATH |
| **Verification loop?** | ⚠️ Already checks for `claude` command (line 401) |

## Files to Modify

1. `internal/config/packages.go` — remove lines 201-207
2. `internal/templates/packages.toml` — add to `[post_install].commands`

## Open Questions

- Is Claude a "first-class citizen" that should always be installed?
- Or is it like other agents (user-configurable)?
- Product decision needed.

## Paths Reference

| What | Path |
|------|------|
| Claude binary | `/home/construct/.local/bin/claude` |
| Claude config | `/home/construct/.claude` |
| PATH component | `$HOME/.local/bin` ✓ |
| Volume mount | `~/.config/construct-cli/home:/home/construct` ✓ |

## Notes

- No technical dependency on Claude being installed early
- Install order: Standard Tools → packages.toml (difference: Claude would run slightly later)
- Same official install script: `curl -fsSL https://claude.ai/install.sh | bash`

---

# TODO: MicroVM Engine General Availability (GA) Checklist

Items required before graduating `backend = "microvm"` out of experimental status.

## Prerequisites

- [ ] **Publish Multi-Arch Image to GHCR**: Build and publish `ghcr.io/estebanforge/construct-box:latest` and tagged versions for `linux/amd64` and `linux/arm64` via the release workflow (`.github/workflows/release.yml`). Users running the microVM backend must pull this image directly via `msb pull` without requiring a local Docker engine.
- [ ] **Verify Non-Docker Cold Start**: Validate that fresh machines with `backend = "microvm"` and `msb` installed can run `construct sys init` and agent sessions with zero Docker dependencies installed.
- [ ] **Dogfooding & Stability**: Complete dogfooding across daily workloads (Claude, Pi, Codex, Antigravity) validating bridges (SSH agent, clipboard, host exec, loopback forwarders) and project directory transitions.
- [ ] **Documentation Update**: Remove experimental warnings in `README.md`, `INSTALLATION.md`, `CONFIGURATION.md`, and `ARCHITECTURE-DESIGN.md`.

---

# TODO: Bidirectional Synchronized Workspace for MicroVM (High-Performance Monorepos)

## Background

Virtiofs passthrough over Apple Virtualization Framework carries high metadata latency per file operation (`stat`, `readdir`, `open`). In large monorepos with hundreds of thousands of files or heavy dependency trees, recursive scans by agents (`find`, `rg`, indexing) can saturate the virtualization queue.

Docker Sandboxes solves this problem for monorepos through **Synchronized File Shares** (Mutagen-based background caching), keeping the guest workspace on a native ext4 disk while synchronizing file updates to the host.

## Goal

Implement an opt-in `sync_mode = "bidirectional"` for the Construct microVM backend to give agents native Linux ext4 filesystem performance while keeping host files updated in real time.

## Proposed Architecture

```
Host Project Directory (APFS / ext4)
         │  ▲
         │  │  Bi-directional Sync (Mutagen / Vsock Daemon)
         ▼  │
MicroVM Guest `/workspace` (Native ext4 Virtual Disk)
         ▲
         │  Full Native I/O Speed
AI Agent (Pi, Claude, Codex, Antigravity)
```

## Implementation Plan

1. **Configuration Knob**:
   - Add `[sandbox] sync_mode = "virtiofs"` (default) or `"bidirectional"` to `internal/config/config.go` and `internal/templates/config.toml`.
2. **Guest Storage Layout**:
   - When `sync_mode = "bidirectional"`, omit the host bind mount for `/workspace` in `msbSandboxMounts`.
   - Allocate `/workspace` directly on the microVM's virtual disk.
3. **Synchronization Transport**:
   - Establish a bi-directional file synchronization bridge between the host CLI process and the guest environment over vsock or guest SSH bridge.
   - Use an embedded synchronization agent (such as Mutagen) or a lightweight bidirectional rsync/inotify watcher.
4. **Lifecycle & Synchronization Stages**:
   - **Pre-flight**: Perform an initial seed synchronization from host to guest `/workspace` before starting the agent session.
   - **Runtime**: Stream incremental file modifications bi-directionally during the agent session.
   - **Teardown**: Perform a final sync flush from guest to host before detaching or shutting down.
5. **Ignore & Pruning Rules**:
   - Automatically exclude heavy cache directories and binary artifacts (`node_modules/`, `.git/objects/`, target builds) from bi-directional host sync to minimize synchronization overhead.

## Success Criteria

- Recursive file operations (`find /workspace`, `rg`, `git status`) inside the microVM execute at native ext4 speed with zero Virtiofs queue lag.
- Files created or edited by the agent inside `/workspace` appear immediately on the host filesystem.
- Zero risk of microVM freeze when running agents across large monorepos.

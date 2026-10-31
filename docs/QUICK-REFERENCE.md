# Quick Reference

Everyday commands, examples, and PATH shims. Moved out of the README to keep it lean; nothing was dropped.

## Quick Start

```bash
# First-time setup (prepares the sandbox image, installs agents)
construct sys init

# Run an agent
construct claude "Help me refactor this function"

# Use PATH shims (after installation)
construct sys shims --install
claude "Debug my API code"  # Now available as short command
```

## Common Examples

```bash
# Strict network isolation (allowlist only)
construct claude -ct-n strict "Review my code"

# Offline run (no network)
construct agy --ct-network offline "Explain this code"

# Update all agents
construct sys update
construct sys self-update      # Update the construct binary itself

# Daemon sandbox (microVM backend)
construct sys daemon status    # Sandbox state + live sessions
construct sys daemon recreate  # Stop + wipe + cold-create (full reset)

# Install custom packages
construct sys packages --install

# Edit configuration
construct sys config

# System health check
construct sys doctor
```

## CLI Reference

```bash
# System commands
construct sys init              # First-time setup
construct sys doctor           # Health check
construct sys config           # Edit configuration
construct sys update           # Update agents
construct sys exec -- <cmd>    # Run command inside the running sandbox
construct sys reset            # Reset everything (OCI backends; under msb: sys daemon recreate)

# Agent commands
construct <agent>              # Run an agent (e.g., construct claude, construct agy)
construct sys shims            # Manage PATH shims (sandboxed + ns-) for agents
construct sys agents-md        # Manage AGENTS.md rules
construct sys agents push        # Push host agent configs into Construct [--agents] [--force] [--dry-run] [--yes]
construct sys pull               # Pull latest construct-box image from GHCR

# Development
construct sys rebuild          # Rebuild the sandbox image
construct sys config --migrate # Migrate configuration
construct --help               # Show all commands
```

## PATH shims: agents in harnesses and non-shell callers

Shell aliases only exist inside an interactive shell (the old alias system
was replaced by shims). Tools that manage coding
agents (orchestrators such as Paseo, IDE extensions, CI wrappers) resolve the
agent binary on PATH or spawn it directly without a shell, so they never see
aliases — they run the bare host binary. For users who always want agents
inside the sandbox, shims also install an `ns-<agent>` executable that runs
the real host binary directly (non-sandboxed), and installing removes the
legacy managed alias block from your shell config:

```bash
construct sys shims --install        # writes real executables (default ~/.local/bin)
pi --version                          # sandboxed via Construct
ns-pi --version                       # real host binary, no sandbox
construct sys shims --list           # show state
construct sys shims --uninstall      # remove (only touches files it wrote)
construct sys shims --remove-aliases # only clean up legacy shell aliases
```

Shims exec `construct <slug>` with all arguments; stdin/stdout pass through
unchanged, so agents in RPC modes (e.g. `pi --mode rpc`) keep streaming JSON
on stdout. Host path arguments passed by orchestrators (pi `--extension`,
`--mcp-config`, `--session`) are staged into the construct home and rewritten
to their container paths automatically, so harness-driven agents run sandboxed
with their bridges intact. Harnesses that accept an explicit agent binary
(e.g. Paseo's `PI_COMMAND`) can also point straight at the shim file. Full
setup guide for orchestrators (daemon mounts, PATH, troubleshooting):
[HARNESSES.md](HARNESSES.md).

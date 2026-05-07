# TODO: Move Claude from packages.go to packages.toml

## Background

Claude Code is currently hard-coded in `internal/config/packages.go` as a "Standard Tool (Always installed)" alongside Bun, Amp, imagemagick, topgrade, and cargo-update.

Other agents (gemini, copilot, codex, pi, etc.) are installed via `packages.toml` (npm/brew sections).

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

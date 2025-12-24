# AGENTS.md

Guidance for code agents working in this repository.

## Project Snapshot
- **Purpose**: Single-binary CLI that launches an isolated container preloaded with AI agents, with optional network isolation.
- **Core entrypoint**: `cmd/construct/main.go` (embeds templates, handles `sys/network/daemon` namespaces, runtime detection, build/run/update/reset).
- **Templates**: `internal/templates/` (Dockerfile, docker-compose, entrypoint, update-all, network-filter, config, clipboard scripts).
- **Scripts**: `scripts/` (install, reset helpers, integration tests, clipboard sync).
- **Version**: Defined in `internal/constants/constants.go` as `0.4.0`.

## Build & Test
```bash
make build           # build binary
go build -o construct

make test            # unit + integration
make test-unit       # unit only
make test-integration# integration only
make lint            # format + go vet
make cross-compile   # all platforms
```

## Run & Usage
- First-time setup: `construct sys init` (writes embedded templates, builds image, installs agents, may create `ct` alias).
- Run an agent: `construct <agent> ...` (or `ct <agent> ...` if alias exists).
- Shell: `construct sys shell`.
- Network modes: `--ct-network` / `-ct-n` with `permissive | strict | offline` (also configurable in `~/.config/construct-cli/config.toml`).
- Agent configs mount from host: `~/.config/construct-cli/agents-config/<agent>/`.

## Installed Agents (inside container)
- claude (Claude Code)
- gemini (Gemini CLI)
- qwen (Qwen Code)
- copilot (GitHub Copilot CLI)
- opencode
- cline
- codex (OpenAI Codex CLI)
- Aliases in entrypoint: `zai`/`glm` (Claude with alt endpoints), `minimax` placeholder.

## Installed Tools & Utilities (inside container)
- url-to-markdown-cli-tool
- ripgrep, bat, fzf, eza, zoxide, tree, httpie, gh, neovim, uv, prettier
- Programming languages: Go, Rust, Python, Node.js, Java, PHP, Swift, Zig, Kotlin, Lua, Ruby, Dart, Perl, Erlang
- Cloud tools: AWS CLI, Terraform
- Development utilities: git-delta, git-cliff, shellcheck, yamllint, webpack, vite

## Key Behaviors
- Runtime detection order: macOS `container` → `podman` → macOS OrbStack's docker → traditional docker (Docker CE/Docker Desktop).
- Volumes: `construct-agents` and `construct-packages` persist installs; containers run with `--rm`.
- Network: strict mode creates `construct-net` and applies UFW rules; override compose via `docker-compose.override.yml`.
- Update script (`internal/templates/update-all.sh`): apt upgrade, claude installer, mcp-cli-ent installer, `brew update/upgrade/cleanup`, `npm update -g`.

## Notes for Agents
- Do not assume Homebrew auto-updates; `HOMEBREW_NO_AUTO_UPDATE=1` is set in the Dockerfile.
- Keep changes ASCII unless necessary; avoid destructive git commands; prefer `apply_patch` for edits.
- When adding flags, maintain `--ct-*` prefix to avoid conflicts with agent flags.

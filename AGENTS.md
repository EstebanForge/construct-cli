# AGENTS.md

## Build & Test
- Build: `make build` or `go build -o construct`
- Lint: `make lint` (fmt, vet, golangci-lint)
- Test all: `make test` (unit + integration)
- Unit only: `make test-unit` or `go test ./internal/...`
- Integration only: `make test-integration`
- Single test: `go test -run TestName ./internal/path/...`
- Single package: `go test -v ./internal/config`
- Coverage: `make test-coverage` (outputs `coverage.html`)

## Code Style
- Format: `gofmt` enforced; imports sorted by `goimports` (stdlib → third-party → internal)
- Naming: MixedCaps (no underscores), Uppercase=exported, lowercase=unexported
- Errors: Always check, use `fmt.Errorf("context: %w", err)` for wrapping
- Comments: `// Package name` at top, godoc for exported funcs
- Interfaces: Single-method interfaces end with `-er` suffix
- Testing: `TestXxx` funcs, `BenchmarkXxx` for benches
- Line length: No limit, let `gofmt` wrap

## Version Bumping
- **NEVER** modify the `VERSION` file - it's managed by GitHub Actions
- When asked to bump version: update `internal/constants/constants.go` only
- When asked to add CHANGELOG entry: add new section with current version from constants.go
- The `VERSION` file is automatically updated during the release process

## Adding CLI Agents
- Add npm package in `internal/templates/packages.toml` under `[npm].packages`.
- Register agent mount in `internal/agent/agent.go` (Name, Slug, ConfigPath).
- Register AGENTS.md rules path in `internal/sys/memories.go` and update `internal/sys/memories_test.go`.
- Update help list in `internal/ui/help.go`.
- Update docs in `README.md` under "Available AGENTS".
- If the agent needs setup commands, add them in `[post_install].commands` in `internal/templates/packages.toml`.
- If the agent requires first-run setup that should not be automated, gate the run in `internal/agent/runner.go` and use a marker file under Construct home (e.g., `~/.config/<agent>/.construct_configured`) to prompt once and record completion.

## Agent Additions Log
- Kilo Code CLI
  - Command: `npm install -g @kilocode/cli` (run as `kilocode`)
  - Rules path: `~/.kilocode/rules/AGENTS.md`
  - Files updated: `internal/templates/packages.toml`, `internal/agent/agent.go`, `internal/sys/memories.go`, `internal/sys/memories_test.go`, `internal/ui/help.go`, `README.md`

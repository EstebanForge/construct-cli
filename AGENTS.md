# AGENTS.md

## Build & Test
- Build: `make build` (outputs `bin/construct`, unsigned local build)
- Sign (optional, macOS): `make sign` or `make build-signed`
- Lint: `make lint` (golangci-lint)
- Full checks: `make check` or `./scripts/checks.sh` (`fmt -> vet -> lint -> test -> build`)
- CI alias: `make ci` (same as `make check`)
- Test: `make test` (`go mod download`, `go mod verify`, then `go test ./...`; `-race` when CGO is enabled)
- Unit only: `make test-unit` or `go test ./internal/...`
- Integration only: `make test-integration`
- Single test: `go test -run TestName ./internal/path/...`
- Single package: `go test -v ./internal/config`
- Coverage: `make test-coverage` (outputs `coverage.html`)

## Code Style
- Format: `make fmt` (`go fmt ./...`, then `goimports -w .` if installed)
- Naming: MixedCaps (no underscores), Uppercase=exported, lowercase=unexported
- Errors: Always check, use `fmt.Errorf("context: %w", err)` for wrapping
- Comments: `// Package name` at top, godoc for exported funcs
- Interfaces: Single-method interfaces end with `-er` suffix
- Testing: `TestXxx` funcs, `BenchmarkXxx` for benches
- Line length: No limit, let `gofmt` wrap

## PATH Construction
- PATH is hardcoded and must be kept in sync across these files:
- `internal/env/env.go` (BuildConstructPath)
- `internal/templates/entrypoint.sh`
- `internal/templates/docker-compose.yml`
- `internal/templates/Dockerfile`

## Version Bumping
- **NEVER** modify the `VERSION` file - it's managed by GitHub Actions
- **NEVER** modify the `VERSION-BETA` file manually - it's managed by GitHub Actions for prereleases
- When asked to bump version: update `internal/constants/constants.go` only
- When asked to add CHANGELOG entry: add new section with current version from constants.go
- `VERSION` is updated by release workflow for stable tags (e.g. `1.3.8`)
- `VERSION-BETA` is updated by release workflow for prerelease tags (e.g. `1.3.9-beta.1`)
- Version strings and release tags are plain semver/prerelease values with **no** `v` prefix (use `1.4.0-beta.3`, never `v1.4.0-beta.3`)
- Keep `internal/constants/constants.go` version exactly aligned with the tag being released (stable or prerelease), or `make release` fails `check-version`
- Stable users track `VERSION`; beta users track `VERSION-BETA` when `runtime.update_channel = "beta"`

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

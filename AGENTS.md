# AGENTS.md

## Build & Test
- Build: `make build` or `go build -o construct`
- Lint: `make lint` (fmt, vet, golangci-lint)
- Test all: `make test` (unit + integration)
- Unit only: `make test-unit` or `go test ./internal/...`
- Single test: `go test -run TestName ./internal/path/...`
- Single package: `go test -v ./internal/config`

## Code Style
- Format: `gofmt` enforced; imports sorted by `goimports` (stdlib → third-party → internal)
- Naming: MixedCaps (no underscores), Uppercase=exported, lowercase=unexported
- Errors: Always check, use `fmt.Errorf("context: %w", err)` for wrapping
- Comments: `// Package name` at top, godoc for exported funcs
- Interfaces: Single-method interfaces end with `-er` suffix
- Testing: `TestXxx` funcs, `BenchmarkXxx` for benches
- Line length: No limit, let `gofmt` wrap

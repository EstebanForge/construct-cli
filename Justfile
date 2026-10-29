default:
    @just --list

# Build construct binary
build:
    make build

# Format Go code
fmt:
    make fmt

# Run go vet
vet:
    make vet

# Run golangci-lint
lint:
    make lint

# Run all tests
test:
    make test

# Run unit tests only
test-unit:
    make test-unit

# Full check pipeline (fmt -> vet -> lint -> test -> build)
check:
    make check

# Local CI gate
ci: check

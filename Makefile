.PHONY: help build sign build-signed release-sign notarize-release test test-ci test-unit test-unit-ci test-integration clean clean-docker clean-all install install-local install-dev uninstall uninstall-local release cross-compile check ci lint fmt vet

# Variables
BINARY_NAME := construct
ALIAS_NAME := ct
VERSION := $(shell grep 'Version.*=' internal/constants/constants.go | sed 's/.*"\(.*\)".*/\1/')
VERSION_FILE := $(shell cat VERSION)
BUILD_DIR := bin
BINARY_PATH := $(BUILD_DIR)/$(BINARY_NAME)
DIST_DIR := dist

# Go parameters
GOCMD := go
GOBUILD := $(GOCMD) build
GOCLEAN := $(GOCMD) clean
GOTEST := $(GOCMD) test
GOGET := $(GOCMD) get
GOMOD := $(GOCMD) mod
GOFMT := $(GOCMD) fmt
GOVET := $(GOCMD) vet
GOLANGCI_LINT := golangci-lint

# Build flags
LDFLAGS := -ldflags "-s -w"
SIGN_IDENTITY ?= -

# Default target
.DEFAULT_GOAL := help

help: ## Show this help message
	@echo "The Construct CLI - Build System"
	@echo ""
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  %-20s %s\n", $$1, $$2}'

check-version: ## Ensure VERSION file matches constants.Version
	@const_ver="$(VERSION)"; file_ver="$(VERSION_FILE)"; \
	if [ "$$const_ver" != "$$file_ver" ]; then \
		echo "Version mismatch: internal/constants/constants.go=$$const_ver VERSION=$$file_ver"; \
		exit 1; \
	fi

build: ## Build the binary
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p $(BUILD_DIR)
	$(GOBUILD) $(LDFLAGS) -o $(BINARY_PATH) ./cmd/construct
	@echo "✓ Built: $(BINARY_PATH)"

sign: build ## Ad-hoc sign binary on macOS
	@if [ "$$(uname)" = "Darwin" ]; then \
		codesign -s "$(SIGN_IDENTITY)" -f $(BINARY_PATH) 2>/dev/null || true; \
		echo "✓ Signed: $(BINARY_PATH)"; \
	else \
		echo "ℹ️  Skipping sign (non-macOS)"; \
	fi

build-signed: build sign ## Build and sign (macOS)

release-sign: ## Sign macOS release binaries in dist/ (optional; set RELEASE_SIGN=1)
	@if [ "$${RELEASE_SIGN:-0}" != "1" ]; then \
		echo "ℹ️  RELEASE_SIGN!=1; skipping release signing"; \
	elif [ "$$(uname)" != "Darwin" ]; then \
		echo "ℹ️  Release signing requires a macOS runner; skipping"; \
	else \
		for bin in "$(DIST_DIR)/$(BINARY_NAME)-darwin-amd64" "$(DIST_DIR)/$(BINARY_NAME)-darwin-arm64"; do \
			if [ -f "$$bin" ]; then \
				codesign -s "$(SIGN_IDENTITY)" -f "$$bin"; \
				echo "✓ Signed $$bin"; \
			else \
				echo "ℹ️  Missing $$bin (skip)"; \
			fi; \
		done; \
	fi

notarize-release: ## Notarize release artifacts (optional; set RELEASE_NOTARIZE=1)
	@if [ "$${RELEASE_NOTARIZE:-0}" != "1" ]; then \
		echo "ℹ️  RELEASE_NOTARIZE!=1; skipping notarization"; \
	elif [ "$$(uname)" != "Darwin" ]; then \
		echo "✗ Notarization requires macOS runner"; \
		exit 1; \
	elif [ -x "./scripts/notarize-release.sh" ]; then \
		./scripts/notarize-release.sh; \
	else \
		echo "✗ scripts/notarize-release.sh not found/executable"; \
		exit 1; \
	fi

test: ## Run tests (deps + verify + go test)
	@echo "Downloading dependencies..."
	$(GOMOD) download
	@echo "Verifying dependencies..."
	$(GOMOD) verify
	@echo "Running tests..."
	@if [ "$$($(GOCMD) env CGO_ENABLED)" = "1" ]; then \
		$(GOTEST) -v -race ./...; \
	else \
		echo "CGO disabled; running tests without -race"; \
		$(GOTEST) -v ./...; \
	fi
	@echo "✓ Tests passed"

test-unit: ## Run Go unit tests
	@echo "Running unit tests..."
	$(GOTEST) -v -race -coverprofile=coverage.out ./internal/...
	@echo "✓ Unit tests passed"

test-ci: ## Run CI tests
	@$(MAKE) --no-print-directory test

test-unit-ci: ## Run Go unit tests (CI fast)
	@echo "Running unit tests (CI fast)..."
	$(GOTEST) -v ./internal/...
	@echo "✓ Unit tests passed"

test-integration: build ## Run integration tests
	@echo "Running integration tests..."
	@./scripts/integration.sh ./$(BINARY_PATH)
	@echo "✓ Integration tests passed"

test-coverage: ## Run tests with coverage report
	@echo "Generating coverage report..."
	$(GOTEST) -race -coverprofile=coverage.out -covermode=atomic ./internal/...
	$(GOCMD) tool cover -html=coverage.out -o coverage.html
	@echo "✓ Coverage report generated: coverage.html"

bench: ## Run benchmarks
	@echo "Running benchmarks..."
	$(GOTEST) -bench=. -benchmem ./internal/...

clean: ## Clean build artifacts
	@echo "Cleaning..."
	$(GOCLEAN)
	rm -f $(BINARY_NAME)
	rm -f $(BINARY_PATH)
	rm -rf $(BUILD_DIR) $(DIST_DIR)
	rm -f coverage.out coverage.html
	@echo "✓ Cleaned"

clean-docker: ## Clean all Docker resources (containers, images, volumes, networks)
	@echo "Cleaning Docker resources..."
	@./scripts/reset-environment.sh
	@echo "✓ Docker resources cleaned"

clean-all: clean clean-docker ## Clean everything (build artifacts + Docker + config)
	@echo "Cleaning everything (including config)..."
	@./scripts/reset-environment.sh --all
	@echo "✓ Full cleanup complete"

install: build ## Install binary and ct alias to /usr/local/bin
	@echo "Installing $(BINARY_NAME) to /usr/local/bin..."
	@sudo cp $(BINARY_PATH) /usr/local/bin/$(BINARY_NAME)
	@sudo ln -sf /usr/local/bin/$(BINARY_NAME) /usr/local/bin/$(ALIAS_NAME)
	@echo "✓ Installed: /usr/local/bin/$(BINARY_NAME)"
	@echo "✓ Alias: /usr/local/bin/$(ALIAS_NAME)"
	@echo ""
	@echo "Verify with: construct version && ct version"

uninstall: ## Uninstall binary from /usr/local/bin
	@echo "Uninstalling $(BINARY_NAME)..."
	@sudo rm -f /usr/local/bin/$(BINARY_NAME)
	@sudo rm -f /usr/local/bin/$(ALIAS_NAME)
	@echo "✓ Uninstalled"

install-local: ## Install to ~/.local/bin with backup and verification (no sudo)
	@./scripts/install-local.sh

install-dev: ## Quick dev install to ~/.local/bin (no backup, no confirmations)
	@./scripts/dev-install.sh

uninstall-local: ## Uninstall from ~/.local/bin with backup options
	@./scripts/uninstall-local.sh

deps: ## Download dependencies
	@echo "Downloading dependencies..."
	$(GOMOD) download
	$(GOMOD) tidy
	@echo "✓ Dependencies updated"

fmt: ## Format Go code
	@echo "Formatting code..."
	$(GOFMT) ./...
	@if command -v goimports >/dev/null 2>&1; then \
		goimports -w .; \
	else \
		echo "goimports not found; skipping import formatting"; \
		echo "Install with: go install golang.org/x/tools/cmd/goimports@latest"; \
	fi
	@echo "✓ Code formatted"

vet: ## Run go vet
	@echo "Running go vet..."
	$(GOVET) ./...
	@echo "✓ Vet passed"

lint: ## Run linters
	@echo "Running golangci-lint..."
	@command -v $(GOLANGCI_LINT) >/dev/null 2>&1 || (echo "golangci-lint not installed. Install with: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest" && exit 1)
	$(GOLANGCI_LINT) run --timeout=5m
	@echo "✓ Linting complete"

check: ## Run full checks (fmt, vet, lint, test, build)
	@echo "==> make fmt"
	@$(MAKE) --no-print-directory fmt
	@echo "==> make vet"
	@$(MAKE) --no-print-directory vet
	@echo "==> make lint"
	@$(MAKE) --no-print-directory lint
	@echo "==> make test"
	@$(MAKE) --no-print-directory test
	@echo "==> make build"
	@$(MAKE) --no-print-directory build
	@echo "✓ Full checks complete"

cross-compile: ## Build for all platforms
	@echo "Cross-compiling for all platforms..."
	@mkdir -p $(DIST_DIR)

	@echo "Building for macOS (Apple Silicon)..."
	GOOS=darwin GOARCH=arm64 $(GOBUILD) $(LDFLAGS) -o $(DIST_DIR)/$(BINARY_NAME)-darwin-arm64 ./cmd/construct

	@echo "Building for macOS (Intel)..."
	GOOS=darwin GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o $(DIST_DIR)/$(BINARY_NAME)-darwin-amd64 ./cmd/construct

	@echo "Building for Linux (amd64)..."
	GOOS=linux GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o $(DIST_DIR)/$(BINARY_NAME)-linux-amd64 ./cmd/construct

	@echo "Building for Linux (arm64)..."
	GOOS=linux GOARCH=arm64 $(GOBUILD) $(LDFLAGS) -o $(DIST_DIR)/$(BINARY_NAME)-linux-arm64 ./cmd/construct

	@echo "✓ Cross-compilation complete"
	@ls -lh $(DIST_DIR)/

release: check-version clean test cross-compile ## Create release build
	@echo "Creating release archives..."
	@mkdir -p $(DIST_DIR)

	@cd $(DIST_DIR) && \
		tar -czf $(BINARY_NAME)-darwin-arm64-$(VERSION).tar.gz $(BINARY_NAME)-darwin-arm64 && \
		tar -czf $(BINARY_NAME)-darwin-amd64-$(VERSION).tar.gz $(BINARY_NAME)-darwin-amd64 && \
		tar -czf $(BINARY_NAME)-linux-amd64-$(VERSION).tar.gz $(BINARY_NAME)-linux-amd64 && \
		tar -czf $(BINARY_NAME)-linux-arm64-$(VERSION).tar.gz $(BINARY_NAME)-linux-arm64

	@cd $(DIST_DIR) && \
		shasum -a 256 *.tar.gz > checksums.txt

	@echo "✓ Release $(VERSION) ready in $(DIST_DIR)/"
	@echo ""
	@echo "Release files:"
	@ls -lh $(DIST_DIR)/*.tar.gz
	@echo ""
	@cat $(DIST_DIR)/checksums.txt

version: ## Show version
	@echo "$(BINARY_NAME) version $(VERSION)"

run: build ## Build and run
	./$(BINARY_PATH)

dev: ## Development mode - build and init
	@$(MAKE) build
	@./$(BINARY_PATH) sys init
	@echo "✓ Development environment ready"

ci: check ## Run CI checks (full pipeline)
	@echo "✓ CI checks passed"

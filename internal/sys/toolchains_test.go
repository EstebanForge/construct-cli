package sys

import (
	"context"
	"testing"

	"github.com/EstebanForge/construct-cli/internal/config"
	"github.com/EstebanForge/construct-cli/internal/toolchains"
)

func TestHandleToolchainsCommand(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	origFetch := fetchToolchainFn
	fetchToolchainFn = func(_ context.Context, _ toolchains.ToolchainID, _ toolchains.FetchOptions) error {
		return nil
	}
	defer func() { fetchToolchainFn = origFetch }()

	// Should not panic on list or default
	HandleToolchainsCommand([]string{"list"})
	HandleToolchainsCommand(nil)
	HandleToolchainsCommand([]string{"help"})
	HandleToolchainsCommand([]string{"fetch"})
	HandleToolchainsCommand([]string{"always"})
	HandleToolchainsCommand([]string{"always", "list"})
}

func TestHandleAlwaysCommand(t *testing.T) {
	origFetch := fetchToolchainFn
	fetchToolchainFn = func(_ context.Context, _ toolchains.ToolchainID, _ toolchains.FetchOptions) error {
		return nil
	}
	defer func() { fetchToolchainFn = origFetch }()

	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// List on empty
	HandleAlwaysCommand([]string{"list"})

	// Add toolchains (including case-insensitive and invalid)
	HandleAlwaysCommand([]string{"add", "Go", "rust", "invalid"})

	cfg, _, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load failed: %v", err)
	}
	if len(cfg.Toolchains.Always) != 2 || cfg.Toolchains.Always[0] != "go" || cfg.Toolchains.Always[1] != "rust" {
		t.Fatalf("expected [go rust], got %v", cfg.Toolchains.Always)
	}

	// Remove toolchain
	HandleAlwaysCommand([]string{"remove", "RUST"})
	cfg, _, err = config.Load()
	if err != nil {
		t.Fatalf("config.Load failed: %v", err)
	}
	if len(cfg.Toolchains.Always) != 1 || cfg.Toolchains.Always[0] != "go" {
		t.Fatalf("expected [go], got %v", cfg.Toolchains.Always)
	}

	// Direct add shortcut with uppercase
	HandleAlwaysCommand([]string{"Node"})
	cfg, _, err = config.Load()
	if err != nil {
		t.Fatalf("config.Load failed: %v", err)
	}
	if len(cfg.Toolchains.Always) != 2 || cfg.Toolchains.Always[1] != "node" {
		t.Fatalf("expected [go node], got %v", cfg.Toolchains.Always)
	}

	// Unknown action should not crash
	HandleAlwaysCommand([]string{"unknown_action"})
}

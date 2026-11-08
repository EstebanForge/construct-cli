package sys

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/EstebanForge/construct-cli/internal/config"
	"github.com/EstebanForge/construct-cli/internal/toolchains"
	"github.com/EstebanForge/construct-cli/internal/ui"
)

var fetchToolchainFn = toolchains.FetchToolchain

// HandleToolchainsCommand handles the `construct sys toolchains` CLI command.
func HandleToolchainsCommand(args []string) {
	if len(args) > 0 {
		switch args[0] {
		case "list":
			ListToolchains()
			return
		case "fetch", "install", "pull":
			FetchToolchains(args[1:])
			return
		case "always":
			HandleAlwaysCommand(args[1:])
			return
		case "--help", "-h", "help":
			printToolchainsHelp()
			return
		default:
			fmt.Fprintf(os.Stderr, "Unknown toolchains command: %s\n", args[0])
			printToolchainsHelp()
			os.Exit(1)
		}
	}

	ListToolchains()
}

func printToolchainsHelp() {
	fmt.Println("Usage: construct sys toolchains [list|fetch|always] [args...]")
	fmt.Println()
	fmt.Println("Manage and inspect modular read-only toolchains.")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  list                         List detected, always-loaded, and cached toolchains (default)")
	fmt.Println("  fetch [--always] [all|<id>...] Fetch and cache toolchains from GHCR (flags: --force, --always)")
	fmt.Println("  always [list|add|remove]     Configure toolchains loaded always regardless of project markers")
	fmt.Println()
	fmt.Println("Supported toolchain IDs:")
	fmt.Println("  go, rust, node, python, ruby, php, java, kotlin, dart, swift")
}

// ListToolchains lists detected, configured always-loaded, and cached toolchains.
func ListToolchains() {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	detected := toolchains.DetectToolchains(cwd)
	cacheDir := toolchains.CacheDir()
	available := toolchains.AvailableToolchains(cacheDir)

	var alwaysList []string
	var configErr error
	if cfg, _, err := config.Load(); err == nil && cfg != nil {
		alwaysList = cfg.Toolchains.Always
	} else {
		configErr = err
	}

	fmt.Println("Construct Modular Toolchains (v3.0):")
	fmt.Println()

	fmt.Printf("Host Cache Directory: %s\n\n", cacheDir)

	fmt.Println("Detected in Current Project:")
	if len(detected) == 0 {
		fmt.Println("  (none)")
	} else {
		for _, tc := range detected {
			fmt.Printf("  • %s (mount: /opt/toolchains/%s, artifact: %s)\n",
				tc, tc, toolchains.DistributionArtifact(tc))
		}
	}
	fmt.Println()

	fmt.Println("Configured Always Loaded (config.toml):")
	if configErr != nil {
		fmt.Printf("  (error reading config: %v)\n", configErr)
	} else if len(alwaysList) == 0 {
		fmt.Println("  (none - configure with: construct sys toolchains always add <id...>)")
	} else {
		for _, name := range alwaysList {
			fmt.Printf("  • %s (mount: /opt/toolchains/%s)\n", name, name)
		}
	}
	fmt.Println()

	fmt.Println("Installed in Host Cache:")
	if len(available) == 0 {
		fmt.Println("  (none - toolchains will use base image or user packages)")
	} else {
		for _, tm := range available {
			fmt.Printf("  • %s -> %s\n", tm.ID, tm.HostPath)
		}
	}
	fmt.Println()

	ui.InfoLn("Modular toolchains mount read-only into /opt/toolchains/<id> and prepend to PATH.")
}

// FetchToolchains fetches one or more toolchains from GHCR into host cache.
func FetchToolchains(args []string) {
	ctx := context.Background()
	force := false
	markAlways := false
	var targets []toolchains.ToolchainID

	for _, arg := range args {
		switch arg {
		case "--force", "-f":
			force = true
		case "--always", "-a":
			markAlways = true
		case "all":
			targets = append(targets, toolchains.AllToolchains...)
		default:
			targets = append(targets, toolchains.ToolchainID(arg))
		}
	}

	if len(targets) == 0 {
		cwd, err := os.Getwd()
		if err != nil {
			cwd = "."
		}
		targets = toolchains.DetectToolchains(cwd)
		if len(targets) == 0 {
			ui.InfoLn("No toolchains detected in current project. Specify a toolchain ID or 'all':")
			ui.InfoLn("  construct sys toolchains fetch <id...>")
			ui.InfoLn("  construct sys toolchains fetch all")
			return
		}
	}

	opts := toolchains.FetchOptions{Force: force}
	var successful []string
	for _, tc := range targets {
		if !force && toolchains.IsToolchainCached(tc) {
			ui.InfoF("✓ Toolchain %s already cached (use --force to re-download)\n", tc)
			successful = append(successful, string(tc))
			continue
		}
		ui.InfoF("📦 Downloading toolchain: %s...\n", tc)
		if err := fetchToolchainFn(ctx, tc, opts); err != nil {
			ui.InfoF("Error downloading toolchain %s: %v\n", tc, err)
		} else {
			ui.InfoF("✓ Toolchain %s installed\n", tc)
			successful = append(successful, string(tc))
		}
	}

	if markAlways && len(successful) > 0 {
		addAlwaysToolchains(successful)
	}
}

// HandleAlwaysCommand manages the `construct sys toolchains always` subcommands.
func HandleAlwaysCommand(args []string) {
	if len(args) == 0 || args[0] == "list" {
		listAlwaysToolchains()
		return
	}

	switch args[0] {
	case "add":
		if len(args) < 2 {
			ui.InfoLn("Usage: construct sys toolchains always add <id...>")
			return
		}
		addAlwaysToolchains(args[1:])
	case "remove", "rm", "delete":
		if len(args) < 2 {
			ui.InfoLn("Usage: construct sys toolchains always remove <id...>")
			return
		}
		removeAlwaysToolchains(args[1:])
	default:
		normID := toolchains.ToolchainID(strings.ToLower(strings.TrimSpace(args[0])))
		if toolchains.IsValidToolchain(normID) {
			addAlwaysToolchains(args)
			return
		}
		ui.InfoF("Unknown always action: %s\n", args[0])
		ui.InfoLn("Usage: construct sys toolchains always [list|add|remove] [id...]")
	}
}

func listAlwaysToolchains() {
	cfg, _, err := config.Load()
	if err != nil {
		ui.InfoF("Error reading configuration: %v\n", err)
		return
	}
	if len(cfg.Toolchains.Always) == 0 {
		ui.InfoLn("No toolchains configured to always load.")
		ui.InfoLn("Add one with: construct sys toolchains always add <id...>")
		return
	}
	ui.InfoLn("Toolchains configured to always load:")
	for _, tc := range cfg.Toolchains.Always {
		ui.InfoF("  • %s\n", tc)
	}
}

func addAlwaysToolchains(ids []string) {
	configPath := filepath.Join(config.GetConfigDir(), "config.toml")
	cfg, _, err := config.Load()
	if err != nil {
		ui.InfoF("Error reading config: %v\n", err)
		return
	}

	seen := make(map[string]bool)
	var updated []string
	for _, existing := range cfg.Toolchains.Always {
		normExisting := strings.ToLower(strings.TrimSpace(existing))
		if !seen[normExisting] && normExisting != "" {
			seen[normExisting] = true
			updated = append(updated, normExisting)
		}
	}

	ctx := context.Background()
	var toFetch []toolchains.ToolchainID
	for _, raw := range ids {
		norm := strings.ToLower(strings.TrimSpace(raw))
		tcID := toolchains.ToolchainID(norm)
		if !toolchains.IsValidToolchain(tcID) {
			ui.InfoF("Warning: %q is not a supported toolchain ID, skipping.\n", raw)
			continue
		}
		if !seen[norm] {
			seen[norm] = true
			updated = append(updated, norm)
		}
		if !toolchains.IsToolchainCached(tcID) {
			toFetch = append(toFetch, tcID)
		}
	}

	if err := config.SetToolchainsAlways(configPath, updated); err != nil {
		ui.InfoF("Failed to update config.toml: %v\n", err)
		return
	}
	ui.InfoLn("✓ Updated [toolchains] always in config.toml")

	if len(toFetch) > 0 {
		for _, tc := range toFetch {
			ui.InfoF("📦 Downloading toolchain: %s...\n", tc)
			if err := fetchToolchainFn(ctx, tc, toolchains.FetchOptions{}); err != nil {
				ui.InfoF("Warning: could not fetch toolchain %s: %v\n", tc, err)
			} else {
				ui.InfoF("✓ Toolchain %s installed\n", tc)
			}
		}
	}
}

func removeAlwaysToolchains(ids []string) {
	configPath := filepath.Join(config.GetConfigDir(), "config.toml")
	cfg, _, err := config.Load()
	if err != nil {
		ui.InfoF("Error reading config: %v\n", err)
		return
	}

	toRemove := make(map[string]bool)
	for _, raw := range ids {
		toRemove[strings.ToLower(strings.TrimSpace(raw))] = true
	}

	var updated []string
	for _, existing := range cfg.Toolchains.Always {
		normExisting := strings.ToLower(strings.TrimSpace(existing))
		if !toRemove[normExisting] && normExisting != "" {
			updated = append(updated, normExisting)
		}
	}

	if err := config.SetToolchainsAlways(configPath, updated); err != nil {
		ui.InfoF("Failed to update config.toml: %v\n", err)
		return
	}
	ui.InfoLn("✓ Removed from [toolchains] always in config.toml")
}

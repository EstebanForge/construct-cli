package sys

import (
	"context"
	"fmt"
	"os"

	"github.com/EstebanForge/construct-cli/internal/toolchains"
	"github.com/EstebanForge/construct-cli/internal/ui"
)

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
	fmt.Println("Usage: construct sys toolchains [list|fetch] [args...]")
	fmt.Println()
	fmt.Println("Manage and inspect modular read-only toolchains.")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  list                List detected project toolchains and installed cache (default)")
	fmt.Println("  fetch [all|<id>...] Fetch and cache toolchains from GHCR (flags: --force)")
}

// ListToolchains lists detected and cached toolchains.
func ListToolchains() {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	detected := toolchains.DetectToolchains(cwd)
	cacheDir := toolchains.CacheDir()
	available := toolchains.AvailableToolchains(cacheDir)

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
	var targets []toolchains.ToolchainID

	for _, arg := range args {
		switch arg {
		case "--force", "-f":
			force = true
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
	for _, tc := range targets {
		if !force && toolchains.IsToolchainCached(tc) {
			ui.InfoF("✓ Toolchain %s already cached (use --force to re-download)\n", tc)
			continue
		}
		ui.InfoF("📦 Downloading toolchain: %s...\n", tc)
		if err := toolchains.FetchToolchain(ctx, tc, opts); err != nil {
			ui.InfoF("Error downloading toolchain %s: %v\n", tc, err)
		} else {
			ui.InfoF("✓ Toolchain %s installed\n", tc)
		}
	}
}

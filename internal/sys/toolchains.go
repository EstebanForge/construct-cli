package sys

import (
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
	fmt.Println("Usage: construct sys toolchains [list]")
	fmt.Println()
	fmt.Println("Manage and inspect modular read-only toolchains.")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  list    List detected project toolchains and installed cache (default)")
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

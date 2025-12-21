// Package ui provides user interface utilities and help message formatting.
package ui

import (
	"fmt"
	"os"

	"github.com/EstebanForge/construct-cli/internal/constants"
)

// PrintHelp prints the main help message.
func PrintHelp() {
	help := `The Construct CLI - The Secure Loading Program for AI Agents

Usage:
  construct <agent> [args...]                # Run AI agent (primary use case)
  construct <namespace> <command> [options]  # Run namespaced command
  construct --help                           # Show this help
  ct <agent> [args...]                       # Alias for construct

Global Flags:
  -ct-v, --ct-verbose    # Show detailed output (info level)
  -ct-d, --ct-debug      # Show debug output (debug level)
  -ct-n, --ct-network    # Set network isolation mode (permissive|strict|offline)

[sys] System Commands:
  construct sys init             # Initialize environment and install agents
  construct sys update           # Update agents to latest versions
  construct sys migrate          # Re-run migrations to sync config/templates with the binary
  construct sys reset            # Delete volumes and reinstall
  construct sys shell            # Interactive shell with all agents
  construct sys install-aliases  # Install agent aliases to your host shell (claude, gemini, etc.)
  construct sys self-update      # Update construct itself to the latest version
  construct sys update-check     # Check if an update is available
  construct sys version          # Show version
  construct sys help             # Show this help (alias for --help)
  construct sys config           # Open config.toml in editor
  construct sys agents           # List supported agents
  construct sys doctor           # Check system health

[network] Network Management:
  construct network allow api.anthropic.com  # Add domain to allowlist
  construct network block *.malicious.com    # Add domain to blocklist
  construct network remove 1.2.3.4           # Remove rule
  construct network list                     # Show all rules
  construct network status                   # Show active UFW status in container
  construct network clear                    # Clear all rules

Agent Examples:
  construct claude "Debug this API"        # Run Claude Code
  construct gemini --resume id "Continue"  # Run Gemini with flags
  construct shell "run bash script"        # No collision with sys shell
  ct qwen "Fix bugs"                       # Use ct alias

  Available agents: claude, qwen, gemini, opencode, copilot, glm, minimax, kimi

Network Isolation:
  Set in config.toml [network] section or use --ct-network flag:
    mode = "permissive"  # Full network access (default)
    mode = "strict"      # Custom network + domain/IP filtering (use network commands)
    mode = "offline"     # No network access

For more information, visit: https://github.com/EstebanForge/construct-cli
`
	if GumAvailable() {
		cmd := GetGumCommand("format", help)
		cmd.Stdout = os.Stdout
		cmd.Run()
	} else {
		fmt.Print(help)
	}
}

// PrintNetworkHelp prints help for network commands.
func PrintNetworkHelp() {
	help := `Network Management Commands

Usage:
  construct network <command> [args...]

Commands:
  allow <domain|ip>   # Add domain or IP to allowlist
  block <domain|ip>   # Add domain or IP to blocklist
  remove <domain|ip>  # Remove network rule
  list                # Show all configured rules
  status              # Show active UFW status in container
  clear               # Clear all network rules

Examples:
  construct network allow api.anthropic.com
  construct network block *.malicious.com
  construct network remove 1.2.3.4
  construct network list
`
	fmt.Print(help)
}

// PrintDaemonHelp prints help for daemon commands.
func PrintDaemonHelp() {
	help := `Daemon Mode Commands

Usage:
  construct daemon <command>

Commands:
  start   # Start background container
  stop    # Stop background container
  attach  # Attach to running daemon (Ctrl+P Ctrl+Q to detach)
  status  # Show daemon status

Examples:
  construct daemon start
  construct daemon attach
  construct daemon status
  construct daemon stop
`
	fmt.Print(help)
}

// PrintVersion prints the application version.
func PrintVersion() {
	fmt.Printf("%s version %s\n", constants.AppName, constants.Version)
}

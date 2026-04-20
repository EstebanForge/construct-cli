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
  construct sys agents             # List supported agents
  construct sys agents-md          # Manage global instruction files (rules) for agents
  construct sys config             # Config operations (opens config.toml by default)
                                   # [--migrate] Re-sync config/templates with the current binary
                                   # [--restore] Restore config.toml from backup
  construct sys daemon             # Manage background daemon (start|stop|restart|attach|status)
  construct sys packages           # Package operations (opens packages.toml by default)
                                   # [--install] Apply changes from packages.toml to running container
                                   # [--reinstall] Recreate package volume and reinstall packages
                                   # [--update] Alias for 'construct sys update'
  construct sys doctor             # System health operations (includes packages drift checks)
                                   # [--fix] Append missing defaults to config.toml (backup first)
  construct sys clipboard-debug    # Show clipboard bridge logs and patch state for Copilot
  construct sys ct-fix             # Repair the ct shorthand command symlink
  construct sys help               # Show this help (alias for --help)
  construct sys init               # Initialize environment and install agents inside Construct
  construct sys aliases            # Host alias operations
                                   # [--install] Install agent aliases/functions (includes ns-)
                                   # [--update] Reinstall/update host aliases
                                   # [--uninstall] Remove Construct alias block from shell
  construct sys login-bridge       # Start localhost login callback bridge for headless agents
  construct sys rebuild            # Migrate and sync config/templates, then rebuild Docker image
  construct sys reset              # Delete agent binaries and cache (preserves personal config)
  construct sys self-update        # Update construct itself to the latest version
  construct sys set-password       # Change the password for the construct user in container
  construct sys shell              # Interactive shell with all agents inside Construct
  construct sys ssh-import         # Import SSH keys from host into Construct (no SSH Agent)
  construct sys update             # Update agents and packages to latest versions inside Construct
  construct sys check-update       # Check if an update is available for The Construct
  construct sys version            # Show version

Available agents: claude, qwen, gemini, opencode, copilot, cline, crush, codex,
                droid, goose, kilocode, pi, omp, amp

For more information, visit: https://github.com/EstebanForge/construct-cli
`
	if GumAvailable() {
		cmd := GetGumCommand("format", help)
		cmd.Stdout = os.Stdout
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to render help: %v\n", err)
		}
	} else {
		fmt.Print(help)
	}
}

// PrintSysHelp prints help for sys commands.
func PrintSysHelp() {
	help := `Usage: construct sys <command> [options]

Commands:
  agents             # List supported agents
  agents-md          # Manage global instruction files (rules) for agents
  config             # Config operations (opens config.toml by default)
                     # [--migrate] Re-sync config/templates with current binary
                     # [--restore] Restore config.toml from backup
  daemon             # Manage background daemon (start|stop|restart|attach|status|install|uninstall)
  packages           # Manage packages.toml and package lifecycle
  doctor             # System health operations
                     # [--fix] Append missing defaults to config.toml (backup first)
  clipboard-debug    # Show clipboard bridge logs and patch state for Copilot
  ct-fix             # Repair the ct shorthand command symlink
  help               # Show this help
  init               # Initialize environment and install agents inside Construct
  aliases            # Host alias operations
                     # [--install] Install agent aliases/functions (includes ns-)
                     # [--update] Reinstall/update host aliases
                     # [--uninstall] Remove Construct alias block from shell
  login-bridge       # Start localhost login callback bridge for headless agents
  rebuild            # Migrate and sync config/templates, then rebuild Docker image
  reset              # Delete agent binaries and cache (preserves personal config)
  self-update        # Update construct itself to the latest version
  set-password       # Change the password for the construct user in container
  shell              # Interactive shell with all agents inside Construct
  ssh-import         # Import SSH keys from host into Construct (no SSH Agent)
  update             # Update agents and packages to latest versions inside Construct
  check-update       # Check if an update is available for The Construct
  version            # Show version
`
	fmt.Print(help)
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

// PrintSysDaemonHelp prints help for sys daemon commands.
func PrintSysDaemonHelp() {
	help := `Daemon Mode Commands

Usage:
  construct sys daemon <command>

Commands:
  start          # Start background container
  stop           # Stop background container
  restart        # Restart background container
  attach         # Attach to running daemon (Ctrl+P Ctrl+Q to detach)
  status         # Show daemon + auto-start service status
  install        # Install daemon as auto-start service (runs on login/boot)
  uninstall      # Uninstall daemon auto-start service

Examples:
  construct sys daemon start
  construct sys daemon attach
  construct sys daemon status
  construct sys daemon stop
  construct sys daemon restart
  construct sys daemon install
  construct sys daemon uninstall
`
	fmt.Print(help)
}

// PrintVersion prints the application version.
func PrintVersion() {
	fmt.Printf("The Construct CLI - Version %s\n", constants.Version)
}

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
  --clone, --ct-clone    # Execute in an ephemeral zero-trust git clone

[sys] System Commands:
  construct sys agents             # List supported agents
                                   # [push] Push host agent configs into Construct
                                   # [--agents slug,slug] [--force] [--dry-run] [--yes]
  construct sys agents-md          # Manage global instruction files (rules) for agents
  construct sys config             # Config operations (opens config.toml by default)
                                   # [--migrate] Re-sync config/templates with the current binary
                                   # [--restore] Restore config.toml from backup
  construct sys daemon             # Manage background daemon (start|stop|restart|recreate|attach|status)
  construct sys toolchains         # Manage modular toolchains (list detected/cached, fetch from GHCR)
  construct sys packages           # Package operations (opens packages.toml by default)
                                   # [--install] Apply changes from packages.toml to the sandbox
                                   # [--reinstall] Recreate package volume and reinstall packages
                                   # [--update] Alias for 'construct sys update'
  construct sys doctor             # System health operations (includes packages drift checks)
                                   # [--fix] Append missing defaults to config.toml (backup first)
  construct sys clipboard-debug    # Show clipboard bridge logs and patch state for Copilot
  construct sys ct-fix             # Repair the ct shorthand command symlink
  construct sys help               # Show this help (alias for --help)
  construct sys init               # Initialize environment and install agents inside Construct
  construct sys shims              # PATH shims: real executables routing agents
                                   # [<slug>] routes through the sandbox (exec construct <slug>)
                                   # [ns-<slug>] runs the real host binary (non-sandboxed)
                                   # [--install] Write shims (default ~/.local/bin)
                                   # [--uninstall] Remove installed shims
                                   # [--remove-aliases] Only remove legacy shell alias block
                                   # [--list] Show shim state
  construct sys login-bridge       # Start localhost login callback bridge for headless agents
  construct sys rebuild            # Migrate and sync config/templates, then rebuild the sandbox image
  construct sys reset              # Delete agent binaries and cache (preserves personal config)
  construct sys self-update        # Update construct itself to the latest version
  construct sys set-password       # Change the password for the construct user in the sandbox
  construct sys shell              # Interactive shell with all agents inside Construct
  construct sys exec -- <command>  # Run a single command inside the running Construct sandbox
  construct sys ssh-import         # Import SSH keys from host into Construct (no SSH Agent)
  construct sys pull               # Pull latest construct-box image from GHCR
  construct sys update             # Update agents and packages to latest versions inside Construct
  construct sys check-update       # Check if an update is available for The Construct
  construct sys version            # Show version

Available agents: claude, qwen, agy, opencode, copilot, cline, crush, codex,
                droid, goose, kilocode, pi, pig, amp

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

// PrintSysHelp prints help for sys commands, grouped by backend reality.
// Sections that do not apply to the active backend are hidden; an empty or
// unknown backend shows every section.
func PrintSysHelp(backend string) {
	msbActive := backend == "" || backend == "msb"
	ociActive := backend == "" || backend == "containers" || backend == "podman" || backend == "docker"

	help := `Usage: construct sys <command> [options]

Sandbox (works on the active backend):
  update             # Update agents and packages to latest versions inside Construct
  doctor             # System health operations
                     # [--fix] Append missing defaults to config.toml (backup first)
  shell              # Interactive shell with all agents inside Construct
  exec -- <command>  # Run a single command inside the running Construct sandbox
  login-bridge       # Start localhost login callback bridge for headless agents
  clipboard-debug    # Show clipboard bridge logs and patch state for Copilot
`
	if msbActive {
		help += `
msb daemon (backend = msb):
  daemon             # Manage background daemon (start|stop|restart|recreate|attach|status|install|uninstall|roots)
  prepull            # Stage the microVM image in the background
`
	}
	if ociActive {
		help += `
OCI engine (backend = containers|podman|docker):
  init               # Initialize environment and install agents inside Construct
  rebuild            # Migrate and sync config/templates, then rebuild the sandbox image
  pull               # Pull latest construct-box image from GHCR
  reset              # Delete agent binaries and cache (preserves personal config)
  packages           # Manage packages.toml and package lifecycle
                     # [--install] Apply changes from packages.toml to the sandbox
                     # [--reinstall] Recreate package volume and reinstall packages
  set-password       # Change the password for the construct user in the sandbox
`
	}
	help += `
Host & config:
  agents             # List supported agents
  agents-md          # Manage global instruction files (rules) for agents
  config             # Config operations (opens config.toml by default)
                     # [--migrate] Re-sync config/templates with current binary
                     # [--restore] Restore config.toml from backup
  aliases            # Host alias operations
                     # [--install] Install agent aliases/functions (includes ns-)
                     # [--update] Reinstall/update host aliases
                     # [--uninstall] Remove Construct alias block from shell
  shims              # PATH shims: real executables routing agents
                     # [--install] Write shims (default ~/.local/bin)
                     # [--uninstall] Remove installed shims
                     # [--list] Show shim state
  ssh-import         # Import SSH keys from host into Construct (no SSH Agent)
  ct-fix             # Repair the ct shorthand command symlink
  self-update        # Update construct itself to the latest version
  check-update       # Check if an update is available for The Construct
  push               # Push host agent configs into Construct
                     # [--agents slug,slug] [--force] [--dry-run] [--yes]
  version            # Show version
  help               # Show this help
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
  status              # Show active UFW status in the sandbox
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
  start          # Start background sandbox
  stop           # Stop background sandbox
  restart        # Restart background sandbox
  attach         # Attach to running daemon (Ctrl+P Ctrl+Q to detach)
  status         # Show daemon + auto-start service status (+ live session count)
  install        # Install daemon as auto-start service (runs on login/boot)
  uninstall      # Uninstall daemon auto-start service
  roots list     # Show learned roots (auto-mounted dirs) + pinned mount paths
  roots forget <path>  # Remove a learned root (pinned mount_paths entries refuse)

Examples:
  construct sys daemon start
  construct sys daemon attach
  construct sys daemon status
  construct sys daemon stop
  construct sys daemon restart
  construct sys daemon install
  construct sys daemon uninstall
  construct sys daemon roots list
`
	fmt.Print(help)
}

// PrintVersion prints the application version.
func PrintVersion() {
	fmt.Printf("The Construct CLI - Version %s\n", constants.Version)
}

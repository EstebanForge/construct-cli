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
  construct <command> [options]              # Run frequent command (doctor, agents, update)
  construct sys <command> [options]          # Run system administration command (alias: ct sys)
  construct --help                           # Show this help
  ct <agent> [args...]                       # Alias for construct

Global Flags:
  -ct-v, --ct-verbose    # Show detailed output (info level)
  -ct-d, --ct-debug      # Show debug output (debug level)
  -ct-n, --ct-network    # Set network isolation mode (permissive|strict|offline|allowlist)
  --clone, --ct-clone    # Execute in an ephemeral zero-trust git clone

Shortcuts:
  construct doctor [fix|clipboard]           # Diagnose sandbox health (repair, clipboard debug)
  construct agents [list|push|rules]         # Manage agents (list supported, push configs, rules)
  construct update                           # Update agents and packages inside Construct

[sys] System Commands:
  construct sys shell              # Interactive shell with all agents inside Construct
  construct sys exec -- <command>  # Run a single command inside the running Construct sandbox
  construct sys update             # Update agents and packages to latest versions inside Construct
  construct sys daemon             # Manage background daemon (start|stop|restart|recreate|attach|status|roots)
  construct sys toolchains         # Manage modular toolchains (list detected/cached, fetch from GHCR)
  construct sys packages [action]  # Package operations (opens packages.toml by default)
                                   # [install] Apply changes from packages.toml to sandbox
                                   # [reinstall] Recreate package volume and reinstall
                                   # [update] Update packages to latest versions
  construct sys image <command>    # Image lifecycle (pull, rebuild, reset, init)
  construct sys agents [command]   # Supported agent operations
                                   # [list] List supported agents (default)
                                   # [push] Push host agent configs into Construct
                                   # [rules] Manage global instruction files (AGENTS.md)
  construct sys network <command>  # Egress proxy and network policy (alias: sys net)
                                   # [explain] Diagnose host policy (allowed/blocked)
                                   # [log] View egress proxy audit logs ([--tail N] [-f])
                                   # [list] Show rules and allowed TCP ports
                                   # [allow|block|remove] Manage domain or IP rules
                                   # [status|clear] Check status or clear dynamic rules
  construct sys shims [action]     # PATH shims routing agents and shorthand ct link
                                   # [install] Write shims (default ~/.local/bin)
                                   # [uninstall] Remove installed shims
                                   # [fix] Repair ct shorthand symlink
                                   # [list] Show shim state (default)
  construct sys auth <action>      # Authentication operations
                                   # [bridge] Start localhost OAuth callback bridge ([--ports])
                                   # [password] Set or change sandbox user password
  construct sys ssh import         # Import SSH keys from host into Construct (no SSH Agent)
  construct sys config [action]    # Config operations (opens config.toml by default)
                                   # [migrate] Re-sync config/templates with current binary
                                   # [restore] Restore config.toml from backup
  construct sys doctor [action]    # System health diagnostics
                                   # [fix] Auto-repair missing defaults and symlinks
                                   # [clipboard] View clipboard bridge diagnostics
  construct sys self-update [act]  # Update construct itself to the latest version
                                   # [check] Check if an update is available
  construct sys version            # Show version
  construct sys help               # Show sys help

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

// PrintSysHelp prints help for sys commands, grouped by functional domains.
// Sections that do not apply to the active backend are hidden; an empty or
// unknown backend shows every section.
func PrintSysHelp(backend string) {
	msbActive := backend == "" || backend == "msb"
	ociActive := backend == "" || backend == "containers" || backend == "podman" || backend == "docker"

	help := `Usage: construct sys <command> [options]

Sandbox & Tools:
  shell              # Interactive shell with all agents inside Construct
  exec -- <command>  # Run a single command inside the running Construct sandbox
  update             # Update agents and packages to latest versions inside Construct
  toolchains         # Manage modular toolchains (list detected/cached, fetch from GHCR)
  packages [action]  # Manage packages.toml and package lifecycle
                     # [install] Apply changes from packages.toml to the sandbox
                     # [reinstall] Recreate package volume and reinstall packages
                     # [update] Update packages to latest versions
`
	if msbActive {
		help += `
msb daemon (backend = msb):
  daemon             # Manage background daemon (start|stop|restart|recreate|attach|status|roots)
`
	}
	help += `
Image management:
  image <command>    # Manage container/microVM sandbox image
                     # [pull] Pull latest construct-box image from GHCR
                     # [rebuild] Re-sync templates and rebuild the sandbox image
                     # [reset] Delete agent binaries and cache (preserves personal config)
`
	if ociActive {
		help += `                     # [init] Initialize environment and install agents
`
	}
	help += `
Agents & Rules:
  agents [command]   # Manage supported AI agents
                     # [list] List supported agents (default)
                     # [push] Push host agent configs into Construct
                     # [rules] Manage global instruction files (AGENTS.md) for agents

Network & Security (alias: net):
  network <command>  # Egress network policy and proxy management
                     # [explain] Diagnose whether host is allowed or blocked
                     # [log] View proxy audit log ([--tail N] [-f])
                     # [list] List rules and allowed TCP ports
                     # [allow] Add domain or IP to allowlist
                     # [block] Add domain or IP to blocklist
                     # [remove] Remove rule from allowlist or blocklist
                     # [status] Show active network status
                     # [clear] Clear dynamic rules
  auth <action>      # Authentication helpers
                     # [bridge] Start localhost OAuth callback bridge ([--ports 1455,8085])
                     # [password] Set or change sandbox user password
  ssh import         # Import SSH keys from host into Construct (no SSH Agent)

Host & Integrations:
  shims [action]     # PATH shims routing agents and shorthand ct link
                     # [install] Write shims (default ~/.local/bin)
                     # [uninstall] Remove installed shims
                     # [fix] Repair ct shorthand symlink
                     # [list] Show shim state (default)
  config [action]    # Config operations (opens config.toml by default)
                     # [migrate] Re-sync config/templates with current binary
                     # [restore] Restore config.toml from backup
  doctor [action]    # System health diagnostics
                     # [fix] Auto-repair missing defaults and symlinks
                     # [clipboard] View clipboard bridge diagnostics
  self-update [act]  # Update construct itself to the latest version
                     # [check] Check if an update is available
  version            # Show version
  help               # Show this help
`
	fmt.Print(help)
}

// PrintNetworkHelp prints help for network commands.
func PrintNetworkHelp() {
	help := `Network Management Commands

Usage:
  construct sys network <command> [args...] (alias: construct sys net)

Commands:
  allow <domain|ip>   # Add domain or IP to allowlist
  block <domain|ip>   # Add domain or IP to blocklist
  remove <domain|ip>  # Remove network rule
  list                # Show all configured rules
  status              # Show active network status in the sandbox
  clear               # Clear all network rules
  explain <host>      # Explain whether a host is allowed or blocked and why
  log [--tail N] [-f] # View egress proxy access logs and security events

Examples:
  construct sys net explain api.anthropic.com
  construct sys net log --tail 20
  construct sys network allow api.anthropic.com
  construct sys network block *.malicious.com
  construct sys network remove 1.2.3.4
  construct sys network list
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

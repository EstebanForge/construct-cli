// Package main is the entry point for the Construct CLI.
package main

import (
	"fmt"
	"os"

	"github.com/EstebanForge/construct-cli/internal/agent"
	"github.com/EstebanForge/construct-cli/internal/config"
	"github.com/EstebanForge/construct-cli/internal/constants"
	"github.com/EstebanForge/construct-cli/internal/daemon"
	"github.com/EstebanForge/construct-cli/internal/doctor"
	"github.com/EstebanForge/construct-cli/internal/logs"
	"github.com/EstebanForge/construct-cli/internal/migration"
	"github.com/EstebanForge/construct-cli/internal/network"
	"github.com/EstebanForge/construct-cli/internal/runtime"
	"github.com/EstebanForge/construct-cli/internal/sys"
	"github.com/EstebanForge/construct-cli/internal/ui"
	"github.com/EstebanForge/construct-cli/internal/update"
)

func main() {
	// Parse global flags
	args := os.Args[1:]
	var networkFlag string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--ct-verbose", "-ct-v":
			ui.SetLogLevel(ui.LogLevelInfo)
			args = append(args[:i], args[i+1:]...)
			i--
		case "--ct-debug", "-ct-d":
			ui.SetLogLevel(ui.LogLevelDebug)
			args = append(args[:i], args[i+1:]...)
			i--
		case "--ct-network", "-ct-n":
			if i+1 < len(args) {
				networkFlag = args[i+1]
				args = append(args[:i], args[i+2:]...)
				i -= 2
			} else {
				fmt.Fprintf(os.Stderr, "Error: --ct-network flag requires a value\n\n")
				ui.PrintHelp()
				os.Exit(1)
			}
		}
	}

	// Handle version/help early - these don't require config loading
	if len(args) >= 1 {
		switch args[0] {
		case "--version", "-v", "version":
			ui.PrintVersion()
			return
		case "--help", "-h", "help":
			ui.PrintHelp()
			return
		}
	}

	// Check for version migrations before loading config
	// This ensures config files are updated before we try to parse them
	// Skip migration check for self-update to avoid confusing version messages
	isSelfUpdate := len(args) >= 2 && args[0] == "sys" && args[1] == "self-update"
	if !isSelfUpdate && migration.NeedsMigration() {
		if err := migration.CheckAndMigrate(); err != nil {
			fmt.Fprintf(os.Stderr, "Error during migration: %v\n", err)
			fmt.Fprintf(os.Stderr, "Please check your configuration files manually.\n")
			os.Exit(1)
		}
	}

	// Load config to check for updates (ignoring errors for now, will be handled by commands)
	cfg, _, err := config.Load()
	if err != nil {
		ui.LogError(err)
		cfg = nil
	}
	if cfg != nil {
		logs.RunCleanupIfDue(cfg)
	}

	// Passive update check (non-blocking, runs in background)
	if cfg != nil && update.ShouldCheckForUpdates(cfg) {
		go func() {
			if latest, available, err := update.CheckForUpdates(); err == nil && available {
				update.DisplayNotification(latest)
			}
			update.RecordUpdateCheck()
		}()
	}

	if len(args) < 1 {
		sys.EnsureCtSymlink()
		ui.PrintHelp()
		return
	}

	command := args[0]

	// Namespace routing
	switch command {
	case "sys":
		if len(args) < 2 {
			sys.EnsureCtSymlink()
			ui.PrintSysHelp()
			os.Exit(1)
		}
		handleSysCommand(args[1:], cfg)
	case "network":
		if len(args) < 2 {
			ui.PrintNetworkHelp()
			os.Exit(1)
		}
		handleNetworkCommand(args[1:])
	case "daemon":
		if len(args) < 2 {
			ui.PrintDaemonHelp()
			os.Exit(1)
		}
		handleDaemonCommand(args[1:])
	case "cc":
		if len(args) < 2 || args[1] == "--help" || args[1] == "-h" {
			// Ensure config is loaded for PrintCCHelp
			if cfg == nil {
				var err error
				cfg, _, err = config.Load()
				if err != nil {
					ui.LogError(err)
					os.Exit(1)
				}
			}
			agent.PrintCCHelp(cfg)
			os.Exit(0)
		}
		providerName := args[1]
		agentArgs := append([]string{"claude"}, args[2:]...)
		agent.RunWithProvider(agentArgs, networkFlag, providerName)
	case "claude":
		// Check if first arg is a provider alias (fallback wrapper)
		if len(args) > 1 {
			// Ensure config is loaded
			if cfg == nil {
				var err error
				cfg, _, err = config.Load()
				if err != nil {
					ui.LogError(err)
					os.Exit(1)
				}
			}
			if _, exists := cfg.Claude.Providers[args[1]]; exists {
				providerName := args[1]
				agentArgs := append([]string{"claude"}, args[2:]...)
				agent.RunWithProvider(agentArgs, networkFlag, providerName)
				return
			}
		}
		// Normal claude invocation
		agent.RunWithArgs(args, networkFlag)
	default:
		// Check if it's a supported agent
		if !agent.IsSupported(command) {
			fmt.Printf("Unknown command or agent: %s\n", command)
			fmt.Println("Run 'construct --help' for usage.")
			os.Exit(1)
		}
		// Everything else is an agent invocation
		agent.RunWithArgs(args, networkFlag)
	}
}

func handleSysCommand(args []string, cfg *config.Config) {
	// Auto-create ct symlink for all sys commands
	sys.EnsureCtSymlink()

	switch args[0] {
	case "init", "rebuild":
		// For rebuild, we also want to refresh configuration and templates from binary first.
		// This ensures that any template or config changes are applied before building.
		// For init, we rely on config.Load()'s idempotent Init() and the automatic migration check at startup.
		if args[0] == "rebuild" {
			if err := migration.ForceRefresh(); err != nil {
				ui.GumError(fmt.Sprintf("Migration failed: %v", err))
				os.Exit(1)
			}
		}

		// Init/rebuild logic is handled by runtime.BuildImage which calls config loading if needed
		// If cfg is nil, we load it
		if cfg == nil {
			var err error
			cfg, _, err = config.Load()
			if err != nil {
				ui.LogError(err)
				os.Exit(1)
			}
		}
		runtime.BuildImage(cfg)
	case "update":
		if cfg == nil {
			var err error
			cfg, _, err = config.Load()
			if err != nil {
				ui.LogError(err)
				os.Exit(1)
			}
		}
		sys.UpdateAgents(cfg)
	case "install-packages":
		if cfg == nil {
			var err error
			cfg, _, err = config.Load()
			if err != nil {
				ui.LogError(err)
				os.Exit(1)
			}
		}
		sys.InstallPackages(cfg)
	case "reset":
		if cfg == nil {
			var err error
			cfg, _, err = config.Load()
			if err != nil {
				ui.LogError(err)
				os.Exit(1)
			}
		}
		sys.ResetVolumes(cfg)
	case "shell":
		// Shell is just running with empty args (entrypoint defaults to shell)
		agent.RunWithArgs([]string{}, "")
	case "install-aliases":
		sys.InstallAliases()
	case "version":
		ui.PrintVersion()
	case "help":
		ui.PrintHelp()
	case "config":
		sys.OpenConfig()
	case "packages":
		sys.OpenPackages()
	case "agents":
		agent.List()
	case "agents-md":
		sys.ListAgentMemories()
	case "doctor":
		doctor.Run()
	case "self-update":
		if err := update.SelfUpdate(); err != nil {
			ui.GumError(fmt.Sprintf("Self-update failed: %v", err))
			os.Exit(1)
		}
	case "update-check":
		latest, available, err := update.CheckForUpdates()
		if err != nil {
			ui.GumError(fmt.Sprintf("Failed to check for updates: %v", err))
		} else if available {
			update.DisplayNotification(latest)
			// Offer to self-update
			if ui.GumConfirm("Would you like to update now?") {
				if err := update.SelfUpdate(); err != nil {
					ui.GumError(fmt.Sprintf("Self-update failed: %v", err))
					os.Exit(1)
				}
			}
		} else {
			if ui.GumAvailable() {
				ui.GumSuccess("You are on the latest version.")
			} else {
				fmt.Printf("You are on the latest version (%s)\n", constants.Version)
			}
		}
		update.RecordUpdateCheck()
	case "migrate":
		// Force refresh configuration and templates from binary
		if err := migration.ForceRefresh(); err != nil {
			ui.GumError(fmt.Sprintf("Migration failed: %v", err))
			fmt.Fprintf(os.Stderr, "Please check your configuration files manually.\n")
			os.Exit(1)
		}
	case "ssh-import":
		sys.SSHImport()
	case "restore-config":
		sys.RestoreConfig()
	case "login-bridge":
		sys.LoginBridge(args[1:])
	default:
		fmt.Printf("Unknown system command: %s\n", args[0])
		fmt.Println("Run 'construct sys' for a list of available commands.")
		os.Exit(1)
	}
}

func handleNetworkCommand(args []string) {
	command := args[0]
	switch command {
	case "allow":
		if len(args) < 2 {
			ui.GumError("Usage: construct network allow <domain|ip>")
			os.Exit(1)
		}
		network.AddRule(args[1], "allow")
	case "block":
		if len(args) < 2 {
			ui.GumError("Usage: construct network block <domain|ip>")
			os.Exit(1)
		}
		network.AddRule(args[1], "block")
	case "remove":
		if len(args) < 2 {
			ui.GumError("Usage: construct network remove <domain|ip>")
			os.Exit(1)
		}
		network.RemoveRule(args[1])
	case "list":
		network.ListRules()
	case "status":
		network.ShowStatus()
	case "clear":
		network.ClearRules()
	default:
		ui.GumError(fmt.Sprintf("Unknown network command: %s", command))
		ui.PrintNetworkHelp()
		os.Exit(1)
	}
}

func handleDaemonCommand(args []string) {
	command := args[0]
	switch command {
	case "start":
		daemon.Start()
	case "stop":
		daemon.Stop()
	case "attach":
		daemon.Attach()
	case "status":
		daemon.Status()
	default:
		ui.GumError(fmt.Sprintf("Unknown daemon command: %s", command))
		ui.PrintDaemonHelp()
		os.Exit(1)
	}
}

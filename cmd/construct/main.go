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
	"github.com/EstebanForge/construct-cli/internal/msbembed"
	"github.com/EstebanForge/construct-cli/internal/network"
	"github.com/EstebanForge/construct-cli/internal/runtime"
	"github.com/EstebanForge/construct-cli/internal/sys"
	"github.com/EstebanForge/construct-cli/internal/ui"
	"github.com/EstebanForge/construct-cli/internal/update"
)

func main() {
	// Embedded-runtime builds pin the msb pair + isolated home into the
	// process env before anything can touch msb. Best-effort: a failed
	// extraction surfaces as a hard error at the launch sites that need
	// it, not as a global failure of e.g. `construct --help`.
	if msbembed.Available() {
		if err := msbembed.Activate(); err != nil {
			ui.LogWarning("Embedded microsandbox runtime unavailable: %v", err)
		}
	}

	// Parse global flags
	args := os.Args[1:]
	if len(args) > 0 && args[0] == "__gum" {
		os.Exit(ui.RunEmbeddedGum(args[1:]))
	}

	var networkFlag string
	var cloneFlag bool
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--clone", "--ct-clone":
			cloneFlag = true
			args = append(args[:i], args[i+1:]...)
			i--
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

	// Check for version migrations before loading config.
	// Run migrations only for recognized commands to avoid side effects on typos
	// (e.g. `construct sys rebuilt`).
	if shouldRunMigration(args) && migration.NeedsMigration() {
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
			if latest, available, err := update.CheckForUpdates(cfg); err == nil && available {
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

	// Top-level shortcuts and namespace routing
	switch command {
	case "doctor":
		handleDoctorCommand(args[1:], cfg)
		return
	case "agents":
		handleAgentsCommand(args[1:])
		return
	case "update":
		cfg = ensureConfigLoaded(cfg)
		runUpdate(cfg)
		return
	case "sys":
		if len(args) < 2 {
			sys.EnsureCtSymlink()
			ui.PrintSysHelp(cfg.Runtime.Backend)
			os.Exit(1)
		}
		handleSysCommand(args[1:], cfg)
		return
	case "cc":
		if len(args) < 2 || args[1] == "--help" || args[1] == "-h" {
			cfg = ensureConfigLoaded(cfg)
			agent.PrintCCHelp(cfg)
			os.Exit(0)
		}
		providerName := args[1]
		agentArgs := append([]string{"claude"}, args[2:]...)
		agent.RunWithProvider(agentArgs, networkFlag, providerName, cloneFlag)
		return
	case "claude":
		if len(args) > 1 && cfg != nil {
			if _, exists := cfg.Claude.Providers[args[1]]; exists {
				providerName := args[1]
				agentArgs := append([]string{"claude"}, args[2:]...)
				agent.RunWithProvider(agentArgs, networkFlag, providerName, cloneFlag)
				return
			}
		}
		agent.RunWithArgs(args, networkFlag, cloneFlag)
		return
	default:
		// Check if it's a supported agent
		if !agent.IsSupported(command) {
			fmt.Printf("Unknown command or agent: %s\n", command)
			fmt.Println("Run 'construct --help' for usage.")
			os.Exit(1)
		}
		// Everything else is an agent invocation
		agent.RunWithArgs(args, networkFlag, cloneFlag)
	}
}

func handleDoctorCommand(args []string, cfg *config.Config) {
	if len(args) > 0 {
		switch args[0] {
		case "clipboard":
			cfg = ensureConfigLoaded(cfg)
			sys.ClipboardDebug(cfg)
			return
		case "fix":
			doctor.Run(append([]string{"fix"}, args[1:]...)...)
			return
		}
	}
	doctor.Run(args...)
}

func handleImageCommand(args []string, cfg *config.Config) {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		fmt.Println("Usage: construct sys image <pull|rebuild|reset|init>")
		fmt.Println("\nCommands:")
		fmt.Println("  pull       Pull latest construct-box image from GHCR")
		fmt.Println("  rebuild    Re-sync templates and rebuild the sandbox image")
		fmt.Println("  reset      Delete agent binaries and cache (preserves personal config)")
		fmt.Println("  init       Initialize environment and install agents inside Construct")
		os.Exit(0)
	}

	switch args[0] {
	case "pull":
		cfg = ensureConfigLoaded(cfg)
		if err := runtime.PullImage(cfg); err != nil {
			ui.GumError(fmt.Sprintf("Pull failed: %v", err))
			os.Exit(1)
		}
	case "init", "rebuild":
		if args[0] == "rebuild" {
			if err := migration.ForceRefresh(); err != nil {
				ui.GumError(fmt.Sprintf("Migration failed: %v", err))
				os.Exit(1)
			}
		}

		cfg = ensureConfigLoaded(cfg)
		if cfg.Runtime.Backend == config.BackendMsb {
			rt := runtime.DetectRuntime("auto")
			if rt == "" || rt == "none" {
				if err := runtime.PullImage(cfg); err != nil {
					ui.GumError(fmt.Sprintf("Image pull failed: %v", err))
					os.Exit(1)
				}
				return
			}
		}
		runtime.BuildImage(cfg)
		if cfg.Runtime.Backend == config.BackendMsb {
			if err := runtime.TransitionLocalConstructImageToMsb(cfg); err != nil {
				ui.GumError(fmt.Sprintf("msb image transition failed: %v", err))
				os.Exit(1)
			}
		}
	case "reset":
		cfg = ensureConfigLoaded(cfg)
		sys.ResetVolumes(cfg)
	default:
		fmt.Fprintf(os.Stderr, "Unknown image command: %s\n", args[0])
		fmt.Fprintf(os.Stderr, "Run 'construct sys image help' for available commands.\n")
		os.Exit(1)
	}
}

func handleSysCommand(args []string, cfg *config.Config) {
	// Auto-create ct symlink for all sys commands
	sys.EnsureCtSymlink()

	switch args[0] {
	case "image":
		handleImageCommand(args[1:], cfg)
	case "update":
		runUpdate(cfg)
	case "shell":
		agent.RunWithArgs([]string{}, "")
	case "exec":
		execArgs := parseExecArgs(args[1:])
		if len(execArgs) == 0 {
			fmt.Fprintf(os.Stderr, "Usage: construct sys exec -- <command> [args...]\n")
			fmt.Fprintf(os.Stderr, "Run a command inside a running Construct container.\n")
			os.Exit(1)
		}
		exitCode := sys.ExecCommand(cfg, execArgs)
		os.Exit(exitCode)
	case "shims":
		sys.HandleShimsCommand(args[1:])
	case "version":
		ui.PrintVersion()
	case "help":
		ui.PrintSysHelp(cfg.Runtime.Backend)
	case "config":
		handleConfigCommand(args[1:])
	case "packages":
		handlePackagesCommand(args[1:], cfg)
	case "agents":
		handleAgentsCommand(args[1:])
	case "network", "net":
		if len(args) < 2 {
			ui.PrintNetworkHelp()
			os.Exit(1)
		}
		handleNetworkCommand(args[1:])
	case "toolchains", "toolchain":
		sys.HandleToolchainsCommand(args[1:])
	case "doctor":
		handleDoctorCommand(args[1:], cfg)
	case "self-update":
		if len(args) > 1 {
			switch args[1] {
			case "check":
				latest, available, err := update.CheckForUpdates(cfg)
				if err != nil {
					ui.GumError(fmt.Sprintf("Failed to check for updates: %v", err))
					os.Exit(1)
				}
				if available {
					update.DisplayNotification(latest)
					if ui.GumConfirm("Would you like to update now?") {
						if err := update.SelfUpdate(cfg); err != nil {
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
				return
			default:
				fmt.Fprintf(os.Stderr, "Unknown self-update option: %s\n", args[1])
				fmt.Fprintf(os.Stderr, "Usage: construct sys self-update [check]\n")
				os.Exit(1)
			}
		}
		if err := update.SelfUpdate(cfg); err != nil {
			ui.GumError(fmt.Sprintf("Self-update failed: %v", err))
			os.Exit(1)
		}
	case "ssh":
		if len(args) > 1 && args[1] == "import" {
			sys.SSHImport()
			return
		}
		fmt.Fprintf(os.Stderr, "Usage: construct sys ssh import\n")
		os.Exit(1)
	case "auth":
		if len(args) > 1 {
			switch args[1] {
			case "bridge":
				sys.LoginBridge(args[2:])
				return
			case "password":
				cfg = ensureConfigLoaded(cfg)
				sys.SetPassword(cfg)
				return
			}
		}
		fmt.Fprintf(os.Stderr, "Usage: construct sys auth <bridge|password>\n")
		os.Exit(1)
	case "prepull":
		runtime.PrepullRun()
	case "daemon":
		if len(args) < 2 {
			ui.PrintSysDaemonHelp()
			os.Exit(1)
		}
		handleDaemonCommand(args[1:])
	default:
		fmt.Printf("Unknown system command: %s\n", args[0])
		fmt.Println("Run 'construct sys' for a list of available commands.")
		os.Exit(1)
	}
}

func handleAgentsCommand(args []string) {
	if len(args) == 0 || args[0] == "list" {
		agent.List()
		return
	}

	switch args[0] {
	case "push":
		if err := agent.SyncAgentConfigs(args[1:]); err != nil {
			ui.GumError(fmt.Sprintf("Agent configs push failed: %v", err))
			os.Exit(1)
		}
	case "rules", "rule":
		sys.ListAgentMemories()
	default:
		fmt.Fprintf(os.Stderr, "Unknown agents command: %s\n", args[0])
		fmt.Fprintf(os.Stderr, "Usage: construct agents [list|push|rules]\n")
		os.Exit(1)
	}
}

func handlePackagesCommand(args []string, cfg *config.Config) {
	// Default behavior remains "open packages.toml".
	if len(args) == 0 {
		sys.OpenPackages()
		return
	}

	switch args[0] {
	case "install":
		cfg = ensureConfigLoaded(cfg)
		sys.InstallPackages(cfg)
	case "reinstall":
		cfg = ensureConfigLoaded(cfg)
		sys.ReinstallPackages(cfg)
	case "update":
		runUpdate(cfg)
	default:
		fmt.Fprintf(os.Stderr, "Unknown packages command: %s\n", args[0])
		fmt.Fprintf(os.Stderr, "Usage: construct sys packages [install|reinstall|update]\n")
		os.Exit(1)
	}
}

func handleConfigCommand(args []string) {
	if len(args) == 0 {
		sys.OpenConfig()
		return
	}

	switch args[0] {
	case "migrate":
		// Force refresh configuration and templates from binary.
		if err := migration.ForceRefresh(); err != nil {
			ui.GumError(fmt.Sprintf("Migration failed: %v", err))
			fmt.Fprintf(os.Stderr, "Please check your configuration files manually.\n")
			os.Exit(1)
		}
	case "restore":
		sys.RestoreConfig()
	default:
		fmt.Fprintf(os.Stderr, "Unknown config command: %s\n", args[0])
		fmt.Fprintf(os.Stderr, "Usage: construct sys config [migrate|restore]\n")
		os.Exit(1)
	}
}

func runUpdate(cfg *config.Config) {
	cfg = ensureConfigLoaded(cfg)
	sys.UpdateAgents(cfg)
}

func ensureConfigLoaded(cfg *config.Config) *config.Config {
	if cfg != nil {
		return cfg
	}
	loadedCfg, _, err := config.Load()
	if err != nil {
		ui.LogError(err)
		os.Exit(1)
	}
	return loadedCfg
}

func handleNetworkCommand(args []string) {
	command := args[0]
	switch command {
	case "allow":
		if len(args) < 2 {
			ui.GumError("Usage: construct sys network allow <domain|ip>")
			os.Exit(1)
		}
		network.AddRule(args[1], "allow")
	case "block":
		if len(args) < 2 {
			ui.GumError("Usage: construct sys network block <domain|ip>")
			os.Exit(1)
		}
		network.AddRule(args[1], "block")
	case "remove":
		if len(args) < 2 {
			ui.GumError("Usage: construct sys network remove <domain|ip>")
			os.Exit(1)
		}
		network.RemoveRule(args[1])
	case "list":
		network.ListRules()
	case "status":
		network.ShowStatus()
	case "clear":
		network.ClearRules()
	case "explain":
		if len(args) < 2 {
			ui.GumError("Usage: construct sys network explain <host[:port]>")
			os.Exit(1)
		}
		network.Explain(args[1])
	case "log", "logs":
		network.ShowLog(args[1:])
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
	case "restart":
		daemon.Restart()
	case "recreate":
		daemon.Recreate()
	case "attach":
		daemon.Attach()
	case "status":
		daemon.Status()
	case "install":
		daemon.InstallService()
	case "uninstall":
		daemon.UninstallService()
	case "roots":
		if len(args) < 2 {
			ui.GumError("roots requires a subcommand: list | add <path> | forget <path>")
			os.Exit(1)
		}
		switch args[1] {
		case "list":
			daemon.RootsList()
		case "add":
			if len(args) < 3 {
				ui.GumError("add requires a path argument")
				os.Exit(1)
			}
			daemon.RootsAdd(args[2])
		case "forget":
			if len(args) < 3 {
				ui.GumError("forget requires a path argument")
				os.Exit(1)
			}
			daemon.RootsForget(args[2])
		default:
			ui.GumError(fmt.Sprintf("Unknown roots subcommand: %s", args[1]))
			os.Exit(1)
		}
	case "idle-watch":
		// Invoked as a detached process by MaybeSpawnIdleWatcher. Not
		// meant for direct user invocation; kept in the dispatcher so
		// the binary can re-exec itself for the watch loop without a
		// separate helper.
		minutes := daemon.IdleWatchMinutesFromArgs(args[1:])
		runtime.IdleWatchRun(minutes)
	default:
		ui.GumError(fmt.Sprintf("Unknown daemon command: %s", command))
		ui.PrintSysDaemonHelp()
		os.Exit(1)
	}
}

// parseExecArgs splits args after "exec" on the first "--" separator.
// Returns everything after "--" as the command to run inside the container.
// Returns nil if no "--" is found or nothing follows it.
func parseExecArgs(args []string) []string {
	for i, arg := range args {
		if arg == "--" {
			if i+1 < len(args) {
				return args[i+1:]
			}
			return nil
		}
	}
	return nil
}

func shouldRunMigration(args []string) bool {
	if len(args) == 0 {
		return false
	}

	switch args[0] {
	case "doctor", "agents", "update":
		return true
	case "sys":
		if len(args) < 2 {
			return false
		}
		switch args[1] {
		case "image":
			if len(args) < 3 {
				return false
			}
			switch args[2] {
			case "pull", "init", "reset":
				return true
			default:
				return false
			}
		case "shims":
			if len(args) < 3 {
				return false
			}
			switch args[2] {
			case "install", "uninstall":
				return true
			default:
				return false
			}
		case "update", "shell", "exec", "version", "help",
			"config", "packages", "agents", "toolchains", "toolchain", "doctor",
			"ssh", "auth", "daemon":
			return true
		case "self-update":
			return false
		case "network", "net":
			if len(args) < 3 {
				return false
			}
			switch args[2] {
			case "allow", "block", "remove", "list", "status", "clear", "explain", "log", "logs":
				return true
			default:
				return false
			}
		default:
			return false
		}
	case "cc", "claude":
		return true
	default:
		return agent.IsSupported(args[0])
	}
}

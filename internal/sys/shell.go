package sys

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/EstebanForge/construct-cli/internal/agent"
	"github.com/EstebanForge/construct-cli/internal/ui"
	"github.com/EstebanForge/construct-cli/internal/update"
)

// EnsureCtSymlink silently creates ~/.local/bin/ct symlink if needed.
func EnsureCtSymlink() {
	target, err := buildCtTarget()
	if err != nil {
		return
	}

	// Check if ct command already exists in PATH
	ctPath, err := exec.LookPath("ct")
	if err == nil {
		// ct exists - check if it's pointing to our binary
		ctPathRaw := ctPath
		resolvedPath, err := filepath.EvalSymlinks(ctPath)
		if err != nil {
			// Failed to resolve, skip silently
			return
		}
		ctPath = resolvedPath
		if ctPath != target.resolved {
			if shouldReplaceBrewCt(ctPathRaw, ctPath) {
				replaceSymlink(ctPathRaw, target.path)
			}
			// ct exists but points to something else - silently skip
			// (user already has a ct command, don't interfere)
			return
		}
		// ct already points to our binary, all good
		return
	}

	// Try to create symlink in ~/.local/bin silently
	createSymlinkInLocalBin(target.path)
}

func createSymlinkInLocalBin(exePath string) bool {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return false
	}

	localBin := filepath.Join(homeDir, ".local", "bin")
	ctSymlink := filepath.Join(localBin, "ct")

	// Create ~/.local/bin if it doesn't exist
	if err := os.MkdirAll(localBin, 0755); err != nil {
		return false
	}

	// Check if symlink already exists
	if fileExists(ctSymlink) {
		// Check if it points to our binary
		targetResolved, err := filepath.EvalSymlinks(ctSymlink)
		exeResolved := exePath
		if resolved, resolveErr := filepath.EvalSymlinks(exePath); resolveErr == nil {
			exeResolved = resolved
		}
		if err == nil && targetResolved == exeResolved {
			return false // Already pointing to us
		}
		// Exists but points elsewhere - don't overwrite
		return false
	}

	// Create the symlink
	if err := os.Symlink(exePath, ctSymlink); err != nil {
		return false
	}

	// Ensure ~/.local/bin is in PATH (add to shell configs if needed)
	ensureLocalBinInPath(localBin)

	return true
}

type ctTarget struct {
	path     string
	resolved string
}

func buildCtTarget() (ctTarget, error) {
	// Try to find construct in PATH first (prefer installed version over local dev build)
	var exePath string
	pathCmd, err := exec.LookPath("construct")
	if err != nil {
		// Fall back to current executable if construct not in PATH
		pathCmd = ""
		exePath, err = os.Executable()
		if err != nil {
			return ctTarget{}, err
		}
		// Resolve symlinks to get real path for local builds
		exePath, err = filepath.EvalSymlinks(exePath)
		if err != nil {
			return ctTarget{}, err
		}
	} else {
		exePath = pathCmd
	}

	exeResolved := exePath
	if resolved, resolveErr := filepath.EvalSymlinks(exePath); resolveErr == nil {
		exeResolved = resolved
	}
	if update.IsBrewInstalled() {
		if preferred := preferredBrewConstructPath(pathCmd, exeResolved); preferred != "" {
			exePath = preferred
			if resolved, resolveErr := filepath.EvalSymlinks(preferred); resolveErr == nil {
				exeResolved = resolved
			} else {
				exeResolved = preferred
			}
		}
	}

	return ctTarget{path: exePath, resolved: exeResolved}, nil
}

// FixCtSymlink ensures ~/.local/bin/ct points to the current Construct binary.
func FixCtSymlink() (bool, string, error) {
	target, err := buildCtTarget()
	if err != nil {
		return false, "", err
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return false, "", err
	}

	localBin := filepath.Join(homeDir, ".local", "bin")
	ctSymlink := filepath.Join(localBin, "ct")

	if err := os.MkdirAll(localBin, 0755); err != nil {
		return false, "", err
	}

	if info, statErr := os.Lstat(ctSymlink); statErr == nil {
		if info.Mode()&os.ModeSymlink == 0 {
			return false, fmt.Sprintf("ct exists but is not a symlink: %s", ctSymlink), nil
		}

		targetResolved, err := filepath.EvalSymlinks(ctSymlink)
		if err == nil && targetResolved == target.resolved {
			return false, fmt.Sprintf("ct already points to %s", target.path), nil
		}

		if err := os.Remove(ctSymlink); err != nil {
			return false, "", err
		}
	}

	if err := os.Symlink(target.path, ctSymlink); err != nil {
		return false, "", err
	}

	ensureLocalBinInPath(localBin)
	return true, fmt.Sprintf("ct now points to %s", target.path), nil
}

func preferredBrewConstructPath(pathCmd, exeResolved string) string {
	if pathCmd != "" {
		switch pathCmd {
		case "/opt/homebrew/bin/construct", "/usr/local/bin/construct", "/home/linuxbrew/.linuxbrew/bin/construct":
			return pathCmd
		}
	}

	switch {
	case strings.Contains(exeResolved, "/opt/homebrew/Cellar/construct-cli/"):
		return "/opt/homebrew/bin/construct"
	case strings.Contains(exeResolved, "/usr/local/Cellar/construct-cli/"):
		return "/usr/local/bin/construct"
	case strings.Contains(exeResolved, "/home/linuxbrew/.linuxbrew/Cellar/construct-cli/"):
		return "/home/linuxbrew/.linuxbrew/bin/construct"
	default:
		return ""
	}
}

func shouldReplaceBrewCt(ctPath, ctResolved string) bool {
	if !update.IsBrewInstalled() {
		return false
	}
	if !isBrewCellarPath(ctResolved) {
		return false
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	localCt := filepath.Join(homeDir, ".local", "bin", "ct")
	return ctPath == localCt
}

func isBrewCellarPath(path string) bool {
	return strings.Contains(path, "/opt/homebrew/Cellar/construct-cli/") ||
		strings.Contains(path, "/usr/local/Cellar/construct-cli/") ||
		strings.Contains(path, "/home/linuxbrew/.linuxbrew/Cellar/construct-cli/")
}

func replaceSymlink(targetPath, exePath string) {
	if err := os.Remove(targetPath); err != nil {
		return
	}
	if err := os.Symlink(exePath, targetPath); err != nil {
		return
	}
}

func ensureLocalBinInPath(localBin string) {
	// Check if ~/.local/bin is already in PATH
	pathEnv := os.Getenv("PATH")
	if strings.Contains(pathEnv, localBin) {
		return // Already in PATH
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return
	}

	// Detect user's shell
	shell := os.Getenv("SHELL")
	if shell == "" {
		return
	}

	var configFile string
	var pathLine string

	// Determine config file and PATH export line based on shell
	if strings.Contains(shell, "zsh") {
		configFile = filepath.Join(homeDir, ".zshrc")
		pathLine = "\n# Add ~/.local/bin to PATH\nexport PATH=\"$HOME/.local/bin:$PATH\"\n"
	} else if strings.Contains(shell, "bash") {
		configFile = filepath.Join(homeDir, ".bashrc")
		if _, statErr := os.Stat(configFile); os.IsNotExist(statErr) {
			configFile = filepath.Join(homeDir, ".bash_profile")
		}
		pathLine = "\n# Add ~/.local/bin to PATH\nexport PATH=\"$HOME/.local/bin:$PATH\"\n"
	} else if strings.Contains(shell, "fish") {
		configFile = filepath.Join(homeDir, ".config/fish/config.fish")
		pathLine = "\n# Add ~/.local/bin to PATH\nset -gx PATH $HOME/.local/bin $PATH\n"
	} else {
		return
	}

	// Check if PATH line already exists
	if fileExists(configFile) {
		content, readErr := os.ReadFile(configFile)
		if readErr == nil && strings.Contains(string(content), ".local/bin") {
			return // Already added
		}
	}

	// Append PATH export to config file silently (ignore errors)
	f, err := os.OpenFile(configFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	//nolint:errcheck // Intentionally ignoring errors for silent operation
	defer f.Close()

	//nolint:errcheck // Intentionally ignoring errors for silent operation
	f.WriteString(pathLine)
}

// InstallAliases writes shell aliases for supported agents.
func InstallAliases() {
	// Determine the command to use in aliases
	// Prefer 'construct' if available in PATH (Homebrew, system install)
	// Otherwise use full path (local dev builds)
	var constructCmd string
	if pathCmd, err := exec.LookPath("construct"); err == nil {
		// construct is in PATH - use the command name only
		// This ensures version-independent aliases for Homebrew installs
		constructCmd = "construct"
		_ = pathCmd // Explicitly ignore the returned path
	} else {
		// construct not in PATH - fall back to full executable path
		exePath, err := os.Executable()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error determining executable path: %v\n", err)
			os.Exit(1)
		}
		// Resolve symlinks
		exePath, err = filepath.EvalSymlinks(exePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error resolving symlinks: %v\n", err)
			os.Exit(1)
		}
		constructCmd = exePath
	}

	// Standard agents
	agents := make([]string, 0, len(agent.SupportedAgents))
	for _, a := range agent.SupportedAgents {
		agents = append(agents, a.Slug)
	}

	// CC providers (prefixed with cc-)
	ccProviders := []string{"zai", "minimax", "kimi", "qwen", "mimo"}

	shellInfo, err := getShellInfo()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error determining shell config: %v\n", err)
		os.Exit(1)
	}
	configFile := shellInfo.configFile

	// UX: Explain what's happening
	if ui.GumAvailable() {
		// Use Gum style for explanation
		cmd := ui.GetGumCommand("style", "--foreground", "212", "--border", "rounded", "--padding", "1 2", "--margin", "1 0",
			"This command will install shell aliases for all supported AI agents.",
			"",
			"From now on, when you type:",
			"  • claude",
			"  • gemini",
			"  • qwen",
			"",
			"...they will automatically run inside The Construct.",
			"Your agents will be sandboxed, and will only have access to the current directory where you call them.")
		cmd.Stdout = os.Stdout
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to render alias info: %v\n", err)
		}
	} else {
		fmt.Println("\nThis command will install shell aliases for all supported AI agents.")
		fmt.Println("From now on, commands like 'claude', 'gemini', 'qwen' will run inside The Construct.")
		fmt.Println("Your agents will be sandboxed, and will only have access to the current directory where you call them.")
	}

	fmt.Printf("Target command: %s\n", constructCmd)
	fmt.Printf("Config file:    %s\n\n", configFile)

	fmt.Println("Aliases to be installed:")
	// Preview aliases
	for _, agent := range agents {
		fmt.Printf("  • alias %-10s = construct %s\n", agent, agent)
	}
	fmt.Println("\nCC Provider aliases:")
	for _, provider := range ccProviders {
		fmt.Printf("  • alias cc-%-7s = construct cc %s\n", provider, provider)
	}
	fmt.Println()

	// Check for non-sandboxed agents and create ns- aliases
	var nsAliases []nsAlias
	for _, agent := range agents {
		// Check if agent binary exists in PATH
		if agentPath, err := exec.LookPath(agent); err == nil {
			resolvedPath := resolveBinaryPath(agentPath)
			nsAliases = append(nsAliases, nsAlias{agent: agent, path: resolvedPath})
			fmt.Printf("  • %s (non-sandboxed)\n", formatNSFunctionPreview(shellInfo.shellType, agent, resolvedPath))
		}
	}
	if len(nsAliases) > 0 {
		fmt.Println("\nNon-sandboxed (ns-) aliases:")
		fmt.Println("  These allow running agents directly without The Construct sandbox.")
	}
	fmt.Println()

	// Check if block already exists
	contentBytes, err := os.ReadFile(configFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading config file: %v\n", err)
		os.Exit(1)
	}
	content := string(contentBytes)
	aliasesExist := strings.Contains(content, "# construct-cli aliases start")

	if aliasesExist {
		if ui.GumAvailable() {
			ui.GumWarning(fmt.Sprintf("Aliases are already installed in %s", configFile))
			if !ui.GumConfirm("Do you want to re-install to update them?") {
				fmt.Println("Canceled.")
				return
			}
			// Backup config before modification
			if err := backupConfigFile(configFile); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
			}
			// Remove old alias block
			startIdx := strings.Index(content, "# construct-cli aliases start")
			endIdx := strings.Index(content, "# construct-cli aliases end")
			if startIdx != -1 && endIdx != -1 {
				// Find the end of the line with "# construct-cli aliases end"
				endLineIdx := strings.Index(content[endIdx:], "\n") + endIdx + 1
				newContent := content[:startIdx] + content[endLineIdx:]
				if err := os.WriteFile(configFile, []byte(newContent), 0644); err != nil {
					fmt.Fprintf(os.Stderr, "Error removing old aliases: %v\n", err)
					os.Exit(1)
				}
			}
		} else {
			fmt.Printf("⚠️  Aliases are already installed in %s\n", configFile)
			fmt.Print("Do you want to re-install to update them? [y/N]: ")
			reader := bufio.NewReader(os.Stdin)
			response, err := reader.ReadString('\n')
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error reading input: %v\n", err)
				os.Exit(1)
			}
			response = strings.TrimSpace(response)
			if strings.ToLower(response) != "y" {
				fmt.Println("Canceled.")
				return
			}
			// Backup config before modification
			if err := backupConfigFile(configFile); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
			}
			// Remove old alias block
			startIdx := strings.Index(content, "# construct-cli aliases start")
			endIdx := strings.Index(content, "# construct-cli aliases end")
			if startIdx != -1 && endIdx != -1 {
				endLineIdx := strings.Index(content[endIdx:], "\n") + endIdx + 1
				newContent := content[:startIdx] + content[endLineIdx:]
				if err := os.WriteFile(configFile, []byte(newContent), 0644); err != nil {
					fmt.Fprintf(os.Stderr, "Error removing old aliases: %v\n", err)
					os.Exit(1)
				}
			}
		}
		fmt.Println("✓ Removed old aliases")
	}

	// Confirm (skip if already confirmed for re-install)
	if !aliasesExist {
		if ui.GumAvailable() {
			if !ui.GumConfirm("Do you want to proceed?") {
				fmt.Println("Canceled.")
				return
			}
		} else {
			fmt.Print("Do you want to proceed? [y/N]: ")
			reader := bufio.NewReader(os.Stdin)
			response, err := reader.ReadString('\n')
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error reading input: %v\n", err)
				os.Exit(1)
			}
			response = strings.TrimSpace(response)
			if strings.ToLower(response) != "y" {
				fmt.Println("Canceled.")
				return
			}
		}
	}

	// Build alias block
	var sb strings.Builder
	sb.WriteString("\n\n# construct-cli aliases start\n")

	// Add standard agents
	for _, agent := range agents {
		sb.WriteString(fmt.Sprintf("alias %s='%s %s'\n", agent, constructCmd, agent))
	}

	// Add CC providers
	for _, provider := range ccProviders {
		sb.WriteString(fmt.Sprintf("alias cc-%s='%s cc %s'\n", provider, constructCmd, provider))
	}

	// Add non-sandboxed (ns-) aliases for agents found in PATH
	if len(nsAliases) > 0 {
		sb.WriteString("\n# Non-sandboxed aliases - run agents directly without Construct sandbox\n")
		for _, nsAlias := range nsAliases {
			sb.WriteString(formatNSFunction(shellInfo.shellType, nsAlias.agent, nsAlias.path))
		}
	}

	sb.WriteString("# construct-cli aliases end\n")

	// Backup config before modification
	if err := backupConfigFile(configFile); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
	}

	// Append to file
	f, err := os.OpenFile(configFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening config file: %v\n", err)
		os.Exit(1)
	}
	defer func() {
		if closeErr := f.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to close config file: %v\n", closeErr)
		}
	}()

	if _, err := f.WriteString(sb.String()); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing to config file: %v\n", err)
		os.Exit(1)
	}

	totalAliases := len(agents) + len(ccProviders) + len(nsAliases)
	if ui.GumAvailable() {
		ui.GumSuccess(fmt.Sprintf("Successfully installed %d aliases to %s", totalAliases, configFile))
	} else {
		fmt.Printf("✅ Successfully installed %d aliases to %s\n", totalAliases, configFile)
	}

	fmt.Println()
	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error determining home directory: %v\n", err)
		os.Exit(1)
	}
	displayPath := strings.Replace(configFile, homeDir, "~", 1)
	fmt.Printf("To apply the changes without closing your current session, run: source %s\n", displayPath)
}

type nsAlias struct {
	agent string
	path  string
}

func resolveBinaryPath(agentPath string) string {
	resolvedPath := agentPath
	if resolved, err := filepath.EvalSymlinks(agentPath); err == nil {
		resolvedPath = resolved
	}
	if absPath, err := filepath.Abs(resolvedPath); err == nil {
		resolvedPath = absPath
	}
	return resolvedPath
}

func quoteShellPath(path string) string {
	escaped := strings.ReplaceAll(path, "\"", "\\\"")
	return fmt.Sprintf("\"%s\"", escaped)
}

func formatNSFunction(shellType, agent, path string) string {
	quotedPath := quoteShellPath(path)
	switch shellType {
	case "fish":
		return fmt.Sprintf("function ns-%s; %s $argv; end\n", agent, quotedPath)
	default:
		return fmt.Sprintf("ns-%s() { %s \"$@\"; }\n", agent, quotedPath)
	}
}

func formatNSFunctionPreview(shellType, agent, path string) string {
	quotedPath := quoteShellPath(path)
	switch shellType {
	case "fish":
		return fmt.Sprintf("function ns-%-8s = %s $argv; end", agent, quotedPath)
	default:
		return fmt.Sprintf("function ns-%-8s = %s \"$@\"", agent, quotedPath)
	}
}

type shellInfo struct {
	configFile string
	shellType  string
}

func getShellInfo() (shellInfo, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return shellInfo{}, err
	}

	shell := os.Getenv("SHELL")
	if shell == "" {
		return shellInfo{}, fmt.Errorf("no shell detected")
	}

	if strings.Contains(shell, "zsh") {
		return shellInfo{configFile: filepath.Join(homeDir, ".zshrc"), shellType: "zsh"}, nil
	} else if strings.Contains(shell, "bash") {
		configFile := filepath.Join(homeDir, ".bashrc")
		if _, statErr := os.Stat(configFile); os.IsNotExist(statErr) {
			return shellInfo{configFile: filepath.Join(homeDir, ".bash_profile"), shellType: "bash"}, nil
		}
		return shellInfo{configFile: configFile, shellType: "bash"}, nil
	} else if strings.Contains(shell, "fish") {
		return shellInfo{configFile: filepath.Join(homeDir, ".config/fish/config.fish"), shellType: "fish"}, nil
	}

	return shellInfo{}, fmt.Errorf("unsupported shell: %s", shell)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// backupConfigFile creates a timestamped backup of the config file
func backupConfigFile(configFile string) error {
	// Only backup if file exists
	if !fileExists(configFile) {
		return nil
	}

	// Create backup filename with timestamp
	timestamp := time.Now().Format("20060102-150405")
	backupPath := configFile + ".backup-" + timestamp

	// Read original file
	content, err := os.ReadFile(configFile)
	if err != nil {
		return fmt.Errorf("failed to read config file for backup: %w", err)
	}

	// Write backup
	if err := os.WriteFile(backupPath, content, 0644); err != nil {
		return fmt.Errorf("failed to create backup: %w", err)
	}

	// Show backup location to user
	displayPath := backupPath
	if homeDir, err := os.UserHomeDir(); err == nil {
		displayPath = strings.Replace(backupPath, homeDir, "~", 1)
	}
	if ui.GumAvailable() {
		fmt.Printf("%s  ✓ Backup created: %s%s\n", ui.ColorGrey, displayPath, ui.ColorReset)
	} else {
		fmt.Printf("  ✓ Backup created: %s\n", displayPath)
	}

	return nil
}

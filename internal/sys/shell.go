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
)

// EnsureCtSymlink silently creates ~/.local/bin/ct symlink if needed.
func EnsureCtSymlink() {
	// Try to find construct in PATH first (prefer installed version over local dev build)
	exePath, err := exec.LookPath("construct")
	if err != nil {
		// Fall back to current executable if construct not in PATH
		exePath, err = os.Executable()
		if err != nil {
			return // Can't determine path, skip
		}
	}

	// Resolve symlinks to get real path
	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		return
	}

	// Check if ct command already exists in PATH
	ctPath, err := exec.LookPath("ct")
	if err == nil {
		// ct exists - check if it's pointing to our binary
		resolvedPath, err := filepath.EvalSymlinks(ctPath)
		if err != nil {
			// Failed to resolve, skip silently
			return
		}
		ctPath = resolvedPath
		if ctPath != exePath {
			// ct exists but points to something else - silently skip
			// (user already has a ct command, don't interfere)
			return
		}
		// ct already points to our binary, all good
		return
	}

	// Try to create symlink in ~/.local/bin silently
	createSymlinkInLocalBin(exePath)
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
		if err == nil && targetResolved == exePath {
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
	// Get current executable path
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

	// Standard agents
	agents := make([]string, 0, len(agent.SupportedAgents))
	for _, a := range agent.SupportedAgents {
		agents = append(agents, a.Slug)
	}

	// CC providers (prefixed with cc-)
	ccProviders := []string{"zai", "minimax", "kimi", "qwen", "mimo"}

	configFile, err := getShellConfigFile()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error determining shell config: %v\n", err)
		os.Exit(1)
	}

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

	fmt.Printf("Target binary: %s\n", exePath)
	fmt.Printf("Config file:   %s\n\n", configFile)

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
	var nsAliases []string
	for _, agent := range agents {
		// Check if agent binary exists in PATH
		if path, err := exec.LookPath(agent); err == nil {
			nsAliases = append(nsAliases, fmt.Sprintf("%s|%s", agent, path))
			fmt.Printf("  • alias ns-%-8s = %s (non-sandboxed)\n", agent, agent)
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
		sb.WriteString(fmt.Sprintf("alias %s='%s %s'\n", agent, exePath, agent))
	}

	// Add CC providers
	for _, provider := range ccProviders {
		sb.WriteString(fmt.Sprintf("alias cc-%s='%s cc %s'\n", provider, exePath, provider))
	}

	// Add non-sandboxed (ns-) aliases for agents found in PATH
	if len(nsAliases) > 0 {
		sb.WriteString("\n# Non-sandboxed aliases - run agents directly without Construct sandbox\n")
		for _, nsAlias := range nsAliases {
			parts := strings.Split(nsAlias, "|")
			agent := parts[0]
			path := parts[1]
			sb.WriteString(fmt.Sprintf("alias ns-%s='%s'\n", agent, path))
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

func getShellConfigFile() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	shell := os.Getenv("SHELL")
	if shell == "" {
		return "", fmt.Errorf("no shell detected")
	}

	if strings.Contains(shell, "zsh") {
		return filepath.Join(homeDir, ".zshrc"), nil
	} else if strings.Contains(shell, "bash") {
		configFile := filepath.Join(homeDir, ".bashrc")
		if _, statErr := os.Stat(configFile); os.IsNotExist(statErr) {
			return filepath.Join(homeDir, ".bash_profile"), nil
		}
		return configFile, nil
	} else if strings.Contains(shell, "fish") {
		return filepath.Join(homeDir, ".config/fish/config.fish"), nil
	}

	return "", fmt.Errorf("unsupported shell: %s", shell)
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

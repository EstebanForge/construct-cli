package sys

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/EstebanForge/construct-cli/internal/agent"
	"github.com/EstebanForge/construct-cli/internal/ui"
)

// SuggestAliasSetup attempts to install a ct alias or symlink for the user.
func SuggestAliasSetup() {
	// Get the full path to the current executable
	exePath, err := os.Executable()
	if err != nil {
		return // Can't determine path, skip
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
			fmt.Fprintf(os.Stderr, "Warning: Failed to resolve ct symlink: %v\n", err)
			return
		}
		ctPath = resolvedPath
		if ctPath != exePath {
			// ct exists but points to something else - warn user
			fmt.Println("\n⚠️  Warning: A 'ct' command already exists on your system")
			fmt.Printf("   Location: %s\n", ctPath)
			fmt.Println("   Please resolve this conflict manually if you want to use 'ct' as a The Construct alias.")
			return
		}
		// ct already points to our binary, all good
		return
	}

	// Priority 1: Try to create symlink in ~/.local/bin
	if createSymlinkInLocalBin(exePath) {
		fmt.Println("\n✓ Symlink 'ct' created in ~/.local/bin")
		fmt.Println("  The 'ct' command is now available system-wide.")
		return
	}

	// Priority 2: Fall back to shell alias
	aliasCmd := fmt.Sprintf("alias ct='%s'", exePath)
	configFile, added, err := addShellAlias("ct", aliasCmd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to add shell alias: %v\n", err)
	} else if added {
		fmt.Println("\n✓ Shell alias 'ct' created successfully!")
		fmt.Printf("  Run: source %s\n", configFile)
		return
	}

	// Priority 3: Give up, suggest manual
	fmt.Println("\n💡 Tip: You can create a 'ct' alias for 'construct'")
	fmt.Println("   Symlink: ln -s " + exePath + " ~/.local/bin/ct")
	fmt.Println("   Or alias: alias ct='" + exePath + "'")
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
			return false // Already pointing to us, but don't announce
		}
		// Exists but points elsewhere - don't overwrite
		return false
	}

	// Create the symlink
	if err := os.Symlink(exePath, ctSymlink); err != nil {
		return false
	}

	// Ensure ~/.local/bin is in PATH (add to shell configs if needed)
	if err := ensureLocalBinInPath(localBin); err != nil {
		ui.LogWarning("Failed to add ~/.local/bin to PATH: %v", err)
	}

	return true
}

func ensureLocalBinInPath(localBin string) error {
	// Check if ~/.local/bin is already in PATH
	pathEnv := os.Getenv("PATH")
	if strings.Contains(pathEnv, localBin) {
		return nil // Already in PATH
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	// Detect user's shell
	shell := os.Getenv("SHELL")
	if shell == "" {
		return fmt.Errorf("no shell detected")
	}

	var configFile string
	var pathLine string

	// Determine config file and PATH export line based on shell
	if strings.Contains(shell, "zsh") {
		configFile = filepath.Join(homeDir, ".zshrc")
		pathLine = "\n# Add ~/.local/bin to PATH\nexport PATH=\"~/.local/bin:$PATH\"\n"
	} else if strings.Contains(shell, "bash") {
		configFile = filepath.Join(homeDir, ".bashrc")
		if _, statErr := os.Stat(configFile); os.IsNotExist(statErr) {
			configFile = filepath.Join(homeDir, ".bash_profile")
		}
		pathLine = "\n# Add ~/.local/bin to PATH\nexport PATH=\"~/.local/bin:$PATH\"\n"
	} else if strings.Contains(shell, "fish") {
		configFile = filepath.Join(homeDir, ".config/fish/config.fish")
		pathLine = "\n# Add ~/.local/bin to PATH\nset -gx PATH $HOME/.local/bin $PATH\n"
	} else {
		return fmt.Errorf("unsupported shell: %s", shell)
	}

	// Check if PATH line already exists
	if fileExists(configFile) {
		content, readErr := os.ReadFile(configFile)
		if readErr == nil && strings.Contains(string(content), ".local/bin") {
			return nil // Already added
		}
	}

	// Append PATH export to config file
	f, err := os.OpenFile(configFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := f.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to close config file: %v\n", closeErr)
		}
	}()

	if _, err := f.WriteString(pathLine); err != nil {
		return err
	}

	return nil
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

	// Check if block already exists
	contentBytes, err := os.ReadFile(configFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading config file: %v\n", err)
		os.Exit(1)
	}
	content := string(contentBytes)
	if strings.Contains(content, "# construct-cli aliases start") {
		if ui.GumAvailable() {
			ui.GumWarning(fmt.Sprintf("Aliases are already installed in %s", configFile))
		} else {
			fmt.Printf("⚠️  Aliases are already installed in %s\n", configFile)
		}
		return
	}

	// Confirm
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

	sb.WriteString("# construct-cli aliases end\n")

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

	totalAliases := len(agents) + len(ccProviders)
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

func addShellAlias(aliasName, aliasLine string) (string, bool, error) {
	configFile, err := getShellConfigFile()
	if err != nil {
		return "", false, err
	}

	// Check if alias already exists
	if fileExists(configFile) {
		content, readErr := os.ReadFile(configFile)
		if readErr == nil && strings.Contains(string(content), fmt.Sprintf("alias %s=", aliasName)) {
			return configFile, false, nil
		}
	}

	f, err := os.OpenFile(configFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return configFile, false, err
	}
	defer func() {
		if closeErr := f.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to close config file: %v\n", closeErr)
		}
	}()

	if _, err := f.WriteString(fmt.Sprintf("\n%s\n", aliasLine)); err != nil {
		return configFile, false, err
	}

	return configFile, true, nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Package sys provides system-level commands for the CLI.
package sys

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/EstebanForge/construct-cli/internal/config"
	"github.com/EstebanForge/construct-cli/internal/ui"
)

// OpenConfig opens the config file in the user's preferred editor
func OpenConfig() {
	configPath := filepath.Join(config.GetConfigDir(), "config.toml")

	// Ensure config exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		ui.GumInfo("Config file doesn't exist yet. Initializing...")
		config.Init()
		fmt.Println()
	}

	var cmd *exec.Cmd
	var editorName string

	// Detect environment and choose appropriate editor
	if ui.IsGUIEnvironment() {
		// GUI environment - use system default editor
		switch runtime.GOOS {
		case "darwin":
			cmd = exec.Command("open", configPath)
			editorName = "default macOS editor"
		case "linux":
			cmd = exec.Command("xdg-open", configPath)
			editorName = "default GUI editor"
		case "windows":
			cmd = exec.Command("cmd", "/c", "start", configPath)
			editorName = "default Windows editor"
		default:
			// Fallback to terminal editor
			cmd = ui.GetTerminalEditor(configPath)
			editorName = "terminal editor"
		}
	} else {
		// Headless/terminal environment
		cmd = ui.GetTerminalEditor(configPath)
		editorName = cmd.Args[0]
	}

	if err := cmd.Run(); err != nil {
		ui.GumError(fmt.Sprintf("Failed to open %s with %s: %v", configPath, editorName, err))
		os.Exit(1)
	}
}

// RestoreConfig restores config.toml from config.toml.backup
func RestoreConfig() {
	configDir := config.GetConfigDir()
	configPath := filepath.Join(configDir, "config.toml")
	backupPath := configPath + ".backup"

	// Check if backup exists
	if _, err := os.Stat(backupPath); os.IsNotExist(err) {
		ui.GumError("No configuration backup found to restore.")
		return
	}

	if ui.GumAvailable() {
		if !ui.GumConfirm("Are you sure you want to restore your config from the backup?") {
			fmt.Println("Restore canceled.")
			return
		}
	} else {
		fmt.Print("Restore config from backup? [y/N]: ")
		var resp string
		if _, err := fmt.Scanln(&resp); err != nil {
			resp = "n"
		}
		if strings.ToLower(resp) != "y" {
			return
		}
	}

	// Read backup
	data, err := os.ReadFile(backupPath)
	if err != nil {
		ui.GumError(fmt.Sprintf("Failed to read backup file: %v", err))
		return
	}

	// Create a safety backup of the "broken" current one just in case
	if err := os.Rename(configPath, configPath+".broken"); err != nil {
		ui.LogDebug("failed to create temporary broken backup: %v", err)
	}

	// Write backup to config.toml
	if err := os.WriteFile(configPath, data, 0644); err != nil {
		ui.GumError(fmt.Sprintf("Failed to restore config: %v", err))
		// Try to undo the rename
		if err := os.Rename(configPath+".broken", configPath); err != nil {
			ui.LogDebug("failed to restore from broken backup: %v", err)
		}
		return
	}

	// Clean up broken backup
	if err := os.Remove(configPath + ".broken"); err != nil {
		ui.LogDebug("failed to remove temporary broken backup: %v", err)
	}

	ui.GumSuccess("Configuration restored successfully from backup!")
}

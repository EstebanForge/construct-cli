package sys

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

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

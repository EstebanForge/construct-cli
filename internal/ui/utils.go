package ui

import (
	"os"
	"os/exec"
	"runtime"
)

// IsGUIEnvironment detects if running in a GUI environment
func IsGUIEnvironment() bool {
	// Check for X11
	if os.Getenv("DISPLAY") != "" {
		return true
	}

	// Check for Wayland
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		return true
	}

	// macOS always has GUI (unless SSH session)
	if runtime.GOOS == "darwin" {
		// Check if SSH session
		if os.Getenv("SSH_CONNECTION") == "" && os.Getenv("SSH_CLIENT") == "" {
			return true
		}
	}

	return false
}

// GetTerminalEditor returns the command for a terminal editor
func GetTerminalEditor(configPath string) *exec.Cmd {
	// Priority: EDITOR env var > nano > vim > vi
	if editor := os.Getenv("EDITOR"); editor != "" {
		return exec.Command(editor, configPath)
	}

	// Try nano first (more user-friendly)
	if _, err := exec.LookPath("nano"); err == nil {
		return exec.Command("nano", configPath)
	}

	// Try vim
	if _, err := exec.LookPath("vim"); err == nil {
		return exec.Command("vim", configPath)
	}

	// Fallback to vi (should be available everywhere)
	return exec.Command("vi", configPath)
}

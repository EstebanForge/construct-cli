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

// AgentMemory represents an agent's global instruction file
type AgentMemory struct {
	Name         string
	FriendlyName string
	Paths        []string // Multiple paths for fallback logic (e.g. Cline)
}

// GetSupportedAgents returns the list of agents supported by 'sys agents-md'
func GetSupportedAgents() []AgentMemory {
	return []AgentMemory{
		{Name: "gemini", FriendlyName: "Gemini CLI", Paths: []string{"~/.gemini/GEMINI.md"}},
		{Name: "qwen", FriendlyName: "Qwen CLI", Paths: []string{"~/.qwen/AGENTS.md"}},
		{Name: "opencode", FriendlyName: "OpenCode CLI", Paths: []string{"~/.config/opencode/AGENTS.md"}},
		{Name: "claude", FriendlyName: "Claude CLI", Paths: []string{"~/.claude/CLAUDE.md"}},
		{Name: "codex", FriendlyName: "Codex CLI", Paths: []string{"~/.codex/AGENTS.md"}},
		{Name: "copilot", FriendlyName: "Copilot CLI", Paths: []string{"~/.copilot/AGENTS.md"}},
		{Name: "cline", FriendlyName: "Cline CLI", Paths: []string{
			"~/Documents/Cline/Rules/AGENTS.md",
			"~/Cline/Rules/AGENTS.md",
		}},
	}
}

// ExpandPath expands the ~ to the Construct persistent home directory
func ExpandPath(path string) (string, error) {
	if !strings.HasPrefix(path, "~") {
		return path, nil
	}

	// The ~ in agent paths refers to the persistent home directory inside Construct
	// which is mapped to ~/.config/construct-cli/home/ on the host.
	constructHome := filepath.Join(config.GetConfigDir(), "home")

	return filepath.Join(constructHome, path[1:]), nil
}

// ListAgentMemories displays a selection list of supported agents
func ListAgentMemories() {
	agents := GetSupportedAgents()

	if ui.GumAvailable() {
		// Header
		fmt.Printf("%sThese are the main AGENTS.md files used for giving all agents rules and protocols to follow.%s\n", ui.ColorBold, ui.ColorReset)
		fmt.Printf("%sRead more about agent instructions in AGENTS.md.%s\n", ui.ColorGrey, ui.ColorReset)
		fmt.Printf("%sSelect an agent to edit its rules file; it will open in your default editor.%s\n\n", ui.ColorGrey, ui.ColorReset)

		var choices []string
		choices = append(choices, "Open all Agent Rules")
		for _, a := range agents {
			choices = append(choices, fmt.Sprintf("%s (%s)", a.FriendlyName, a.Paths[0]))
		}

		cmd := ui.GetGumCommand("choose")
		cmd.Args = append(cmd.Args, choices...)
		cmd.Stdin = os.Stdin
		cmd.Stderr = os.Stderr
		output, err := cmd.Output()
		if err != nil {
			// User likely canceled (e.g. Ctrl+C)
			return
		}

		selected := strings.TrimSpace(string(output))
		if selected == "Open all Agent Rules" {
			for _, a := range agents {
				OpenAgentMemory(a)
			}
			return
		}

		for _, a := range agents {
			if strings.HasPrefix(selected, a.FriendlyName) {
				OpenAgentMemory(a)
				return
			}
		}
	} else {
		// Fallback for environment without gum
		fmt.Println("These are the main AGENTS.md files used for giving all agents rules and protocols to follow.")
		fmt.Println("Select an agent to edit its rules file:")
		fmt.Println("0) Open all Agent Rules")
		for i, a := range agents {
			fmt.Printf("%d) %s (%s)\n", i+1, a.FriendlyName, a.Paths[0])
		}
		fmt.Print("\nEnter choice: ")
		var choice int
		if _, err := fmt.Scanln(&choice); err != nil || choice < 0 || choice > len(agents) {
			ui.GumError("Invalid choice")
			return
		}
		if choice == 0 {
			for _, a := range agents {
				OpenAgentMemory(a)
			}
		} else {
			OpenAgentMemory(agents[choice-1])
		}
	}
}

// OpenAgentMemory handles the existence check, creation, and opening of the rules file
func OpenAgentMemory(agent AgentMemory) {
	var targetPath string
	var err error

	// Handle path selection/discovery (especially for Cline)
	found := false
	for _, p := range agent.Paths {
		expanded, err := ExpandPath(p)
		if err != nil {
			ui.GumError(fmt.Sprintf("Failed to expand path %s: %v", p, err))
			return
		}
		if _, err := os.Stat(expanded); err == nil {
			targetPath = expanded
			found = true
			break
		}
	}

	// If none found, use the first one (primary)
	if !found {
		targetPath, err = ExpandPath(agent.Paths[0])
		if err != nil {
			ui.GumError(fmt.Sprintf("Failed to expand path %s: %v", agent.Paths[0], err))
			return
		}

		// Create file and parent directories
		if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
			ui.GumError(fmt.Sprintf("Failed to create directory %s: %v", filepath.Dir(targetPath), err))
			return
		}

		if _, err := os.Stat(targetPath); os.IsNotExist(err) {
			if err := os.WriteFile(targetPath, []byte(""), 0644); err != nil {
				ui.GumError(fmt.Sprintf("Failed to create rules file %s: %v", targetPath, err))
				return
			}
			ui.GumInfo(fmt.Sprintf("Created new rules file for %s", agent.FriendlyName))
		}
	}

	openInEditor(targetPath)
}

// openInEditor is a helper that reuses the logic from OpenConfig but for any file
func openInEditor(path string) {
	var cmd *exec.Cmd
	var editorName string

	if ui.IsGUIEnvironment() {
		switch runtime.GOOS {
		case "darwin":
			cmd = exec.Command("open", path)
			editorName = "default macOS editor"
		case "linux":
			cmd = exec.Command("xdg-open", path)
			editorName = "default GUI editor"
		case "windows":
			cmd = exec.Command("cmd", "/c", "start", path)
			editorName = "default Windows editor"
		default:
			cmd = ui.GetTerminalEditor(path)
			editorName = "terminal editor"
		}
	} else {
		cmd = ui.GetTerminalEditor(path)
		editorName = cmd.Args[0]
	}

	if err := cmd.Run(); err != nil {
		ui.GumError(fmt.Sprintf("Failed to open %s with %s: %v", path, editorName, err))
	}
}

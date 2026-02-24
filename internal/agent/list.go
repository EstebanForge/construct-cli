package agent

import (
	"fmt"
	"os"
	"strings"

	"github.com/EstebanForge/construct-cli/internal/ui"
)

// List prints the list of supported agents
func List() {
	if !ui.GumAvailable() {
		fmt.Println("Available Agents:")
		for _, agent := range SupportedAgents {
			fmt.Printf("  • %-15s (%s)\n", agent.Name, agent.Slug)
		}
		return
	}

	fmt.Println()
	cmd := ui.GetGumCommand("style", "--border", "rounded",
		"--padding", "1 2", "--bold", "Construct Available Agents")
	cmd.Stdout = os.Stdout
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to render header: %v\n", err)
	}
	fmt.Println()

	for _, agent := range SupportedAgents {
		cmd = ui.GetGumCommand("style", "--foreground", "212", fmt.Sprintf("• %s", agent.Name))
		cmd.Stdout = os.Stdout
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to render agent name: %v\n", err)
		}
		cmd = ui.GetGumCommand("style", "--foreground", "242", fmt.Sprintf("  Command: construct %s", agent.Slug))
		cmd.Stdout = os.Stdout
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to render agent command: %v\n", err)
		}
		// Convert container path to host path
		hostConfigPath := strings.TrimPrefix(agent.ConfigPath, "/home/construct")
		cmd = ui.GetGumCommand("style", "--foreground", "242", fmt.Sprintf("  Config:  ~/.config/construct-cli/home%s", hostConfigPath))
		cmd.Stdout = os.Stdout
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to render agent config path: %v\n", err)
		}
		fmt.Println()
	}
}

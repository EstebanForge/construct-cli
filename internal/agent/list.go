package agent

import (
	"fmt"
	"os"
	"os/exec"
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
	cmd := exec.Command("gum", "style", "--border", "rounded",
		"--padding", "1 2", "--bold", "Construct Available Agents")
	cmd.Stdout = os.Stdout
	cmd.Run()
	fmt.Println()

	for _, agent := range SupportedAgents {
		cmd = exec.Command("gum", "style", "--foreground", "212", fmt.Sprintf("• %s", agent.Name))
		cmd.Stdout = os.Stdout
		cmd.Run()
		cmd = exec.Command("gum", "style", "--foreground", "242", fmt.Sprintf("  Command: construct %s", agent.Slug))
		cmd.Stdout = os.Stdout
		cmd.Run()
		// Convert container path to host path
		hostConfigPath := strings.TrimPrefix(agent.ConfigPath, "/home/construct")
		cmd = exec.Command("gum", "style", "--foreground", "242", fmt.Sprintf("  Config:  ~/.config/construct-cli/home%s", hostConfigPath))
		cmd.Stdout = os.Stdout
		cmd.Run()
		fmt.Println()
	}
}

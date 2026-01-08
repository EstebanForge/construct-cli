package agent

import (
	"fmt"
	"os"
	"strings"

	"github.com/EstebanForge/construct-cli/internal/config"
	"github.com/EstebanForge/construct-cli/internal/ui"
)

// PrintCCHelp prints Claude Code provider alias usage.
func PrintCCHelp(cfg *config.Config) {
	help := `Construct CC - Claude Code Provider Aliases

Usage:
  construct cc <provider> [args...]      # Run Claude with provider config
  construct claude <provider> [args...]  # Alias for cc command

Available Providers:
`

	if len(cfg.Claude.Providers) == 0 {
		help += "  (none configured)\n\n"
		help += "Configure providers in ~/.config/construct-cli/config.toml\n"
		help += "Example:\n"
		help += "  [claude.cc.zai]\n"
		help += "  ANTHROPIC_BASE_URL = \"${CNSTR_ZAI_API_URL}\"\n"
		help += "  ANTHROPIC_AUTH_TOKEN = \"${CNSTR_ZAI_API_KEY}\"\n"
		help += "  API_TIMEOUT_MS = \"3000000\"\n"
	} else {
		for name, providerEnv := range cfg.Claude.Providers {
			help += fmt.Sprintf("  - %s", name)

			if len(providerEnv) > 0 {
				keys := make([]string, 0, len(providerEnv))
				for k := range providerEnv {
					keys = append(keys, k)
				}
				help += fmt.Sprintf(" (%s)", strings.Join(keys, ", "))
			}
			help += "\n"
		}
	}

	help += "\nExamples:\n"
	help += "  construct cc zai new-project      # Use zai provider\n"
	help += "  construct claude minimax --help   # Use minimax provider\n"

	if ui.GumAvailable() {
		cmd := ui.GetGumCommand("format", help)
		cmd.Stdout = os.Stdout
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to render help: %v\n", err)
		}
	} else {
		fmt.Print(help)
	}
}

// Package agent handles agent discovery and execution.
package agent

// Agent represents a supported AI agent tool
type Agent struct {
	Name       string // Human-readable name
	Slug       string // Command/folder name (e.g., "claude")
	ConfigPath string // Path inside container (e.g., "/home/construct/.claude")
}

// SupportedAgents defines the list of agents with direct configuration mounting
var SupportedAgents = []Agent{
	{Name: "Google Gemini", Slug: "gemini", ConfigPath: "/home/construct/.gemini"},
	{Name: "Claude Code", Slug: "claude", ConfigPath: "/home/construct/.claude"},
	{Name: "Qwen Code", Slug: "qwen", ConfigPath: "/home/construct/.qwen"},
	{Name: "GitHub Copilot", Slug: "copilot", ConfigPath: "/home/construct/.copilot"},
	{Name: "OpenCode", Slug: "opencode", ConfigPath: "/home/construct/.config/opencode"},
	{Name: "Cline", Slug: "cline", ConfigPath: "/home/construct/.cline"},
	{Name: "OpenAI Codex", Slug: "codex", ConfigPath: "/home/construct/.codex"},
}

// IsSupported checks if an agent slug is supported.
func IsSupported(slug string) bool {
	for _, a := range SupportedAgents {
		if a.Slug == slug {
			return true
		}
	}
	// Also allow "shell" as a special case for manual container access
	return slug == "shell"
}

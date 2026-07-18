package agent

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/EstebanForge/construct-cli/internal/ui"
)

// agentIntegration describes a single host-side integration file construct
// mirrors into the container agent home so host tools (e.g. Herdr's per-agent
// state reporter) keep working inside the box. Each entry is scoped to one
// agent slug; unknown agents get no sync.
type agentIntegration struct {
	slug string
	// relPath is the path of the integration file relative to the agent's
	// config dir (e.g. "agent/extensions/herdr-agent-state.ts").
	relPath string
	// hostDir resolves the host-side agent config dir (the dir that relPath is
	// joined under). Returns "" when the agent is not installed on the host.
	hostDir func() string
	// targetDir resolves the container-side agent config dir on the host
	// filesystem (the bind-mounted ~/.config/construct-cli/home subtree).
	targetDir func(configPath string) string
}

// agentIntegrations is the per-agent registry. Add entries here as more agents
// gain Herdr (or other host tool) integrations. Pi first; others validated later.
var agentIntegrations = []agentIntegration{
	{
		slug:    "pi",
		relPath: filepath.Join("extensions", "herdr-agent-state.ts"),
		hostDir: hostPiDir,
		targetDir: func(configPath string) string {
			return filepath.Join(configPath, "home", ".pi", "agent")
		},
	},
}

// hostPiDir resolves the host pi agent dir, mirroring Herdr's own resolution:
// PI_CODING_AGENT_DIR (used verbatim, tilde-expanded) if set, otherwise
// ~/.pi/agent. Returns "" if the host home dir cannot be determined.
func hostPiDir() string {
	if v := strings.TrimSpace(os.Getenv("PI_CODING_AGENT_DIR")); v != "" {
		return expandHome(v)
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".pi", "agent")
}

// expandHome replaces a leading "~" with the user home dir; non-tilde paths are
// returned unchanged.
func expandHome(p string) string {
	if p == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return p
		}
		return home
	}
	if strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return p
		}
		return filepath.Join(home, p[2:])
	}
	return p
}

// syncAgentIntegrations mirrors host-side integration files into the construct
// home for the agent named by args[0]. Missing agents, absent source files, and
// unchanged targets are skipped. A file is copied only when missing or when its
// SHA-256 differs from the source (so host-side updates propagate on the next
// run). All errors are best-effort: a failed sync must never block the run.
func syncAgentIntegrations(args []string, configPath string) {
	if len(args) == 0 || configPath == "" {
		return
	}
	slug := strings.ToLower(args[0])
	for _, integ := range agentIntegrations {
		if integ.slug != slug {
			continue
		}
		src := filepath.Join(integ.hostDir(), integ.relPath)
		dst := filepath.Join(integ.targetDir(configPath), integ.relPath)
		syncIntegrationFile(src, dst)
		return // one entry per slug
	}
}

// syncIntegrationFile copies src to dst only when dst is missing or differs in
// content. Source-missing is a no-op (the host tool isn't installed). Directory
// creation and the final write are the only mutations.
func syncIntegrationFile(src, dst string) {
	srcBytes, err := os.ReadFile(src)
	if err != nil {
		// Source not present: host tool not installed for this agent. Nothing to do.
		return
	}

	if existing, err := os.ReadFile(dst); err == nil {
		if sha256.Sum256(existing) == sha256.Sum256(srcBytes) {
			return // identical; no write needed
		}
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		ui.LogDebug("integration sync: mkdir failed for %s: %v", dst, err)
		return
	}
	if err := os.WriteFile(dst, srcBytes, 0644); err != nil {
		ui.LogDebug("integration sync: write failed for %s: %v", dst, err)
		return
	}
	fmt.Printf("✓ Synced Herdr integration for %s\n", filepath.Base(dst))
}

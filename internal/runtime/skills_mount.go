package runtime

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/EstebanForge/construct-cli/internal/config"
)

// agentSkillTarget maps an agent slug (matching internal/agent.SupportedAgents)
// to the guest path where its skills directory lives inside the sandbox.
// Hard-coded here because internal/runtime cannot import internal/agent
// (cycle: agent imports runtime via engine.go / runner.go / herdr_bridge.go).
// When a new agent is added to internal/agent/agent.go with a skills path,
// add the matching entry here. The drift is caught by TestAgentSkillTargets
// in runtime_test.go.
//
// Derived from the user's ~/Dev/EstebanForge/AGENTS/manage.sh mapping:
//   - "SkillsPath" entries map "<ConfigPath>/skills".
//   - codex / opencode / pi have no skills path (manage.sh leaves them as "-").
//   - zcode is in manage.sh but not in construct-cli's SupportedAgents list;
//     omitted here intentionally (out of scope until construct-cli ships it).
var agentSkillTarget = map[string]string{
	"agy":      "/home/construct/.antigravity/skills",
	"claude":   "/home/construct/.claude/skills",
	"amp":      "/home/construct/.config/amp/skills",
	"qwen":     "/home/construct/.qwen/skills",
	"copilot":  "/home/construct/.copilot/skills",
	"opencode": "/home/construct/.config/opencode/skills",
	"crush":    "/home/construct/.config/crush/skills",
	"droid":    "/home/construct/.factory/skills",
	"goose":    "/home/construct/.config/goose/skills",
	"kilocode": "/home/construct/.kilocode/skills",
	"cline":    "/home/construct/.cline/skills",
}

// SkillsMountTargets returns the per-agent guest skills destinations in a
// stable order (alphabetical by path) so docker-compose volumes and the
// msb mount map are emitted the same way every run. The hash covers only
// the COUNT, not the order, so reordering is a no-op for cache invalidation.
//
// Exported as SkillsMountTargets for cross-package use (e.g. internal/doctor).
func SkillsMountTargets() []string {
	targets := make([]string, 0, len(agentSkillTarget))
	for _, t := range agentSkillTarget {
		targets = append(targets, t)
	}
	// Stable order so emitted mount lines are deterministic.
	for i := 1; i < len(targets); i++ {
		for j := i; j > 0 && targets[j-1] > targets[j]; j-- {
			targets[j-1], targets[j] = targets[j], targets[j-1]
		}
	}
	return targets
}

// GetSkillsSourcePath resolves the host directory whose contents should be
// bind-mounted into each agent's skills location inside the sandbox.
//
// Precedence (first non-empty wins):
//  1. $CONSTRUCT_SKILLS_SOURCE environment variable (always wins; CI /
//     ephemeral hosts / multi-machine setups).
//  2. cfg.Sandbox.SkillsSource (non-empty user override).
//  3. Auto-detect, first existing path:
//     ~/Dev/EstebanForge/AGENTS/skills (the agent-forge layout)
//     ~/AGENTS/skills
//     ~/.config/construct-cli/skills (construct-cli's own canonical location)
//     $XDG_DATA_HOME/construct/skills (XDG default)
//
// Returns (path, true) only when cfg.Sandbox.MountSkills is enabled AND the
// resolved path exists as a directory. Returns ("", false) silently so the
// override hash does not change when the feature is off or the source is
// absent; the user opts out by clearing MountSkills or removing the source.
//
// Like getQmdModelsPath and getGlobalGitIgnorePath, $HOME is read directly so
// Go 1.24+ os.UserHomeDir changes do not affect tests that override HOME.
func GetSkillsSourcePath(cfg *config.Config) (string, bool) {
	if cfg == nil || !cfg.Sandbox.MountSkills {
		return "", false
	}

	homeDir := os.Getenv("HOME")
	if homeDir == "" {
		fallback, err := os.UserHomeDir()
		if err != nil {
			return "", false
		}
		homeDir = fallback
	}

	expandUser := func(p string) string {
		if p == "" {
			return ""
		}
		if strings.HasPrefix(p, "~/") {
			return filepath.Join(homeDir, p[2:])
		}
		return p
	}

	// 1. Environment variable override (always wins).
	if env := strings.TrimSpace(os.Getenv("CONSTRUCT_SKILLS_SOURCE")); env != "" {
		if resolved, ok := resolveSkillsDir(expandUser(env)); ok {
			return resolved, true
		}
		// Explicit override points at a non-existent path: fail closed,
		// do NOT fall through to auto-detect. The user asked for this
		// path; honoring a different one would surprise them.
		return "", false
	}

	// 2. Config-level override.
	if cfg.Sandbox.SkillsSource != "" {
		if resolved, ok := resolveSkillsDir(expandUser(cfg.Sandbox.SkillsSource)); ok {
			return resolved, true
		}
		return "", false
	}

	// 3. Auto-detect, first existing wins.
	candidates := []string{
		filepath.Join(homeDir, "Dev", "EstebanForge", "AGENTS", "skills"),
		filepath.Join(homeDir, "AGENTS", "skills"),
		filepath.Join(homeDir, ".config", "construct-cli", "skills"),
	}
	if xdgData := os.Getenv("XDG_DATA_HOME"); xdgData != "" {
		candidates = append(candidates, filepath.Join(xdgData, "construct", "skills"))
	} else {
		candidates = append(candidates, filepath.Join(homeDir, ".local", "share", "construct", "skills"))
	}
	for _, c := range candidates {
		if resolved, ok := resolveSkillsDir(c); ok {
			return resolved, true
		}
	}
	return "", false
}

// resolveSkillsDir returns the symlink-resolved absolute path of dir when it
// exists and is a directory; ("", false) otherwise. Symlink resolution
// matches the msb / Docker behavior: a literal-path bind against the
// resolved path survives moves / link swaps without surprise.
func resolveSkillsDir(dir string) (string, bool) {
	if dir == "" {
		return "", false
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return "", false
	}
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		return resolved, true
	}
	return dir, true
}

// SkillsMountOptions builds the docker compose third-field option string
// for the skills mounts. Default behavior (cfg.Sandbox.SkillsReadOnly = true)
// is read-only (`:ro`), the safe choice. Setting SkillsReadOnly = false
// emits no `:ro` option, allowing agents to author or edit skills. The
// selinuxSuffix argument is the standalone third-field suffix from the
// caller (`":z"` on Linux SELinux systems, `""` elsewhere); when combining
// `:ro` with selinux the function uses the comma variant (`:ro,z`)
// rather than `:ro:z` (which podman-compose rejects).
//
// Output examples:
//
//	cfg.Sandbox.SkillsReadOnly = true,  selinuxSuffix = ":z" -> ":ro,z"
//	cfg.Sandbox.SkillsReadOnly = true,  selinuxSuffix = ""  -> ":ro"
//	cfg.Sandbox.SkillsReadOnly = false, selinuxSuffix = ":z" -> ":z"
//	cfg.Sandbox.SkillsReadOnly = false, selinuxSuffix = ""  -> ""
func SkillsMountOptions(cfg *config.Config, selinuxSuffix string) string {
	if cfg == nil || !cfg.Sandbox.SkillsReadOnly {
		return selinuxSuffix
	}
	if selinuxSuffix == "" {
		return ":ro"
	}
	return ":ro," + strings.TrimPrefix(selinuxSuffix, ":")
}

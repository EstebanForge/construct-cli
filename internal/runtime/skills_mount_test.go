package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EstebanForge/construct-cli/internal/config"
)

// withSkillsTestHome resets HOME/XDG_DATA_HOME for the duration of a test so
// the resolver does not see the developer's machine. Returns the temp HOME
// the test should populate.
func withSkillsTestHome(t *testing.T) string {
	t.Helper()
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("CONSTRUCT_SKILLS_SOURCE", "")
	return tmpHome
}

// TestGetSkillsSourcePathDisabled confirms the resolver returns false when
// [sandbox] mount_skills is off, regardless of whether a skills dir exists.
func TestGetSkillsSourcePathDisabled(t *testing.T) {
	tmpHome := withSkillsTestHome(t)
	// Plant an obvious target so a missing-feature bug would surface.
	planted := filepath.Join(tmpHome, "Dev", "EstebanForge", "AGENTS", "skills")
	if err := os.MkdirAll(planted, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.Sandbox.MountSkills = false

	if src, ok := GetSkillsSourcePath(&cfg); ok {
		t.Errorf("expected disabled resolver to return (_, false), got (%q, true)", src)
	}
}

// TestGetSkillsSourcePathMissingFailsClosed confirms the resolver returns
// (empty, false) when nothing matches the auto-detect candidates.
func TestGetSkillsSourcePathMissingFailsClosed(t *testing.T) {
	withSkillsTestHome(t)
	cfg := config.DefaultConfig()
	if src, ok := GetSkillsSourcePath(&cfg); ok {
		t.Errorf("expected no source in empty HOME, got (%q, true)", src)
	}
}

// TestGetSkillsSourcePathAutoDetect walks the candidate list in priority
// order: a planted higher-priority candidate shadows a planted lower-priority
// one. This is the drift catcher for the auto-detect ordering.
func TestGetSkillsSourcePathAutoDetect(t *testing.T) {
	tmpHome := withSkillsTestHome(t)

	lowPrio := filepath.Join(tmpHome, ".local", "share", "construct", "skills")
	highPrio := filepath.Join(tmpHome, "Dev", "EstebanForge", "AGENTS", "skills")
	for _, d := range []string{lowPrio, highPrio} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	cfg := config.DefaultConfig()
	got, ok := GetSkillsSourcePath(&cfg)
	if !ok {
		t.Fatalf("expected auto-detect to find %s", highPrio)
	}
	// EvalSymlinks resolves tmp symlinks; on Linux t.TempDir may live under
	// a /tmp -> /private/tmp symlink on macOS, so compare resolved paths.
	resolvedHigh, _ := filepath.EvalSymlinks(highPrio)
	if got != resolvedHigh {
		t.Errorf("expected high-priority path %s (resolved %s), got %s", highPrio, resolvedHigh, got)
	}

	// Remove the high-priority one and confirm the resolver falls back to the
	// next candidate.
	if err := os.RemoveAll(filepath.Dir(filepath.Dir(filepath.Dir(highPrio)))); err != nil {
		t.Fatalf("cleanup high-priority dir: %v", err)
	}
	got, ok = GetSkillsSourcePath(&cfg)
	if !ok {
		t.Fatalf("expected auto-detect to fall back to %s", lowPrio)
	}
	resolvedLow, _ := filepath.EvalSymlinks(lowPrio)
	if got != resolvedLow {
		t.Errorf("expected fallback path %s (resolved %s), got %s", lowPrio, resolvedLow, got)
	}
}

// TestGetSkillsSourcePathConfigOverride confirms an explicit
// [sandbox] skills_source beats auto-detect and shadows a planted candidate.
func TestGetSkillsSourcePathConfigOverride(t *testing.T) {
	tmpHome := withSkillsTestHome(t)
	planted := filepath.Join(tmpHome, "Dev", "EstebanForge", "AGENTS", "skills")
	if err := os.MkdirAll(planted, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	overrideDir := filepath.Join(tmpHome, "my", "skills")
	if err := os.MkdirAll(overrideDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.Sandbox.SkillsSource = "~/my/skills"

	got, ok := GetSkillsSourcePath(&cfg)
	if !ok {
		t.Fatalf("expected config override to resolve")
	}
	resolvedOverride, _ := filepath.EvalSymlinks(overrideDir)
	if got != resolvedOverride {
		t.Errorf("expected override %s (resolved %s), got %s", overrideDir, resolvedOverride, got)
	}
}

// TestGetSkillsSourcePathEnvWinsOverConfig confirms the env var
// $CONSTRUCT_SKILLS_SOURCE beats [sandbox] skills_source.
func TestGetSkillsSourcePathEnvWinsOverConfig(t *testing.T) {
	tmpHome := withSkillsTestHome(t)
	configDir := filepath.Join(tmpHome, "config", "skills")
	envDir := filepath.Join(tmpHome, "env", "skills")
	for _, d := range []string{configDir, envDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	cfg := config.DefaultConfig()
	cfg.Sandbox.SkillsSource = "~/config/skills"
	t.Setenv("CONSTRUCT_SKILLS_SOURCE", filepath.Join(tmpHome, "env", "skills"))

	got, ok := GetSkillsSourcePath(&cfg)
	if !ok {
		t.Fatalf("expected env var to resolve")
	}
	resolvedEnv, _ := filepath.EvalSymlinks(envDir)
	if got != resolvedEnv {
		t.Errorf("expected env path %s (resolved %s), got %s", envDir, resolvedEnv, got)
	}
}

// TestGetSkillsSourcePathExplicitMissingFailsClosed confirms an explicit
// override pointing at a non-existent directory fails closed even when an
// auto-detect candidate exists. The user asked for THAT path; honoring a
// different one would surprise them.
func TestGetSkillsSourcePathExplicitMissingFailsClosed(t *testing.T) {
	tmpHome := withSkillsTestHome(t)
	planted := filepath.Join(tmpHome, "Dev", "EstebanForge", "AGENTS", "skills")
	if err := os.MkdirAll(planted, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.Sandbox.SkillsSource = "~/no/such/skills"

	if _, ok := GetSkillsSourcePath(&cfg); ok {
		t.Errorf("expected explicit-missing override to fail closed (return false)")
	}
}

// TestAgentSkillTargetDriftCatcher locks the canonical mount target list so
// drift between SkillsMountTargets() and the user's ~/Dev/EstebanForge/AGENTS
// manage.sh mapping is caught by CI, not by users noticing a missing skill.
func TestAgentSkillTargetDriftCatcher(t *testing.T) {
	want := []string{
		"/home/construct/.antigravity/skills",
		"/home/construct/.claude/skills",
		"/home/construct/.cline/skills",
		"/home/construct/.config/amp/skills",
		"/home/construct/.config/crush/skills",
		"/home/construct/.config/goose/skills",
		"/home/construct/.config/opencode/skills",
		"/home/construct/.copilot/skills",
		"/home/construct/.factory/skills",
		"/home/construct/.kilocode/skills",
		"/home/construct/.qwen/skills",
	}
	got := SkillsMountTargets()
	if len(got) != len(want) {
		t.Fatalf("target count drifted: got %d, want %d (got=%v)", len(got), len(want), got)
	}
	// SkillsMountTargets returns alphabetical order; want is in agent-forge
	// manage.sh order. Compare as sets to avoid coupling tests to sort order.
	seen := make(map[string]bool, len(got))
	for _, t := range got {
		seen[t] = true
	}
	for _, w := range want {
		if !seen[w] {
			t.Errorf("missing target %q (got=%v)", w, got)
		}
	}
}

// TestHashOverrideInputsIncludesSkillsSource confirms changing the skills
// source invalidates the override hash, forcing regeneration when the source
// appears or disappears.
func TestHashOverrideInputsIncludesSkillsSource(t *testing.T) {
	base := overrideInputs{Version: "1.0.0", Runtime: "docker", LoopbackPorts: "80,443"}
	withSkills := base
	withSkills.SkillsSource = "/tmp/skills"
	withSkills.SkillsTargetCount = 11
	withSkills.SkillsReadOnly = true

	if hashOverrideInputs(base) == hashOverrideInputs(withSkills) {
		t.Errorf("hash must change when SkillsSource changes")
	}

	countOnly := withSkills
	countOnly.SkillsTargetCount = 0
	if hashOverrideInputs(withSkills) == hashOverrideInputs(countOnly) {
		t.Errorf("hash must change when SkillsTargetCount changes")
	}

	// Toggling SkillsReadOnly must invalidate the override too: RO emits
	// `:ro` in the docker compose third field, RW does not.
	rwOnly := withSkills
	rwOnly.SkillsReadOnly = false
	if hashOverrideInputs(withSkills) == hashOverrideInputs(rwOnly) {
		t.Errorf("hash must change when SkillsReadOnly toggles")
	}
}

// TestGenerateDockerComposeOverrideMountsSkillsWhenEnabled confirms the
// override file emits one bind per supported agent when the source resolves,
// uses `:ro` for read-only (the safe default), and emits nothing when the
// feature is disabled.
func TestGenerateDockerComposeOverrideMountsSkillsWhenEnabled(t *testing.T) {
	tmpHome := withSkillsTestHome(t)
	configDir := filepath.Join(tmpHome, ".config", "construct-cli")
	containerDir := filepath.Join(configDir, "container")
	for _, d := range []string{containerDir, filepath.Join(configDir, "templates")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	// Write a minimal config.toml so config.Load() does not trigger Init()
	// (which would write a default override file and invalidate this test).
	if err := os.WriteFile(filepath.Join(configDir, "config.toml"),
		[]byte("[runtime]\nbackend = \"docker\"\n\n[sandbox]\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	planted := filepath.Join(tmpHome, "Dev", "EstebanForge", "AGENTS", "skills")
	if err := os.MkdirAll(planted, 0o755); err != nil {
		t.Fatalf("mkdir planted: %v", err)
	}

	t.Setenv("CONSTRUCT_PROJECT_PATH", "/projects/test")
	if err := GenerateDockerComposeOverride(configDir, "/projects/test", "bridge", "docker"); err != nil {
		t.Fatalf("GenerateDockerComposeOverride: %v", err)
	}
	overrideBytes, err := os.ReadFile(filepath.Join(containerDir, "docker-compose.override.yml"))
	if err != nil {
		t.Fatalf("read override: %v", err)
	}
	content := string(overrideBytes)

	resolvedSource, _ := filepath.EvalSymlinks(planted)
	for _, target := range SkillsMountTargets() {
		// Default (SkillsReadOnly = true): expect `:ro` after the target.
		want := resolvedSource + ":" + target + ":ro"
		if !strings.Contains(content, want) {
			t.Errorf("expected override to include %s, got:\n%s", want, content)
		}
	}

	// Disable the feature and confirm the mount lines vanish. We re-write
	// config.toml so config.Load() picks up the new [sandbox] block.
	cfg := config.DefaultConfig()
	cfg.Sandbox.MountSkills = false
	if err := writeConfigForTest(configDir, cfg); err != nil {
		t.Fatalf("write config: %v", err)
	}
	// Force a hash mismatch so the override regenerates despite the inputs
	// matching the stored one from the enabled case.
	hashPath := filepath.Join(containerDir, ".override_hash")
	if err := os.WriteFile(hashPath, []byte("force-regen"), 0o644); err != nil {
		t.Fatalf("force regen: %v", err)
	}
	if err := GenerateDockerComposeOverride(configDir, "/projects/test", "bridge", "docker"); err != nil {
		t.Fatalf("GenerateDockerComposeOverride (disabled): %v", err)
	}
	overrideBytes, err = os.ReadFile(filepath.Join(containerDir, "docker-compose.override.yml"))
	if err != nil {
		t.Fatalf("read override (disabled): %v", err)
	}
	content = string(overrideBytes)
	if strings.Contains(content, resolvedSource+":/home/construct/") {
		t.Errorf("expected override to NOT include skills mounts when disabled, got:\n%s", content)
	}
}

// TestGenerateDockerComposeOverrideMountsSkillsRWWhenOptIn confirms the
// override drops `:ro` when [sandbox] skills_read_only = false, allowing
// agents to author skills (writes flow back to the host).
func TestGenerateDockerComposeOverrideMountsSkillsRWWhenOptIn(t *testing.T) {
	tmpHome := withSkillsTestHome(t)
	configDir := filepath.Join(tmpHome, ".config", "construct-cli")
	containerDir := filepath.Join(configDir, "container")
	for _, d := range []string{containerDir, filepath.Join(configDir, "templates")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.toml"),
		[]byte("[runtime]\nbackend = \"docker\"\n\n[sandbox]\nskills_read_only = false\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	planted := filepath.Join(tmpHome, "Dev", "EstebanForge", "AGENTS", "skills")
	if err := os.MkdirAll(planted, 0o755); err != nil {
		t.Fatalf("mkdir planted: %v", err)
	}

	if err := GenerateDockerComposeOverride(configDir, "/projects/test", "bridge", "docker"); err != nil {
		t.Fatalf("GenerateDockerComposeOverride: %v", err)
	}
	overrideBytes, err := os.ReadFile(filepath.Join(containerDir, "docker-compose.override.yml"))
	if err != nil {
		t.Fatalf("read override: %v", err)
	}
	content := string(overrideBytes)
	resolvedSource, _ := filepath.EvalSymlinks(planted)

	// Skills mount lines must be present and MUST NOT carry `:ro`.
	for _, target := range SkillsMountTargets() {
		rwLine := resolvedSource + ":" + target
		if !strings.Contains(content, rwLine) {
			t.Errorf("expected override to include RW mount %s, got:\n%s", rwLine, content)
		}
		roLine := resolvedSource + ":" + target + ":ro"
		if strings.Contains(content, roLine) {
			t.Errorf("expected NO `:ro` on RW mount, but found %s in:\n%s", roLine, content)
		}
	}
}

// TestConditionalAutoMountsIncludesSkills confirms the microvm auto-mount
// helper emits one bind per supported agent when the source resolves,
// defaults to read-only, and emits nothing when the feature is disabled.
func TestConditionalAutoMountsIncludesSkills(t *testing.T) {
	tmpHome := withSkillsTestHome(t)
	planted := filepath.Join(tmpHome, "Dev", "EstebanForge", "AGENTS", "skills")
	if err := os.MkdirAll(planted, 0o755); err != nil {
		t.Fatalf("mkdir planted: %v", err)
	}

	cfg := config.DefaultConfig()
	mounts := conditionalAutoMounts(&cfg)
	if len(mounts) == 0 {
		t.Fatalf("expected skills mounts to be emitted, got none")
	}
	resolvedSource, _ := filepath.EvalSymlinks(planted)
	seen := make(map[string]bool, len(mounts))
	for _, m := range mounts {
		if m.Src != resolvedSource {
			continue
		}
		seen[m.Dest] = true
		// Default SkillsReadOnly = true; expect Readonly=true on every
		// skills mount. RW mode is exercised separately below.
		if !m.Readonly {
			t.Errorf("skills mount %s must default to Readonly=true, got Readonly=false", m.Dest)
		}
	}
	for _, target := range SkillsMountTargets() {
		if !seen[target] {
			t.Errorf("missing skills mount for %s in conditionalAutoMounts()", target)
		}
	}

	// Opt-in RW: every skills mount flips to Readonly=false.
	cfg.Sandbox.SkillsReadOnly = false
	mounts = conditionalAutoMounts(&cfg)
	for _, m := range mounts {
		if m.Src != resolvedSource {
			continue
		}
		if m.Readonly {
			t.Errorf("skills mount %s must be Readonly=false when opted into RW", m.Dest)
		}
	}

	// Disabled: no mounts.
	cfg.Sandbox.MountSkills = false
	mounts = conditionalAutoMounts(&cfg)
	for _, m := range mounts {
		if m.Src == resolvedSource {
			t.Errorf("expected no skills mount when disabled, got %+v", m)
		}
	}
}

// TestSkillsMountOptions exercises the option-builder across the four
// combinations of (SkillsReadOnly x selinuxSuffix) so the docker compose
// third field is correct on every host. The combined syntax must use a
// comma (`:ro,z`), not a second colon (`:ro:z`) — podman-compose rejects
// the latter. The selinuxArg is the standalone suffix (":z" or ""),
// matching what the docker compose generator passes to SkillsMountOptions.
func TestSkillsMountOptions(t *testing.T) {
	cases := []struct {
		name       string
		readOnly   bool
		selinuxArg string
		want       string
	}{
		{"ro+selinux", true, ":z", ":ro,z"},
		{"ro_only", true, "", ":ro"},
		{"rw+selinux", false, ":z", ":z"},
		{"rw_only", false, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.DefaultConfig()
			cfg.Sandbox.SkillsReadOnly = tc.readOnly
			got := SkillsMountOptions(&cfg, tc.selinuxArg)
			if got != tc.want {
				t.Errorf("SkillsMountOptions(readOnly=%v, selinuxSuffix=%q) = %q, want %q",
					tc.readOnly, tc.selinuxArg, got, tc.want)
			}
		})
	}
}

// TestMsbPathMapsMirrorsSkillsMounts confirms the host-exec bridge translation
// table tracks every conditionalAutoMounts entry that came from the skills
// source.
func TestMsbPathMapsMirrorsSkillsMounts(t *testing.T) {
	tmpHome := withSkillsTestHome(t)
	planted := filepath.Join(tmpHome, "Dev", "EstebanForge", "AGENTS", "skills")
	if err := os.MkdirAll(planted, 0o755); err != nil {
		t.Fatalf("mkdir planted: %v", err)
	}

	cfg := config.DefaultConfig()
	auto := conditionalAutoMounts(&cfg)
	// Build the source map MsbPathMaps should populate for the skills source.
	resolvedSource, _ := filepath.EvalSymlinks(planted)
	autoByDest := make(map[string]string, len(auto))
	for _, m := range auto {
		if m.Src == resolvedSource {
			autoByDest[m.Dest] = m.Src
		}
	}
	if len(autoByDest) == 0 {
		t.Skip("no skills mounts emitted; cannot verify mirror")
	}

	paths := MsbPathMaps(&cfg, "")
	bridged := make(map[string]string, len(paths))
	for _, p := range paths {
		bridged[p.Guest] = p.Host
	}
	for dest, src := range autoByDest {
		if bridged[dest] != src {
			t.Errorf("MsbPathMaps guest=%q host=%q; want %q", dest, bridged[dest], src)
		}
	}
}

// writeConfigForTest writes a minimal config.toml the resolver can read so
// the override generator picks up the desired [sandbox] block. Mirrors the
// shape produced by config.Load() for a sandbox-only override scenario.
// configDir is the construct config root (e.g. ~/.config/construct-cli),
// not $HOME.
func writeConfigForTest(configDir string, cfg config.Config) error {
	path := filepath.Join(configDir, "config.toml")
	body := "[sandbox]\nmount_skills = "
	if cfg.Sandbox.MountSkills {
		body += "true\n"
	} else {
		body += "false\n"
	}
	return os.WriteFile(path, []byte(body), 0o644)
}

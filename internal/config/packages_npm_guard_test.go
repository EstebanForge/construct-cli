package config

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

var defaultExecCommand = exec.Command

func TestGenerateInstallScriptSkipsInstalledNpm(t *testing.T) {
	cfg := &PackagesConfig{
		Npm: NpmConfig{Packages: []string{"@openai/codex@latest", "typescript"}},
	}
	script := cfg.GenerateInstallScript()

	// Each declared package installs only when its node_modules dir is
	// absent: forced reinstall passes must skip the network (install-phase
	// budget), and a refresh is the documented rm + rerun.
	for _, tc := range []struct{ pkg, dir string }{
		{"@openai/codex@latest", "@openai/codex"},
		{"typescript", "typescript"},
	} {
		guard := fmt.Sprintf(`if [ -d "$HOME/.npm-global/lib/node_modules/%s" ]; then`, tc.dir)
		if !strings.Contains(script, guard) {
			t.Errorf("missing existence guard for %s: %s", tc.pkg, guard)
		}
		if !strings.Contains(script, "npm install -g "+tc.pkg) {
			t.Errorf("missing install command for %s", tc.pkg)
		}
	}
	if strings.Contains(script, "] ]") {
		t.Error("script contains invalid double closing brackets '] ]'")
	}
	// The version tag must not leak into the checked directory name.
	if strings.Contains(script, "node_modules/@openai/codex@latest") {
		t.Error("guard checks the tagged package name; the tag must be stripped")
	}
}

// TestGenerateInstallScriptComparesPinnedVersions: an explicit version pin
// that differs from the installed one must reinstall — a bare dir check
// would pin users to whatever version first landed.
func TestGenerateInstallScriptComparesPinnedVersions(t *testing.T) {
	cfg := &PackagesConfig{Npm: NpmConfig{Packages: []string{"@openai/codex@2.0.0"}}}
	script := cfg.GenerateInstallScript()

	if !strings.Contains(script, `= "2.0.0"`) {
		t.Errorf("missing version comparison for the pinned install: %s", script)
	}
	if !strings.Contains(script, `package.json').version`) {
		t.Errorf("missing installed-version probe: %s", script)
	}
	if !strings.Contains(script, "npm install -g @openai/codex@2.0.0") {
		t.Errorf("missing install command for the pinned package")
	}
}

// TestGenerateInstallScriptBashSyntaxValid runs bash -n on generated install scripts
// to ensure syntax correctness for both unpinned and pinned packages.
func TestGenerateInstallScriptBashSyntaxValid(t *testing.T) {
	for name, cfg := range map[string]*PackagesConfig{
		"default_empty": {},
		"unpinned": {
			Npm: NpmConfig{Packages: []string{"typescript", "@github/copilot"}},
		},
		"pinned": {
			Npm: NpmConfig{Packages: []string{"@openai/codex@2.0.0", "typescript@5.4.2"}},
		},
		"mixed": {
			Npm: NpmConfig{Packages: []string{"typescript", "@openai/codex@2.0.0", "sass"}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			script := cfg.GenerateInstallScript()
			cmd := strings.NewReader(script)
			bash := execCommand("bash", "-n")
			bash.Stdin = cmd
			out, err := bash.CombinedOutput()
			if err != nil {
				t.Fatalf("bash -n failed on generated script (%v): %s\nScript:\n%s", err, string(out), script)
			}
		})
	}
}

var execCommand = defaultExecCommand

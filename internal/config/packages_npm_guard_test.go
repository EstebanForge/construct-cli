package config

import (
	"fmt"
	"strings"
	"testing"
)

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
		guard := fmt.Sprintf(`[ -d "$HOME/.npm-global/lib/node_modules/%s" ]`, tc.dir)
		if !strings.Contains(script, guard) {
			t.Errorf("missing existence guard for %s: %s", tc.pkg, guard)
		}
		if !strings.Contains(script, "npm install -g "+tc.pkg) {
			t.Errorf("missing install command for %s", tc.pkg)
		}
	}
	// The version tag must not leak into the checked directory name.
	if strings.Contains(script, "node_modules/@openai/codex@latest") {
		t.Error("guard checks the tagged package name; the tag must be stripped")
	}
}

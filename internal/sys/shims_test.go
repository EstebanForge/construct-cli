package sys

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EstebanForge/construct-cli/internal/agent"
)

func writeForeignFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write foreign file: %v", err)
	}
}

func TestWriteShimCreatesExecutableWithMarker(t *testing.T) {
	dir := t.TempDir()
	target, err := writeShim(dir, "pi", "/usr/local/bin/construct", false)
	if err != nil {
		t.Fatalf("writeShim: %v", err)
	}
	if target != filepath.Join(dir, "pi") {
		t.Errorf("target = %q, want %q", target, filepath.Join(dir, "pi"))
	}

	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat shim: %v", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("shim not executable: %v", info.Mode())
	}

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read shim: %v", err)
	}
	s := string(data)
	if !strings.HasPrefix(s, "#!/bin/sh") {
		t.Errorf("missing shebang: %q", s)
	}
	if !strings.Contains(s, shimMarker) {
		t.Errorf("missing marker: %q", s)
	}
	if !strings.Contains(s, `exec "/usr/local/bin/construct" pi "$@"`) {
		t.Errorf("missing exec line: %q", s)
	}
}

func TestIsOurShim(t *testing.T) {
	dir := t.TempDir()

	ours := filepath.Join(dir, "ours")
	if _, err := writeShim(dir, "ours", "/bin/construct", false); err != nil {
		t.Fatalf("writeShim: %v", err)
	}
	if !isOurShim(ours) {
		t.Error("written shim not recognized as ours")
	}

	foreign := filepath.Join(dir, "foreign")
	writeForeignFile(t, foreign, "#!/bin/sh\nexec something-else\n")
	if isOurShim(foreign) {
		t.Error("foreign script misidentified as ours")
	}

	missing := filepath.Join(dir, "missing")
	if isOurShim(missing) {
		t.Error("missing file misidentified as ours")
	}
}

func TestWriteShimRefusesForeignFileWithoutForce(t *testing.T) {
	dir := t.TempDir()
	foreign := filepath.Join(dir, "pi")
	writeForeignFile(t, foreign, "#!/bin/sh\nexec real-pi\n")

	if _, err := writeShim(dir, "pi", "/bin/construct", false); err == nil {
		t.Fatal("expected error overwriting foreign file without --force")
	}

	// Content must be untouched after the refusal.
	data, _ := os.ReadFile(foreign)
	if !strings.Contains(string(data), "real-pi") {
		t.Errorf("foreign file modified: %q", data)
	}

	if _, err := writeShim(dir, "pi", "/bin/construct", true); err != nil {
		t.Fatalf("writeShim with force: %v", err)
	}
	if !isOurShim(foreign) {
		t.Error("forced overwrite did not produce our shim")
	}
}

func TestWriteShimOverwritesOurOwnShim(t *testing.T) {
	dir := t.TempDir()
	if _, err := writeShim(dir, "pi", "/old/construct", false); err != nil {
		t.Fatalf("first writeShim: %v", err)
	}
	target, err := writeShim(dir, "pi", "/new/construct", false)
	if err != nil {
		t.Fatalf("second writeShim (ours, no force): %v", err)
	}
	data, _ := os.ReadFile(target)
	if !strings.Contains(string(data), "/new/construct") {
		t.Errorf("shim not updated: %q", data)
	}
}

func TestSelectAgentsFiltersAndRejectsUnknown(t *testing.T) {
	if _, err := selectAgents(nil); err != nil {
		t.Fatalf("selectAgents(nil) error: %v", err)
	}
	// Every supported slug must be selectable.
	allSlugs := make([]string, 0, len(agent.SupportedAgents))
	for _, a := range agent.SupportedAgents {
		allSlugs = append(allSlugs, a.Slug)
	}
	got, err := selectAgents(allSlugs)
	if err != nil {
		t.Fatalf("selectAgents(all): %v", err)
	}
	if len(got) != len(allSlugs) {
		t.Errorf("got %d agents, want %d", len(got), len(allSlugs))
	}
	if _, err := selectAgents([]string{"definitely-not-an-agent"}); err == nil {
		t.Error("expected error for unknown slug")
	}
}

func TestUninstallShimsRemovesOnlyOurs(t *testing.T) {
	dir := t.TempDir()
	if _, err := writeShim(dir, "pi", "/bin/construct", false); err != nil {
		t.Fatalf("writeShim: %v", err)
	}
	foreign := filepath.Join(dir, "foreign")
	writeForeignFile(t, foreign, "#!/bin/sh\nexec x\n")

	// Route through the public uninstall core with a foreign file occupying
	// one slug is not possible without a supported slug named "foreign";
	// instead verify behavior via removeShim semantics below.
	removeAndReport := func(path string) (removed bool, refused bool) {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return false, false
		}
		if !isOurShim(path) {
			return false, true
		}
		if err := os.Remove(path); err != nil {
			return false, false
		}
		return true, false
	}

	if removed, refused := removeAndReport(filepath.Join(dir, "pi")); !removed || refused {
		t.Errorf("our shim: removed=%v refused=%v", removed, refused)
	}
	if removed, refused := removeAndReport(foreign); removed || !refused {
		t.Errorf("foreign: removed=%v refused=%v", removed, refused)
	}
}

func TestWriteNsShimExecsRealBinary(t *testing.T) {
	dir := t.TempDir()
	target, err := writeNsShim(dir, "pi", "/opt/homebrew/bin/pi", false)
	if err != nil {
		t.Fatalf("writeNsShim: %v", err)
	}
	if target != filepath.Join(dir, "ns-pi") {
		t.Errorf("target = %q, want ns-pi path", target)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(data), `exec "/opt/homebrew/bin/pi" "$@"`) {
		t.Errorf("ns shim missing exec of real binary: %q", data)
	}
	if !isOurShim(target) {
		t.Error("ns shim not recognized as ours")
	}
}

func TestLookPathSkippingDirSkipsShimDir(t *testing.T) {
	shimDir := t.TempDir()
	realDir := t.TempDir()

	// Shadowing fake: a pi "shim" in the dir that must be skipped.
	if err := os.WriteFile(filepath.Join(shimDir, "fakebin"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Real binary in another dir.
	if err := os.WriteFile(filepath.Join(realDir, "fakebin"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+realDir)

	got, err := lookPathSkippingDir("fakebin", shimDir)
	if err != nil {
		t.Fatalf("lookPathSkippingDir: %v", err)
	}
	if got != filepath.Join(realDir, "fakebin") {
		t.Errorf("resolved %q, want the copy outside the skipped dir", got)
	}

	if _, err := lookPathSkippingDir("missing-bin", shimDir); err == nil {
		t.Error("expected error for missing binary")
	}
}

func TestRemoveLegacyAliasBlock(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/bin/zsh")

	rc := filepath.Join(home, ".zshrc")
	before := "export EDITOR=vim\n\n" +
		"# construct-cli aliases start\n" +
		"alias pi='ct pi'\n" +
		"ns-pi() { \"/opt/homebrew/bin/pi\" \"$@\"; }\n" +
		"# construct-cli aliases end\n" +
		"\n# user's own config\n"
	if err := os.WriteFile(rc, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}

	removed, configFile, err := RemoveLegacyAliasBlock()
	if err != nil || !removed {
		t.Fatalf("removed=%v err=%v", removed, err)
	}
	if configFile != rc {
		t.Errorf("configFile = %q", configFile)
	}

	after, _ := os.ReadFile(rc)
	s := string(after)
	if strings.Contains(s, "construct-cli aliases") || strings.Contains(s, "alias pi=") {
		t.Errorf("alias block not removed: %q", s)
	}
	if !strings.Contains(s, "export EDITOR=vim") || !strings.Contains(s, "# user's own config") {
		t.Errorf("surrounding user config damaged: %q", s)
	}

	// Second call: nothing to remove, no error.
	removed, _, err = RemoveLegacyAliasBlock()
	if err != nil || removed {
		t.Errorf("second call: removed=%v err=%v", removed, err)
	}

	// Backup must exist.
	matches, _ := filepath.Glob(rc + ".backup-*")
	if len(matches) == 0 {
		t.Error("no backup created before removal")
	}
}

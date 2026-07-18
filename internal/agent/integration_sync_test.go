package agent

import (
	"os"
	"path/filepath"
	"testing"
)

// writeTempFile creates a file under t.TempDir() and returns its path.
func writeTempFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	full := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", full, err)
	}
	return full
}

func TestSyncIntegrationFile_CopiesWhenMissing(t *testing.T) {
	src := writeTempFile(t, t.TempDir(), "src/herdr-agent-state.ts", "v1 payload")
	dstDir := t.TempDir()
	dst := filepath.Join(dstDir, "agent", "extensions", "herdr-agent-state.ts")

	syncIntegrationFile(src, dst)

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("expected dst written, got %v", err)
	}
	if string(got) != "v1 payload" {
		t.Fatalf("content mismatch: got %q", got)
	}
}

func TestSyncIntegrationFile_OverwritesWhenDifferent(t *testing.T) {
	src := writeTempFile(t, t.TempDir(), "src/herdr-agent-state.ts", "updated payload")
	dstDir := t.TempDir()
	dst := writeTempFile(t, dstDir, "agent/extensions/herdr-agent-state.ts", "stale payload")

	syncIntegrationFile(src, dst)

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if string(got) != "updated payload" {
		t.Fatalf("expected update to propagate, got %q", got)
	}
}

func TestSyncIntegrationFile_SkipsWhenIdentical(t *testing.T) {
	const payload = "same payload"
	srcDir := t.TempDir()
	src := writeTempFile(t, srcDir, "src/herdr-agent-state.ts", payload)
	dstDir := t.TempDir()
	dst := writeTempFile(t, dstDir, "agent/extensions/herdr-agent-state.ts", payload)

	// Capture mtime before; an identical skip must not rewrite (mtime preserved).
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("stat dst: %v", err)
	}
	before := info.ModTime()

	syncIntegrationFile(src, dst)

	info, err = os.Stat(dst)
	if err != nil {
		t.Fatalf("stat dst after: %v", err)
	}
	if !info.ModTime().Equal(before) {
		t.Fatalf("identical file was rewritten (mtime changed): %v -> %v", before, info.ModTime())
	}
}

func TestSyncIntegrationFile_NoopWhenSourceMissing(t *testing.T) {
	dstDir := t.TempDir()
	dst := filepath.Join(dstDir, "agent", "extensions", "herdr-agent-state.ts")

	// Source does not exist; must not error or create dst.
	syncIntegrationFile(filepath.Join(t.TempDir(), "nope.ts"), dst)

	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Fatalf("expected dst absent, got %v", err)
	}
}

func TestSyncAgentIntegrations_PiOnlyKnownSlug(t *testing.T) {
	// Point host pi agent dir at a temp source carrying the integration file.
	srcAgentDir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", srcAgentDir)
	writeTempFile(t, srcAgentDir, "extensions/herdr-agent-state.ts", "from host")

	configPath := t.TempDir()
	syncAgentIntegrations([]string{"pi"}, configPath)

	dst := filepath.Join(configPath, "home", ".pi", "agent", "extensions", "herdr-agent-state.ts")
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("expected sync into %s, got %v", dst, err)
	}
	if string(got) != "from host" {
		t.Fatalf("content mismatch: %q", got)
	}
}

func TestSyncAgentIntegrations_SkipsUnknownSlug(t *testing.T) {
	srcAgentDir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", srcAgentDir)
	configPath := t.TempDir()

	// claude is not in the registry yet; nothing should be written.
	syncAgentIntegrations([]string{"claude"}, configPath)

	dst := filepath.Join(configPath, "home", ".claude")
	if entries, _ := os.ReadDir(dst); len(entries) != 0 {
		t.Fatalf("expected no writes for unregistered slug, found %d entries", len(entries))
	}
}

func TestSyncAgentIntegrations_NoopWithoutArgs(t *testing.T) {
	// No args: must not panic and must not write anything.
	configPath := t.TempDir()
	syncAgentIntegrations(nil, configPath)
	syncAgentIntegrations([]string{}, configPath)
}

func TestSyncAgentIntegrations_PiUsesHomeFallback(t *testing.T) {
	// PI_CODING_AGENT_DIR unset: hostPiDir falls back to ~/.pi/agent. Override
	// HOME so the fallback target is deterministic and isolated.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PI_CODING_AGENT_DIR", "")
	writeTempFile(t, home, ".pi/agent/extensions/herdr-agent-state.ts", "fallback payload")

	configPath := t.TempDir()
	syncAgentIntegrations([]string{"pi"}, configPath)

	dst := filepath.Join(configPath, "home", ".pi", "agent", "extensions", "herdr-agent-state.ts")
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("expected home-fallback sync, got %v", err)
	}
	if string(got) != "fallback payload" {
		t.Fatalf("content mismatch: %q", got)
	}
}

func TestHostPiDir_EnvWinsOverHome(t *testing.T) {
	t.Setenv("PI_CODING_AGENT_DIR", "/custom/pi/dir")
	t.Setenv("HOME", "/should/not/be/used")
	if got := hostPiDir(); got != "/custom/pi/dir" {
		t.Fatalf("expected env path, got %q", got)
	}
}

func TestHostPiDir_TildeExpansion(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PI_CODING_AGENT_DIR", "~/custompi")
	want := filepath.Join(home, "custompi")
	if got := hostPiDir(); got != want {
		t.Fatalf("tilde expand: got %q want %q", got, want)
	}
}

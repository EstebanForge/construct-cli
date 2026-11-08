package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newTestStager builds a stager whose staging root and temp allowlist live
// under t.TempDir(), with an in-memory copy recorder.
func newTestStager(t *testing.T, slug string) *argStager {
	t.Helper()
	tmp := t.TempDir()
	s := newArgStager(slug, filepath.Join(tmp, "config"), filepath.Join(tmp, "cwd"))
	if s == nil {
		t.Fatalf("nil stager for %q", slug)
	}
	s.allowedRoots = []string{filepath.Join(tmp, "temp"), filepath.Join(tmp, "pihome"), s.callerCwd}
	for _, d := range append([]string{s.callerCwd}, s.allowedRoots...) {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir helper dir: %v", err)
		}
	}
	copied := map[string]string{}
	s.copyFn = func(src, dst string) error {
		copied[src] = dst
		return copyFileBestEffort(src, dst)
	}
	t.Cleanup(func() { s.cleanup() })
	return s
}

func writeStageFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAdaptArgsStagesExtensionAndMcpConfig(t *testing.T) {
	s := newTestStager(t, "pi")
	ext := writeStageFile(t, s.allowedRoots[0], "bridge.mjs", "// bridge")
	mcp := writeStageFile(t, s.allowedRoots[0], "mcp.json", "{}")

	args := []string{"pi", "--mode", "rpc", "--extension", ext, "--mcp-config", mcp}
	s.adaptArgs(args)

	if args[3] != "--extension" || !strings.HasPrefix(args[4], stagingContainerDir+"/") {
		t.Errorf("extension not staged: %v", args[3:5])
	}
	if strings.HasSuffix(args[4], ".mjs") == false {
		t.Errorf("extension name lost its suffix: %q", args[4])
	}
	if !strings.HasPrefix(args[6], stagingContainerDir+"/") {
		t.Errorf("mcp-config not staged: %v", args[5:7])
	}
	// Host files exist under the staging root on disk.
	if _, err := os.Stat(s.runDir); err != nil {
		t.Fatalf("staging dir missing: %v", err)
	}
}

func TestAdaptArgsGluedAndRepeatedFlags(t *testing.T) {
	s := newTestStager(t, "pi")
	a := writeStageFile(t, s.allowedRoots[0], "a.mjs", "a")
	b := writeStageFile(t, s.allowedRoots[0], "b.mjs", "b")

	args := []string{"pi", "--extension=" + a, "--extension", b}
	s.adaptArgs(args)

	if !strings.HasPrefix(args[1], "--extension="+stagingContainerDir+"/") {
		t.Errorf("glued form not staged: %q", args[1])
	}
	if args[2] != "--extension" || !strings.HasPrefix(args[3], stagingContainerDir+"/") {
		t.Errorf("repeated flag not staged: %v", args[2:4])
	}
}

func TestAdaptArgsRegistersSessionCopyBack(t *testing.T) {
	s := newTestStager(t, "pi")
	sess := writeStageFile(t, s.allowedRoots[1], "s.jsonl", "{}\n")

	args := []string{"pi", "--session", sess}
	s.adaptArgs(args)

	if len(s.copyBacks) != 1 {
		t.Fatalf("copy-backs = %d, want 1", len(s.copyBacks))
	}
	cb := s.copyBacks[0]
	if cb.host != sess {
		t.Errorf("copy-back host = %q, want %q", cb.host, sess)
	}
	if cb.staged != filepath.Join(s.runDir, "s.jsonl") {
		t.Errorf("copy-back staged = %q", cb.staged)
	}

	// syncBack writes sandbox progress back to the host path.
	if err := os.WriteFile(cb.staged, []byte("{}\n{more}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.syncBack()
	data, _ := os.ReadFile(sess)
	if string(data) != "{}\n{more}\n" {
		t.Errorf("host session not updated: %q", data)
	}
}

func TestAdaptArgsKeepsUnknownAndOutsideAllowlist(t *testing.T) {
	s := newTestStager(t, "pi")
	outside := writeStageFile(t, t.TempDir(), "secret.conf", "x")

	args := []string{"pi", "--model", "zai/glm-5.3", "--extension", "/no/such/file.mjs",
		"--extension", outside, "--", "--extension", "later.mjs"}
	s.adaptArgs(args)

	if args[4] != "/no/such/file.mjs" {
		t.Errorf("non-existent value rewritten: %q", args[4])
	}
	if args[6] != outside {
		t.Errorf("outside-allowlist value rewritten: %q", args[6])
	}
	if args[9] != "later.mjs" {
		t.Errorf("value after -- rewritten: %q", args[9])
	}
	if s.runDir != "" {
		t.Errorf("staging dir created unnecessarily")
	}
}

func TestAdaptArgsRelativeAndTildePaths(t *testing.T) {
	s := newTestStager(t, "pi")
	rel := writeStageFile(t, s.callerCwd, "rel.mjs", "r")

	args := []string{"pi", "--extension", filepath.Base(rel)}
	s.adaptArgs(args)
	if !strings.HasPrefix(args[2], stagingContainerDir+"/") {
		t.Errorf("relative value not staged: %q", args[2])
	}

	// Tilde form under the pi home allowlist root: simulate by pointing the
	// allowlist at the real home only for this check.
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	s2 := newTestStager(t, "pi")
	s2.allowedRoots = []string{home}
	tilde := writeStageFile(t, home, ".construct-staging-test-tilde.mjs", "t")
	defer func() { _ = os.Remove(tilde) }()
	args2 := []string{"pi", "--extension", "~/.construct-staging-test-tilde.mjs"}
	s2.adaptArgs(args2)
	if !strings.HasPrefix(args2[2], stagingContainerDir+"/") {
		t.Errorf("tilde value not staged: %q", args2[2])
	}
}

func TestAdaptArgsPigStagesSessionFromPigHome(t *testing.T) {
	// Regression: pig must resolve its own config root into the staging
	// allowlist, exactly as pi does, or orchestrator session paths under
	// ~/.pig are silently left unrewritten and break inside the container.
	root := hostAgentConfigRoot("pig")
	if root == "" {
		t.Fatal("pig config root unresolved; staging allowlist would miss ~/.pig paths")
	}
	// The config root may not exist yet (XDG resolution on a clean CI
	// runner), and writeStageFile does not create parents.
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	s := newArgStager("pig", filepath.Join(tmp, "config"), filepath.Join(tmp, "cwd"))
	if s == nil {
		t.Fatal("nil stager for pig")
	}
	s.copyFn = func(_, _ string) error { return nil }
	t.Cleanup(func() { s.cleanup() })

	sess := writeStageFile(t, root, "construct-test-session.jsonl", "{}\n")
	defer func() { _ = os.Remove(sess) }()
	args := []string{"pig", "--session", sess}
	s.adaptArgs(args)
	if !strings.HasPrefix(args[2], stagingContainerDir+"/") {
		t.Errorf("pig session under %s not staged: %q", root, args[2])
	}
	if len(s.copyBacks) != 1 {
		t.Errorf("expected 1 copy-back for --session, got %d", len(s.copyBacks))
	}
}

func TestAdaptArgsSkipsOtherAgentsAndSizeCap(t *testing.T) {
	if newArgStager("claude", "/x", "/y") != nil {
		t.Error("stager created for agent without path flags")
	}

	s := newTestStager(t, "pi")
	big := filepath.Join(s.allowedRoots[0], "big.mjs")
	if err := os.WriteFile(big, make([]byte, maxStageFileBytes+1), 0o644); err != nil {
		t.Fatal(err)
	}
	args := []string{"pi", "--extension", big}
	s.adaptArgs(args)
	if args[2] != big {
		t.Errorf("oversized file staged: %q", args[2])
	}
}

func TestAdaptArgsSessionNeverSanitizedAndConfigFailsClosed(t *testing.T) {
	// 1. Session file containing token must NOT be sanitized, so host copy-back never corrupts host tokens.
	s1 := newTestStager(t, "pi")
	sessContent := `{"messages":[],"token":"host-real-secret-token"}`
	sess := writeStageFile(t, s1.allowedRoots[1], "session.json", sessContent)

	args1 := []string{"pi", "--session", sess}
	s1.adaptArgs(args1)

	stagedSess := filepath.Join(s1.runDir, "session.json")
	data, err := os.ReadFile(stagedSess)
	if err != nil {
		t.Fatalf("failed to read staged session: %v", err)
	}
	if string(data) != sessContent {
		t.Errorf("session was modified by sanitizer: got %q, want %q", string(data), sessContent)
	}

	// 2. Config file (--mcp-config) containing token MUST be sanitized.
	s2 := newTestStager(t, "pi")
	mcpContent := `{"env":{"API_KEY":"sk-ant-api03-real-secret"}}`
	mcp := writeStageFile(t, s2.allowedRoots[0], "mcp.json", mcpContent)

	args2 := []string{"pi", "--mcp-config", mcp}
	s2.adaptArgs(args2)

	stagedMcp := filepath.Join(s2.runDir, "mcp.json")
	mcpData, err := os.ReadFile(stagedMcp)
	if err != nil {
		t.Fatalf("failed to read staged mcp: %v", err)
	}
	if strings.Contains(string(mcpData), "sk-ant-api03-real-secret") {
		t.Errorf("mcp-config was not sanitized: %s", string(mcpData))
	}

	// 3. Malformed JSON config must fail closed (staged file deleted, flag not modified).
	s3 := newTestStager(t, "pi")
	badJSON := writeStageFile(t, s3.allowedRoots[0], "bad.json", `{"token":"abc",}`)
	args3 := []string{"pi", "--mcp-config", badJSON}
	s3.adaptArgs(args3)

	if args3[2] != badJSON {
		t.Errorf("malformed json was staged: %q", args3[2])
	}
	stagedBad := filepath.Join(s3.runDir, "bad.json")
	if _, err := os.Stat(stagedBad); !os.IsNotExist(err) {
		t.Errorf("expected staged malformed file to be deleted, got err: %v", err)
	}
}

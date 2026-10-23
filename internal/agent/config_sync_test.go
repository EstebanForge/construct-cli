package agent

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// syncFixture wires a temporary host home and a temporary construct home into
// the sync core the way the CLI would, without touching os.UserHomeDir.
type syncFixture struct {
	t         *testing.T
	hostHome  string
	guestHome string
}

func newSyncFixture(t *testing.T) *syncFixture {
	t.Helper()
	return &syncFixture{
		t:         t,
		hostHome:  t.TempDir(),
		guestHome: t.TempDir(),
	}
}

func (f *syncFixture) hostFile(rel, content string) string {
	f.t.Helper()
	p := filepath.Join(f.hostHome, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
	return p
}

func (f *syncFixture) hostDir(rel string) string {
	f.t.Helper()
	p := filepath.Join(f.hostHome, rel)
	if err := os.MkdirAll(p, 0o755); err != nil {
		f.t.Fatal(err)
	}
	return p
}

func (f *syncFixture) guestPath(rel string) string {
	return filepath.Join(f.guestHome, rel)
}

func (f *syncFixture) readGuest(rel string) ([]byte, error) {
	return os.ReadFile(f.guestPath(rel))
}

func TestConfigSyncTableCoversSupportedAgents(t *testing.T) {
	seen := make(map[string]bool, len(configSyncTable))
	for _, row := range configSyncTable {
		if seen[row.slug] {
			t.Fatalf("duplicate sync table row for %q", row.slug)
		}
		seen[row.slug] = true
		if !IsSupported(row.slug) {
			t.Fatalf("sync table has unknown agent slug %q", row.slug)
		}
		if len(row.entries) == 0 {
			t.Fatalf("agent %q has an empty sync entry list", row.slug)
		}
		for _, e := range row.entries {
			if e.guestRel == "" || e.hostPath == nil {
				t.Fatalf("agent %q has an incomplete entry: %+v", row.slug, e)
			}
			if e.role != roleConfig && e.role != roleCredential {
				t.Fatalf("agent %q entry %q has invalid role %d", row.slug, e.guestRel, e.role)
			}
		}
	}
	for _, a := range SupportedAgents {
		if !seen[a.Slug] {
			t.Fatalf("supported agent %q has no sync table row", a.Slug)
		}
	}
}

func TestSyncConfigPushesHostToGuestAndRepushesOnChange(t *testing.T) {
	f := newSyncFixture(t)
	f.hostFile(".codex/config.toml", "model = o3\n")

	entry := configSyncEntry{
		hostPath: func(home string) string { return filepath.Join(home, ".codex", "config.toml") },
		guestRel: ".codex/config.toml",
		role:     roleConfig,
	}

	res := syncOneEntry(f.hostHome, f.guestHome, entry, false, false)
	if res.pushed != 1 || res.err != nil {
		t.Fatalf("first push: got %+v, want 1 pushed, nil err", res)
	}
	got, err := f.readGuest(".codex/config.toml")
	if err != nil || string(got) != "model = o3\n" {
		t.Fatalf("guest content: %q, %v", got, err)
	}
	fi, err := os.Stat(f.guestPath(".codex/config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o644 {
		t.Fatalf("config perm = %v, want 0644", fi.Mode().Perm())
	}

	// Second run: no host change, no push.
	res = syncOneEntry(f.hostHome, f.guestHome, entry, false, false)
	if res.pushed != 0 || res.err != nil {
		t.Fatalf("idempotent run: got %+v, want 0 pushed", res)
	}

	// Host edit: pushes again.
	f.hostFile(".codex/config.toml", "model = o5\n")
	res = syncOneEntry(f.hostHome, f.guestHome, entry, false, false)
	if res.pushed != 1 || res.err != nil {
		t.Fatalf("repush after host change: got %+v", res)
	}
	got, _ = f.readGuest(".codex/config.toml")
	if string(got) != "model = o5\n" {
		t.Fatalf("guest content after repush: %q", got)
	}
}

func TestSyncCredentialStubIsMissingRealGuestKept(t *testing.T) {
	f := newSyncFixture(t)
	f.hostFile(".codex/auth.json", `{"tokens":{"access":"host-token"}}`)

	entry := configSyncEntry{
		hostPath: func(home string) string { return filepath.Join(home, ".codex", "auth.json") },
		guestRel: ".codex/auth.json",
		role:     roleCredential,
	}

	// Missing guest credential: pushed without --force.
	res := syncOneEntry(f.hostHome, f.guestHome, entry, false, false)
	if res.pushed != 1 || res.err != nil {
		t.Fatalf("push to missing guest: %+v", res)
	}
	fi, err := os.Stat(f.guestPath(".codex/auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("credential perm = %v, want 0600", fi.Mode().Perm())
	}

	// Entry point's stub rule: an empty object counts as missing too.
	f.hostFile(".codex/auth.json", `{"tokens":{"access":"real"}}`)
	if err := os.MkdirAll(filepath.Dir(f.guestPath(".codex/auth.json")), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.guestPath(".codex/auth.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	res = syncOneEntry(f.hostHome, f.guestHome, entry, false, false)
	if res.pushed != 1 || res.err != nil {
		t.Fatalf("stub {} should be treated as missing: %+v", res)
	}

	// Real guest credential: kept without --force, pushed with --force.
	f.hostFile(".codex/auth.json", `{"tokens":{"access":"stale-host"}}`)
	res = syncOneEntry(f.hostHome, f.guestHome, entry, false, false)
	if res.pushed != 0 || res.skipped != 1 || res.err != nil {
		t.Fatalf("real guest credential must be kept: %+v", res)
	}
	got, _ := f.readGuest(".codex/auth.json")
	if !strings.Contains(string(got), "real") {
		t.Fatalf("guest credential was clobbered: %q", got)
	}
	res = syncOneEntry(f.hostHome, f.guestHome, entry, true, false)
	if res.pushed != 1 || res.err != nil {
		t.Fatalf("--force must overwrite credential: %+v", res)
	}
	got, _ = f.readGuest(".codex/auth.json")
	if !strings.Contains(string(got), "stale-host") {
		t.Fatalf("--force did not push host credential: %q", got)
	}
}

func TestSyncRefusesGuestPlantedSymlink(t *testing.T) {
	f := newSyncFixture(t)
	f.hostFile(".claude/settings.json", "{}\n")

	outside := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(outside, []byte("victim"), 0o644); err != nil {
		t.Fatal(err)
	}
	guestDir := filepath.Dir(f.guestPath(".claude/settings.json"))
	if err := os.MkdirAll(guestDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, f.guestPath(".claude/settings.json")); err != nil {
		t.Fatal(err)
	}

	entry := configSyncEntry{
		hostPath: func(home string) string { return filepath.Join(home, ".claude", "settings.json") },
		guestRel: ".claude/settings.json",
		role:     roleConfig,
	}
	res := syncOneEntry(f.hostHome, f.guestHome, entry, true, false)
	if res.pushed != 0 {
		t.Fatalf("symlinked destination must not be written: %+v", res)
	}
	if res.err == nil {
		t.Fatalf("planted symlink must surface an error or skip reason")
	}
	victim, err := os.ReadFile(outside)
	if err != nil || string(victim) != "victim" {
		t.Fatalf("symlink target was modified: %q, %v", victim, err)
	}
}

func TestSyncSkipsMissingHostDirAndEmptyAgent(t *testing.T) {
	f := newSyncFixture(t)
	entry := configSyncEntry{
		hostPath: func(home string) string { return filepath.Join(home, ".missing-agent", "config.toml") },
		guestRel: ".missing-agent/config.toml",
		role:     roleConfig,
	}
	res := syncOneEntry(f.hostHome, f.guestHome, entry, false, false)
	if res.pushed != 0 || res.err != nil {
		t.Fatalf("missing host source must skip silently: %+v", res)
	}
}

func TestSyncDryRunWritesNothing(t *testing.T) {
	f := newSyncFixture(t)
	f.hostFile(".codex/config.toml", "model = o3\n")
	f.hostFile(".codex/auth.json", `{"access":"t"}`)

	entries := []configSyncEntry{
		{
			hostPath: func(home string) string { return filepath.Join(home, ".codex", "config.toml") },
			guestRel: ".codex/config.toml",
			role:     roleConfig,
		},
		{
			hostPath: func(home string) string { return filepath.Join(home, ".codex", "auth.json") },
			guestRel: ".codex/auth.json",
			role:     roleCredential,
		},
	}
	for _, e := range entries {
		res := syncOneEntry(f.hostHome, f.guestHome, e, false, true)
		if res.pushed != 1 || res.err != nil {
			t.Fatalf("dry run should count planned pushes: %+v", res)
		}
	}
	if _, err := os.Stat(f.guestPath(".codex/config.toml")); !os.IsNotExist(err) {
		t.Fatalf("dry run wrote a config file: %v", err)
	}
	if _, err := os.Stat(f.guestPath(".codex/auth.json")); !os.IsNotExist(err) {
		t.Fatalf("dry run wrote a credential file: %v", err)
	}
}

func TestSyncDirEntryPushesFilesSkipsSymlinksAndOversize(t *testing.T) {
	f := newSyncFixture(t)
	f.hostFile(".claude/commands/one.md", "one")
	f.hostFile(".claude/commands/nested/two.md", "two")

	// Symlinked file inside the host dir: followed (resolves to one.md content).
	if err := os.Symlink(f.hostFile(".claude/commands/one.md", "one"), filepath.Join(f.hostDir(".claude/commands"), "link.md")); err != nil {
		t.Fatal(err)
	}
	// Oversized file: must be skipped.
	big := filepath.Join(f.hostDir(".claude/commands"), "big.md")
	if err := os.WriteFile(big, make([]byte, maxSyncFileBytes+1), 0o644); err != nil {
		t.Fatal(err)
	}

	entry := configSyncEntry{
		hostPath: func(home string) string { return filepath.Join(home, ".claude", "commands") },
		guestRel: ".claude/commands",
		role:     roleConfig,
		dir:      true,
	}
	res := syncOneEntry(f.hostHome, f.guestHome, entry, false, false)
	if res.pushed != 3 || res.skipped != 1 || res.err == nil {
		t.Fatalf("dir push: %+v, want 3 pushed, 1 skipped (oversize), err set", res)
	}
	got, err := f.readGuest(".claude/commands/link.md")
	if err != nil || string(got) != "one" {
		t.Fatalf("symlinked host file content: %q, %v", got, err)
	}
	got, err = f.readGuest(".claude/commands/nested/two.md")
	if err != nil || string(got) != "two" {
		t.Fatalf("nested dir push failed: %q, %v", got, err)
	}
}

func TestSyncAgentsFilterSelectsRows(t *testing.T) {
	rows, err := selectSyncRows([]string{"codex"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].slug != "codex" {
		t.Fatalf("selectSyncRows(codex) = %+v", rows)
	}
	if _, err := selectSyncRows([]string{"not-an-agent"}); err == nil {
		t.Fatal("unknown slug must error")
	}
	all, err := selectSyncRows(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != len(configSyncTable) {
		t.Fatalf("nil filter must return all rows: got %d want %d", len(all), len(configSyncTable))
	}
	// Duplicates must not produce redundant sync passes.
	rows, err = selectSyncRows([]string{"pi", "pi"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("duplicate slugs must dedupe: got %d rows", len(rows))
	}
}

func TestParseSyncArgs(t *testing.T) {
	tt := []struct {
		name    string
		args    []string
		want    syncOptions
		wantErr bool
	}{
		{name: "empty", args: nil, want: syncOptions{}},
		{name: "force", args: []string{"--force"}, want: syncOptions{force: true}},
		{name: "dry run", args: []string{"--dry-run"}, want: syncOptions{dryRun: true}},
		{name: "yes", args: []string{"--yes"}, want: syncOptions{yes: true}},
		{name: "yes short", args: []string{"-y"}, want: syncOptions{yes: true}},
		{name: "agents list", args: []string{"--agents", "pi,claude"}, want: syncOptions{agents: []string{"pi", "claude"}}},
		{name: "agents equals", args: []string{"--agents=pi,claude"}, want: syncOptions{agents: []string{"pi", "claude"}}},
		{name: "agents short", args: []string{"-a", "pi"}, want: syncOptions{agents: []string{"pi"}}},
		// Trailing commas and stray spaces must not become phantom slugs.
		{name: "agents messy", args: []string{"--agents", " pi , claude, ,"}, want: syncOptions{agents: []string{"pi", "claude"}}},
		{name: "help", args: []string{"--help"}, want: syncOptions{help: true}},
		{name: "short help", args: []string{"-h"}, want: syncOptions{help: true}},
		{name: "unknown flag", args: []string{"--wat"}, wantErr: true},
		{name: "missing value", args: []string{"--agents"}, wantErr: true},
		{name: "positional junk", args: []string{"pi"}, wantErr: true},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseSyncArgs(tc.args)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseSyncArgs(%v) = %+v, want error", tc.args, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseSyncArgs(%v): %v", tc.args, err)
			}
			if got.force != tc.want.force || got.dryRun != tc.want.dryRun || got.help != tc.want.help || got.yes != tc.want.yes {
				t.Fatalf("parseSyncArgs(%v) flags = %+v, want %+v", tc.args, got, tc.want)
			}
			if strings.Join(got.agents, ",") != strings.Join(tc.want.agents, ",") {
				t.Fatalf("parseSyncArgs(%v) agents = %v, want %v", tc.args, got.agents, tc.want.agents)
			}
		})
	}
}

func TestResolveAction(t *testing.T) {
	tt := []struct {
		name        string
		opts        syncOptions
		interactive bool
		want        pushAction
	}{
		// Explicit flags win everywhere.
		{name: "dry-run flag", opts: syncOptions{dryRun: true}, interactive: true, want: pushPreview},
		{name: "dry-run non-interactive", opts: syncOptions{dryRun: true}, want: pushPreview},
		{name: "yes flag", opts: syncOptions{yes: true}, interactive: true, want: pushApply},
		{name: "yes non-interactive", opts: syncOptions{yes: true}, want: pushApply},
		// dry-run is the safer intent; it must beat --yes on a contradictory line.
		{name: "dry-run beats yes", opts: syncOptions{dryRun: true, yes: true}, want: pushPreview},
		// Bare form adapts to context.
		{name: "bare interactive", interactive: true, want: pushConfirm},
		{name: "bare scripted", want: pushPreviewHint},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveAction(tc.opts, tc.interactive)
			if got != tc.want {
				t.Fatalf("resolveAction(%+v, %v) = %v, want %v", tc.opts, tc.interactive, got, tc.want)
			}
		})
	}
}

func TestSyncMissingHostSourceIsFlagged(t *testing.T) {
	f := newSyncFixture(t)
	entry := configSyncEntry{
		hostPath: func(home string) string { return filepath.Join(home, ".absent-agent", "config.toml") },
		guestRel: ".absent-agent/config.toml",
		role:     roleConfig,
	}
	res := syncOneEntry(f.hostHome, f.guestHome, entry, false, false)
	if !res.missingHost || res.pushed != 0 || res.err != nil {
		t.Fatalf("absent host source must flag missingHost: %+v", res)
	}
	// The empty resolver result (agent not installed) flags the same way.
	entry.hostPath = func(_ string) string { return "" }
	res = syncOneEntry(f.hostHome, f.guestHome, entry, false, false)
	if !res.missingHost {
		t.Fatalf("empty hostPath must flag missingHost: %+v", res)
	}
}

func TestSyncLockAcquireAndRelease(t *testing.T) {
	release, err := acquireSyncLock(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	release()
	// Re-acquire after release: the flock must not be left held.
	release2, err := acquireSyncLock(t.TempDir())
	if err != nil {
		t.Fatalf("re-acquire after release failed: %v", err)
	}
	release2()
}

// Dotfiles managers symlink config files routinely; host sources are
// user-owned and not guest-writable, so sync must follow them safely.
func TestSyncFollowsHostSymlinkedConfigFile(t *testing.T) {
	f := newSyncFixture(t)
	realFile := f.hostFile("dotfiles/CLAUDE.md", "# rules")
	if err := os.MkdirAll(filepath.Join(f.hostHome, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realFile, filepath.Join(f.hostHome, ".claude", "CLAUDE.md")); err != nil {
		t.Fatal(err)
	}

	entry := configSyncEntry{
		hostPath: func(home string) string { return filepath.Join(home, ".claude", "CLAUDE.md") },
		guestRel: ".claude/CLAUDE.md",
		role:     roleConfig,
	}
	res := syncOneEntry(f.hostHome, f.guestHome, entry, false, false)
	if res.pushed != 1 || res.err != nil {
		t.Fatalf("symlinked host config must be followed: %+v", res)
	}
	got, err := f.readGuest(".claude/CLAUDE.md")
	if err != nil || string(got) != "# rules" {
		t.Fatalf("guest content: %q, %v", got, err)
	}
}

func TestSyncDirFollowsSymlinkedFiles(t *testing.T) {
	f := newSyncFixture(t)
	realFile := f.hostFile("dotfiles/rules/one.md", "one")
	f.hostDir(".claude/commands")
	if err := os.Symlink(realFile, filepath.Join(f.hostHome, ".claude", "commands", "one.md")); err != nil {
		t.Fatal(err)
	}

	entry := configSyncEntry{
		hostPath: func(home string) string { return filepath.Join(home, ".claude", "commands") },
		guestRel: ".claude/commands",
		role:     roleConfig,
		dir:      true,
	}
	res := syncOneEntry(f.hostHome, f.guestHome, entry, false, false)
	if res.pushed != 1 || res.skipped != 0 || res.err != nil {
		t.Fatalf("symlinked file inside host dir must be followed: %+v", res)
	}
	got, err := f.readGuest(".claude/commands/one.md")
	if err != nil || string(got) != "one" {
		t.Fatalf("guest content: %q, %v", got, err)
	}
}

func TestSyncSkipsHostSymlinkToFifo(t *testing.T) {
	f := newSyncFixture(t)
	fifo := filepath.Join(t.TempDir(), "pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(f.hostHome, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(fifo, filepath.Join(f.hostHome, ".claude", "settings.json")); err != nil {
		t.Fatal(err)
	}

	entry := configSyncEntry{
		hostPath: func(home string) string { return filepath.Join(home, ".claude", "settings.json") },
		guestRel: ".claude/settings.json",
		role:     roleConfig,
	}
	res := syncOneEntry(f.hostHome, f.guestHome, entry, false, false)
	if res.pushed != 0 || res.err == nil {
		t.Fatalf("symlink to FIFO must skip with a reason, never open: %+v", res)
	}
	if _, err := os.Stat(f.guestPath(".claude/settings.json")); !os.IsNotExist(err) {
		t.Fatalf("FIFO source must not be created in the guest tree: %v", err)
	}
}

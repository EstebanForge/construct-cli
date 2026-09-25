package runtime

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EstebanForge/construct-cli/internal/config"
)

// withRootsTestHome isolates HOME so roots.json lands in a temp dir.
func withRootsTestHome(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
}

// TestRootsStoreLoadMissing: missing roots.json returns empty store.
func TestRootsStoreLoadMissing(t *testing.T) {
	withRootsTestHome(t)
	store, err := LoadRootsStore()
	if err != nil {
		t.Fatalf("LoadRootsStore on missing file: %v", err)
	}
	if store.Version != rootsStoreVersion {
		t.Errorf("expected version %d, got %d", rootsStoreVersion, store.Version)
	}
	if len(store.Roots) != 0 {
		t.Errorf("expected empty roots, got %d", len(store.Roots))
	}
}

// TestRootsStoreSaveLoadRoundTrip: write + read returns the same content.
func TestRootsStoreSaveLoadRoundTrip(t *testing.T) {
	withRootsTestHome(t)
	want := RootsStore{
		Version: rootsStoreVersion,
		Roots: []LearnedRoot{
			{Path: "/a", LearnedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), LastUsed: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)},
			{Path: "/b", LearnedAt: time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC), LastUsed: time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC)},
		},
	}
	if err := SaveRootsStore(want); err != nil {
		t.Fatalf("SaveRootsStore: %v", err)
	}
	got, err := LoadRootsStore()
	if err != nil {
		t.Fatalf("LoadRootsStore after save: %v", err)
	}
	if len(got.Roots) != len(want.Roots) {
		t.Fatalf("len mismatch: got %d want %d", len(got.Roots), len(want.Roots))
	}
	for i := range want.Roots {
		if got.Roots[i].Path != want.Roots[i].Path {
			t.Errorf("entry %d path: got %q want %q", i, got.Roots[i].Path, want.Roots[i].Path)
		}
	}
}

// TestRootsStoreSaveAtomic: save fails cleanly if a temp file cannot be
// renamed (we inject by making the target a directory). The store file
// itself is untouched.
func TestRootsStoreSaveAtomicWhenTargetIsDir(t *testing.T) {
	withRootsTestHome(t)
	// Pre-populate the store.
	if err := SaveRootsStore(RootsStore{Version: rootsStoreVersion}); err != nil {
		t.Fatalf("initial save: %v", err)
	}
	// Make the file path point at a directory. SaveRootsStore's
	// os.Rename will fail with EISDIR. The pre-existing file is preserved.
	if err := os.Remove(rootsStoreFilePath()); err != nil {
		t.Fatalf("rm store: %v", err)
	}
	if err := os.Mkdir(rootsStoreFilePath(), 0o755); err != nil {
		t.Fatalf("mkdir store: %v", err)
	}
	store := RootsStore{Version: rootsStoreVersion, Roots: []LearnedRoot{
		{Path: "/z", LearnedAt: time.Now(), LastUsed: time.Now()},
	}}
	if err := SaveRootsStore(store); err == nil {
		t.Errorf("Errored save succeeded; expected an error")
	}
}

// TestTouchRootNew: appending a new path creates the entry.
func TestTouchRootNew(t *testing.T) {
	s := RootsStore{}
	now := time.Now()
	s.TouchRoot("/x", now)
	if len(s.Roots) != 1 {
		t.Fatalf("expected 1 root, got %d", len(s.Roots))
	}
	if s.Roots[0].Path != "/x" || !s.Roots[0].LastUsed.Equal(now) {
		t.Errorf("entry mismatch: %+v", s.Roots[0])
	}
}

// TestTouchRootExisting: touching an existing path updates LastUsed only,
// preserves LearnedAt, leaves the slice length unchanged.
func TestTouchRootExisting(t *testing.T) {
	s := RootsStore{
		Roots: []LearnedRoot{
			{Path: "/x", LearnedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), LastUsed: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)},
		},
	}
	later := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	s.TouchRoot("/x", later)
	if len(s.Roots) != 1 {
		t.Fatalf("expected 1 root, got %d", len(s.Roots))
	}
	if !s.Roots[0].LearnedAt.Equal(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("LearnedAt changed: %v", s.Roots[0].LearnedAt)
	}
	if !s.Roots[0].LastUsed.Equal(later) {
		t.Errorf("LastUsed not updated: %v", s.Roots[0].LastUsed)
	}
}

// TestForgetRoot: removes the matching entry, returns true; non-match
// returns false and leaves the slice unchanged.
func TestForgetRoot(t *testing.T) {
	s := RootsStore{
		Roots: []LearnedRoot{
			{Path: "/a", LearnedAt: time.Now(), LastUsed: time.Now()},
			{Path: "/b", LearnedAt: time.Now(), LastUsed: time.Now()},
		},
	}
	if !s.ForgetRoot("/a") {
		t.Errorf("ForgetRoot(/a) returned false")
	}
	if len(s.Roots) != 1 || s.Roots[0].Path != "/b" {
		t.Errorf("unexpected roots after forget: %+v", s.Roots)
	}
	if s.ForgetRoot("/missing") {
		t.Errorf("ForgetRoot on missing path returned true")
	}
}

// TestEvictLRU drops the oldest entries until len <= cap; cap=0 disables.
func TestEvictLRU(t *testing.T) {
	now := time.Now()
	s := RootsStore{
		Roots: []LearnedRoot{
			{Path: "/oldest", LearnedAt: now.Add(-3 * time.Hour), LastUsed: now.Add(-3 * time.Hour)},
			{Path: "/middle", LearnedAt: now.Add(-2 * time.Hour), LastUsed: now.Add(-2 * time.Hour)},
			{Path: "/newest", LearnedAt: now.Add(-1 * time.Hour), LastUsed: now.Add(-1 * time.Hour)},
		},
	}
	evicted := s.EvictLRU(1)
	if len(evicted) != 2 || evicted[0] != "/oldest" || evicted[1] != "/middle" {
		t.Errorf("eviction order wrong: %v", evicted)
	}
	if len(s.Roots) != 1 || s.Roots[0].Path != "/newest" {
		t.Errorf("post-eviction roots: %+v", s.Roots)
	}
	if s.EvictLRU(0) != nil {
		t.Errorf("cap=0 should not evict")
	}
}

// TestRootsPathsDropsMissing: paths() skips entries whose host path no
// longer exists (stat check). Plant a real temp dir for the survivor.
func TestRootsPathsDropsMissing(t *testing.T) {
	tmp := t.TempDir()
	realDir := filepath.Join(tmp, "alive")
	if err := os.Mkdir(realDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	s := RootsStore{
		Roots: []LearnedRoot{
			{Path: realDir, LearnedAt: time.Now(), LastUsed: time.Now()},
			{Path: "/definitely/not/a/real/path", LearnedAt: time.Now(), LastUsed: time.Now()},
		},
	}
	paths := s.Paths()
	if len(paths) != 1 || paths[0] != realDir {
		t.Errorf("Paths() = %v, want only the live dir", paths)
	}
}

// TestRequestLearnRootAutoLearnsInsideHome: inside the user's home there
// is no consent gate — the folder the user invoked from is auto-learned
// and persisted, headless or not (Docker parity). The confirm seam must
// never fire.
func TestRequestLearnRootAutoLearnsInsideHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, "projects", "app")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	origInteractive, origConfirm := learnRootInteractive, learnRootConfirm
	learnRootInteractive = func() bool {
		t.Error("inside-home learn must not consult interactivity")
		return false
	}
	learnRootConfirm = func(string) bool {
		t.Error("inside-home learn must not prompt")
		return false
	}
	t.Cleanup(func() { learnRootInteractive, learnRootConfirm = origInteractive, origConfirm })

	cfg := config.DefaultConfig()
	learned, err := requestLearnRoot(&cfg, dir)
	if err != nil || !learned {
		t.Fatalf("learned=%v err=%v, want auto-learn success", learned, err)
	}
	store, lerr := LoadRootsStore()
	if lerr != nil {
		t.Fatalf("load store: %v", lerr)
	}
	if !store.Has(cleanProjectDir(dir)) {
		t.Error("inside-home root was not persisted")
	}
}

// TestRequestLearnRootHomeItselfNotAutoLearned: the home directory itself
// is not "inside home" — learning it would mount the entire home, so it
// keeps the checkpoint flow (headless: fail closed, nothing persisted).
func TestRequestLearnRootHomeItselfNotAutoLearned(t *testing.T) {
	withRootsTestHome(t)
	home := os.Getenv("HOME")

	cfg := config.DefaultConfig()
	learned, err := requestLearnRoot(&cfg, home)
	if !errors.Is(err, ErrMsbDaemonWorkdirUnmapped) {
		t.Fatalf("err = %v, want ErrMsbDaemonWorkdirUnmapped", err)
	}
	if learned {
		t.Error("home itself must not be auto-learned")
	}
	store, lerr := LoadRootsStore()
	if lerr != nil {
		t.Fatalf("load store: %v", lerr)
	}
	if store.Has(cleanProjectDir(home)) {
		t.Error("home itself must not be persisted")
	}
}

// TestRequestLearnRootNonInteractiveDeny pins the OUTSIDE-home consent
// gate: with no TTY on stdin, an unknown root must NOT be learned — the
// caller gets ErrMsbDaemonWorkdirUnmapped and the store stays untouched.
func TestRequestLearnRootNonInteractiveDeny(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	newDir := t.TempDir()

	cfg := config.DefaultConfig()
	learned, err := requestLearnRoot(&cfg, newDir)
	if !errors.Is(err, ErrMsbDaemonWorkdirUnmapped) {
		t.Fatalf("err = %v, want ErrMsbDaemonWorkdirUnmapped", err)
	}
	if learned {
		t.Error("learned must be false on the non-interactive deny path")
	}
	store, rerr := LoadRootsStore()
	if rerr != nil {
		t.Fatalf("load store: %v", rerr)
	}
	if store.Has(cleanProjectDir(newDir)) {
		t.Error("denied root must not be persisted")
	}
}

// TestRequestLearnRootDeclinePersists: an interactive NO is recorded in
// the store so the folder never re-prompts, and the error tells the user
// how to reverse the decision later.
func TestRequestLearnRootDeclinePersists(t *testing.T) {
	withRootsTestHome(t)
	newDir := t.TempDir()

	origInteractive, origConfirm := learnRootInteractive, learnRootConfirm
	learnRootInteractive = func() bool { return true }
	learnRootConfirm = func(string) bool { return false }
	t.Cleanup(func() { learnRootInteractive, learnRootConfirm = origInteractive, origConfirm })

	cfg := config.DefaultConfig()
	learned, err := requestLearnRoot(&cfg, newDir)
	if learned {
		t.Error("declined run must not learn the root")
	}
	if !errors.Is(err, ErrMsbDaemonWorkdirDeclined) {
		t.Fatalf("err = %v, want ErrMsbDaemonWorkdirDeclined", err)
	}
	if !strings.Contains(err.Error(), "construct sys daemon roots add") {
		t.Errorf("decline error lacks the manual-add command: %v", err)
	}
	store, lerr := LoadRootsStore()
	if lerr != nil {
		t.Fatalf("load store: %v", lerr)
	}
	if !store.IsDeclined(cleanProjectDir(newDir)) {
		t.Error("decline was not persisted")
	}
}

// TestRequestLearnRootDeclinedSkipsPrompt: a previously declined folder
// short-circuits BEFORE the gum prompt — re-running ct from the same
// folder must not ask again.
func TestRequestLearnRootDeclinedSkipsPrompt(t *testing.T) {
	withRootsTestHome(t)
	dir := t.TempDir()

	store, err := LoadRootsStore()
	if err != nil {
		t.Fatalf("load store: %v", err)
	}
	store.DeclineRoot(cleanProjectDir(dir), time.Now())
	if err := SaveRootsStore(store); err != nil {
		t.Fatalf("save store: %v", err)
	}

	origConfirm := learnRootConfirm
	learnRootConfirm = func(string) bool {
		t.Error("declined folder must not re-prompt")
		return true
	}
	t.Cleanup(func() { learnRootConfirm = origConfirm })

	cfg := config.DefaultConfig()
	learned, lerr := requestLearnRoot(&cfg, dir)
	if learned || !errors.Is(lerr, ErrMsbDaemonWorkdirDeclined) {
		t.Fatalf("learned=%v err=%v, want declined error", learned, lerr)
	}
}

// TestDaemonRootsAddClearsDecline: roots add is the way back after a
// decline — it mounts the folder and drops the decline record.
func TestDaemonRootsAddClearsDecline(t *testing.T) {
	withRootsTestHome(t)
	dir := t.TempDir()

	store, err := LoadRootsStore()
	if err != nil {
		t.Fatalf("load store: %v", err)
	}
	store.DeclineRoot(cleanProjectDir(dir), time.Now())
	if err := SaveRootsStore(store); err != nil {
		t.Fatalf("save store: %v", err)
	}

	cfg := config.DefaultConfig()
	DaemonRootsAdd(&cfg, dir)

	store, lerr := LoadRootsStore()
	if lerr != nil {
		t.Fatalf("load store: %v", lerr)
	}
	if !store.Has(cleanProjectDir(dir)) {
		t.Error("roots add did not learn the root")
	}
	if store.IsDeclined(cleanProjectDir(dir)) {
		t.Error("roots add did not clear the decline record")
	}
}

// TestEffectiveWorkspaceRootsExcludesDeclined: a declined cwd never
// enters the mount set (it would otherwise ride the uncovered-cwd append).
// Seeds the CANONICALIZED path: decline keys are cleanProjectDir-resolved,
// and on macOS the temp dir sits behind a symlink (/var -> /private/var),
// so a raw t.TempDir key would not match and the leak would go unnoticed.
func TestEffectiveWorkspaceRootsExcludesDeclined(t *testing.T) {
	dir := t.TempDir()
	store := RootsStore{Version: rootsStoreVersion}
	store.DeclineRoot(cleanProjectDir(dir), time.Now())

	roots := effectiveWorkspaceRoots(dir, store)
	if len(roots) != 0 {
		t.Fatalf("declined cwd leaked into the mount set: %v", roots)
	}
}

// TestDaemonRootsForgetClearsDecline: forget on a declined-only path drops
// the decline record — including for folders already deleted from disk,
// which `roots add` would refuse. Without this, a declined folder that is
// later removed can only be cleaned by editing roots.json by hand.
func TestDaemonRootsForgetClearsDecline(t *testing.T) {
	withRootsTestHome(t)
	dir := filepath.Join(t.TempDir(), "gone")

	store, err := LoadRootsStore()
	if err != nil {
		t.Fatalf("load store: %v", err)
	}
	store.DeclineRoot(dir, time.Now())
	if err := SaveRootsStore(store); err != nil {
		t.Fatalf("save store: %v", err)
	}

	cfg := config.DefaultConfig()
	DaemonRootsForget(&cfg, dir)

	store, lerr := LoadRootsStore()
	if lerr != nil {
		t.Fatalf("load store: %v", lerr)
	}
	if store.IsDeclined(dir) {
		t.Error("forget did not clear the decline record")
	}
	if len(store.Roots) != 0 {
		t.Errorf("forget must not invent learned roots: %v", store.Roots)
	}
}

// TestCleanProjectDirAbsolutizesRelative: relative inputs (e.g. "." from
// `roots add .`) must canonicalize to absolute paths, or decline keys and
// mount hashes become cwd-dependent.
func TestCleanProjectDirAbsolutizesRelative(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if got := cleanProjectDir("."); !filepath.IsAbs(got) {
		t.Fatalf("cleanProjectDir(\".\") = %q, want absolute", got)
	}
	if got := cleanProjectDir("relative/dir"); !filepath.IsAbs(got) {
		t.Fatalf("cleanProjectDir(relative) = %q, want absolute", got)
	}
}

// TestRequestLearnRootDeclinedInsideHomeWins: the exclusion list beats
// auto-learn, including for SUBTREES — declining ~/Secret excludes
// ~/Secret/sub, and the error names the record the user originally
// declined (the one `roots add` reverses).
func TestRequestLearnRootDeclinedInsideHomeWins(t *testing.T) {
	withRootsTestHome(t)
	home := os.Getenv("HOME")
	secret := filepath.Join(home, "Secret")
	if err := os.MkdirAll(filepath.Join(secret, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := LoadRootsStore()
	if err != nil {
		t.Fatalf("load store: %v", err)
	}
	store.DeclineRoot(cleanProjectDir(secret), time.Now())
	if err := SaveRootsStore(store); err != nil {
		t.Fatalf("save store: %v", err)
	}

	origConfirm := learnRootConfirm
	learnRootConfirm = func(string) bool {
		t.Error("declined subtree must not re-prompt")
		return true
	}
	t.Cleanup(func() { learnRootConfirm = origConfirm })

	cfg := config.DefaultConfig()
	learned, lerr := requestLearnRoot(&cfg, filepath.Join(secret, "sub"))
	if learned || !errors.Is(lerr, ErrMsbDaemonWorkdirDeclined) {
		t.Fatalf("learned=%v err=%v, want declined error", learned, lerr)
	}
	if !strings.Contains(lerr.Error(), cleanProjectDir(secret)) {
		t.Errorf("error must name the declined record %s: %v", cleanProjectDir(secret), lerr)
	}
	s, rerr := LoadRootsStore()
	if rerr != nil {
		t.Fatalf("load store: %v", rerr)
	}
	if s.Has(cleanProjectDir(filepath.Join(secret, "sub"))) {
		t.Error("declined subtree must not be auto-learned")
	}
}

// TestRequestLearnRootSensitiveHomePathNotAutoLearned: hidden top-level
// home dirs (credential and cache trees like ~/.ssh, ~/.aws, ~/.gnupg)
// and macOS ~/Library keep the checkpoint regime: headless fails closed
// and nothing is persisted.
func TestRequestLearnRootSensitiveHomePathNotAutoLearned(t *testing.T) {
	withRootsTestHome(t)
	home := os.Getenv("HOME")
	sensitive := []string{
		filepath.Join(home, ".ssh"),
		filepath.Join(home, ".aws", "credentials-dir"),
		filepath.Join(home, "Library"),
	}
	for _, dir := range sensitive {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		// cleanProjectDir resolves symlinks; make the stored key the same
		// shape the code would persist, then check non-persistence below.
		cfg := config.DefaultConfig()
		learned, err := requestLearnRoot(&cfg, dir)
		if learned || !errors.Is(err, ErrMsbDaemonWorkdirUnmapped) {
			t.Errorf("%s: learned=%v err=%v, want fail-closed unmapped", dir, learned, err)
		}
		store, lerr := LoadRootsStore()
		if lerr != nil {
			t.Fatalf("load store: %v", lerr)
		}
		if store.Has(cleanProjectDir(dir)) {
			t.Errorf("%s must not be auto-learned", dir)
		}
	}
}

// TestRequestLearnRootDisabledWhenCapZero: max_learned_roots = 0 disables
// the learned-root mechanism entirely — even inside $HOME, an uncovered
// folder fails with the config hint and nothing is persisted.
func TestRequestLearnRootDisabledWhenCapZero(t *testing.T) {
	withRootsTestHome(t)
	home := os.Getenv("HOME")
	dir := filepath.Join(home, "projects", "app")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := config.DefaultConfig()
	cfg.Daemon.MaxLearnedRoots = 0
	learned, err := requestLearnRoot(&cfg, dir)
	if learned || !errors.Is(err, ErrMsbDaemonWorkdirUnmapped) {
		t.Fatalf("learned=%v err=%v, want unmapped when learning disabled", learned, err)
	}
	store, lerr := LoadRootsStore()
	if lerr != nil {
		t.Fatalf("load store: %v", lerr)
	}
	if store.Has(cleanProjectDir(dir)) {
		t.Error("learning disabled must not persist roots")
	}
}

// TestInsideUserHomeSymlinkedAndSlashedHome: containment must survive a
// symlinked home (Fedora Atomic /home -> /var/home shape) and a trailing
// slash on $HOME, while the home directory itself stays outside.
func TestInsideUserHomeSymlinkedAndSlashedHome(t *testing.T) {
	realHome := t.TempDir()
	linkHome := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(realHome, linkHome); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	// cleanProjectDir resolves the input symlink, so a run "through" the
	// link home resolves under realHome; the raw-home comparison alone
	// would miss it.
	resolved := cleanProjectDir(filepath.Join(linkHome, "projects", "app"))

	t.Setenv("HOME", linkHome)
	if !insideUserHome(resolved) {
		t.Errorf("symlinked home containment failed for %q", resolved)
	}
	// Trailing slash on $HOME must not make the home itself auto-learnable.
	t.Setenv("HOME", realHome+string(os.PathSeparator))
	if insideUserHome(realHome) {
		t.Error("home itself must not count as inside home")
	}
	if !insideUserHome(filepath.Join(realHome, "projects")) {
		t.Error("plain containment broken")
	}
}

// TestRequestLearnRootHomeAlwaysPromptsNeverPersists: the home directory
// is the always-ask regime. Every run warns and asks (even after a prior
// acceptance), acceptance is never persisted as a learned root, and a NO
// persists nothing either. Acceptance proceeds via the run's one-off cwd
// mount, so (false, nil) is the correct outcome.
func TestRequestLearnRootHomeAlwaysPromptsNeverPersists(t *testing.T) {
	withRootsTestHome(t)
	home := os.Getenv("HOME")

	origInteractive, origDanger := learnRootInteractive, learnRootConfirmDanger
	t.Cleanup(func() { learnRootInteractive, learnRootConfirmDanger = origInteractive, origDanger })
	learnRootInteractive = func() bool { return true }

	prompts := 0
	learnRootConfirmDanger = func(string) bool {
		prompts++
		return true // accept every time
	}

	cfg := config.DefaultConfig()
	for i := 1; i <= 2; i++ {
		learned, err := requestLearnRoot(&cfg, home)
		if learned || err != nil {
			t.Fatalf("run %d: learned=%v err=%v, want proceed-without-persist", i, learned, err)
		}
		if prompts != i {
			t.Fatalf("run %d: prompted %d time(s), want %d", i, prompts, i)
		}
		store, lerr := LoadRootsStore()
		if lerr != nil {
			t.Fatalf("load store: %v", lerr)
		}
		if store.Has(cleanProjectDir(home)) {
			t.Fatal("home acceptance must never be persisted as a learned root")
		}
		if len(store.Roots) != 0 {
			t.Fatalf("unexpected learned roots: %v", store.Roots)
		}
	}

	// A NO also persists nothing: next run asks again.
	learnRootConfirmDanger = func(string) bool { return false }
	learned, err := requestLearnRoot(&cfg, home)
	if learned || !errors.Is(err, ErrMsbDaemonWorkdirDeclined) {
		t.Fatalf("declined run: learned=%v err=%v, want declined error", learned, err)
	}
	store, lerr := LoadRootsStore()
	if lerr != nil {
		t.Fatalf("load store: %v", lerr)
	}
	if store.IsDeclined(cleanProjectDir(home)) {
		t.Fatal("home refusal must never be persisted; the next run must ask again")
	}
}

// TestIsUserHomeMatchesRawAndResolved: isUserHome must recognize the home
// directory through both its raw and its symlink-resolved forms, and
// must not match subdirectories.
func TestIsUserHomeMatchesRawAndResolved(t *testing.T) {
	realHome := t.TempDir()
	linkHome := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(realHome, linkHome); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	t.Setenv("HOME", linkHome)

	if !isUserHome(cleanProjectDir(linkHome)) {
		t.Error("symlink-resolved home not recognized as home itself")
	}
	if isUserHome(cleanProjectDir(filepath.Join(linkHome, "projects"))) {
		t.Error("subdirectory must not count as home itself")
	}
}

// TestRequestLearnRootHomeHeadlessFailsClosed: headless execution from
// $HOME must fail closed with ErrMsbDaemonWorkdirUnmapped without prompting.
func TestRequestLearnRootHomeHeadlessFailsClosed(t *testing.T) {
	withRootsTestHome(t)
	home := os.Getenv("HOME")

	origInteractive := learnRootInteractive
	t.Cleanup(func() { learnRootInteractive = origInteractive })
	learnRootInteractive = func() bool { return false }

	cfg := config.DefaultConfig()
	learned, err := requestLearnRoot(&cfg, home)
	if learned || !errors.Is(err, ErrMsbDaemonWorkdirUnmapped) {
		t.Fatalf("headless home: learned=%v err=%v, want ErrMsbDaemonWorkdirUnmapped", learned, err)
	}
}

// TestRequestLearnRootHomeWarningText: the danger prompt must explicitly
// warn the user about mounting the ENTIRE home directory.
func TestRequestLearnRootHomeWarningText(t *testing.T) {
	withRootsTestHome(t)
	home := os.Getenv("HOME")

	origInteractive, origDanger := learnRootInteractive, learnRootConfirmDanger
	t.Cleanup(func() { learnRootInteractive, learnRootConfirmDanger = origInteractive, origDanger })
	learnRootInteractive = func() bool { return true }

	promptSeen := ""
	learnRootConfirmDanger = func(p string) bool {
		promptSeen = p
		return true
	}

	cfg := config.DefaultConfig()
	learned, err := requestLearnRoot(&cfg, home)
	if learned || err != nil {
		t.Fatalf("learned=%v err=%v, want (false, nil)", learned, err)
	}
	if !strings.Contains(promptSeen, "ENTIRE home directory") {
		t.Fatalf("prompt %q missing required danger warning", promptSeen)
	}
}

// TestRequestLearnRootHomeIgnoresLegacyRootsAndDeclines: legacy roots.json
// containing $HOME (from release 1.17.4 or manual edits) must not bypass
// the prompt and must not block subprojects.
func TestRequestLearnRootHomeIgnoresLegacyRootsAndDeclines(t *testing.T) {
	withRootsTestHome(t)
	home := cleanProjectDir(os.Getenv("HOME"))

	// Create a legacy store with home in roots and in declined
	legacyStore := RootsStore{
		Version: rootsStoreVersion,
		Roots: []LearnedRoot{
			{Path: home, LearnedAt: time.Now(), LastUsed: time.Now()},
		},
		Declined: map[string]time.Time{
			home: time.Now(),
		},
	}
	if err := SaveRootsStore(legacyStore); err != nil {
		t.Fatalf("save legacy store: %v", err)
	}

	// Loading must sanitize both roots and declined
	store, err := LoadRootsStore()
	if err != nil {
		t.Fatalf("load store: %v", err)
	}
	if len(store.Roots) != 0 {
		t.Fatalf("store.Roots retained home: %v", store.Roots)
	}
	if len(store.Declined) != 0 {
		t.Fatalf("store.Declined retained home: %v", store.Declined)
	}
	if store.Paths() != nil && len(store.Paths()) != 0 {
		t.Fatalf("store.Paths() returned home: %v", store.Paths())
	}
	if store.DeclineKeyFor(home) != "" {
		t.Fatalf("store.DeclineKeyFor returned non-empty: %s", store.DeclineKeyFor(home))
	}

	// requestLearnRoot must still prompt, not fail fast on legacy decline
	origInteractive, origDanger := learnRootInteractive, learnRootConfirmDanger
	t.Cleanup(func() { learnRootInteractive, learnRootConfirmDanger = origInteractive, origDanger })
	learnRootInteractive = func() bool { return true }

	prompted := false
	learnRootConfirmDanger = func(string) bool {
		prompted = true
		return true
	}

	cfg := config.DefaultConfig()
	learned, rerr := requestLearnRoot(&cfg, home)
	if learned || rerr != nil {
		t.Fatalf("learned=%v err=%v, want (false, nil)", learned, rerr)
	}
	if !prompted {
		t.Fatal("expected prompt to fire despite legacy records")
	}
}

// TestDaemonReuseInterleavedHomeAndSubproject: verify that daemon reuse and
// recreation hashes are stable across interleaved home and subproject runs.
func TestDaemonReuseInterleavedHomeAndSubproject(t *testing.T) {
	withRootsTestHome(t)
	home := cleanProjectDir(os.Getenv("HOME"))
	sub := filepath.Join(home, "projects", "app")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.Sandbox.MountSkills = false
	// The scenario mounts $HOME, which the workspace policy only permits
	// with AllowHomeWorkspace; production derives the allowHome argument
	// from this same field.
	cfg.Sandbox.AllowHomeWorkspace = true

	homeDest := GetMsbWorkspaceMountDest(home)
	subDest := GetMsbWorkspaceMountDest(sub)

	// Run 1: Daemon booted from home
	labelsHomeBoot := map[string]string{
		"construct.project_dir": home,
		DaemonMountsLabelKey:    hashDaemonMountPaths([]string{home}),
		DaemonSudoLabelKey:      "free",
	}
	cfgJSONHomeBoot := fmt.Sprintf(`{"mounts":[{"type":"Bind","guest":%q,"host":%q}]}`, homeDest, home)

	// Run 2: Consecutive home run with empty store -> should reuse (no recreate)
	recreate, reason := msbDaemonNeedsRecreate(DaemonMounts{}, labelsHomeBoot, cfgJSONHomeBoot, home, cfg.Sandbox.AllowHomeWorkspace, &cfg)
	if recreate {
		t.Fatalf("consecutive home run should reuse daemon, got recreate with reason: %s", reason)
	}

	// Learn subproject
	store, _ := LoadRootsStore()
	store.TouchRoot(sub, time.Now())
	if err := SaveRootsStore(store); err != nil {
		t.Fatalf("save roots: %v", err)
	}

	// Run 3: Run from subproject -> daemon booted from home needs recreate
	recreate, _ = msbDaemonNeedsRecreate(DaemonMounts{}, labelsHomeBoot, cfgJSONHomeBoot, sub, cfg.Sandbox.AllowHomeWorkspace, &cfg)
	if !recreate {
		t.Fatal("running subproject on home daemon must recreate")
	}

	// Daemon boots from subproject
	labelsSubBoot := map[string]string{
		"construct.project_dir": sub,
		DaemonMountsLabelKey:    hashDaemonMountPaths([]string{sub}),
		DaemonSudoLabelKey:      "free",
	}
	cfgJSONSubBoot := fmt.Sprintf(`{"mounts":[{"type":"Bind","guest":%q,"host":%q}]}`, subDest, sub)

	// Run 4: Run from home again -> needs recreate because home is unmounted
	recreate, _ = msbDaemonNeedsRecreate(DaemonMounts{}, labelsSubBoot, cfgJSONSubBoot, home, cfg.Sandbox.AllowHomeWorkspace, &cfg)
	if !recreate {
		t.Fatal("running home on subproject daemon must recreate to mount home")
	}

	// Recreated daemon has both home and sub
	combinedRoots := []string{home, sub}
	if home > sub {
		combinedRoots = []string{sub, home}
	}
	labelsCombinedBoot := map[string]string{
		"construct.project_dir": home,
		DaemonMountsLabelKey:    hashDaemonMountPaths(combinedRoots),
		DaemonSudoLabelKey:      "free",
	}
	cfgJSONCombinedBoot := fmt.Sprintf(`{"mounts":[{"type":"Bind","guest":%q,"host":%q},{"type":"Bind","guest":%q,"host":%q}]}`, homeDest, home, subDest, sub)

	// Run 5: Subsequent home run -> reuses
	recreate, reason = msbDaemonNeedsRecreate(DaemonMounts{}, labelsCombinedBoot, cfgJSONCombinedBoot, home, cfg.Sandbox.AllowHomeWorkspace, &cfg)
	if recreate {
		t.Fatalf("subsequent home run on combined daemon should reuse, got recreate: %s", reason)
	}
}

// TestRootsStoreAcceptLargeExportSubtree: a large-workspace acceptance
// covers the folder and everything below it, names the ancestor as the
// record key, and survives a reload.
func TestRootsStoreAcceptLargeExportSubtree(t *testing.T) {
	withRootsTestHome(t)
	dir := t.TempDir()
	store, err := LoadRootsStore()
	if err != nil {
		t.Fatalf("LoadRootsStore: %v", err)
	}
	store.AcceptLargeExport(dir, time.Now().UTC())
	if !store.IsAccepted(dir) {
		t.Fatal("IsAccepted(dir) = false after AcceptLargeExport")
	}
	sub := filepath.Join(dir, "sub")
	if !store.IsAccepted(sub) {
		t.Fatal("acceptance does not cover subdirectories")
	}
	if key := store.AcceptedKeyFor(sub); key != dir {
		t.Fatalf("AcceptedKeyFor(sub) = %q, want the ancestor %q", key, dir)
	}
	if store.IsAccepted(filepath.Join(filepath.Dir(dir), "elsewhere")) {
		t.Fatal("acceptance leaked outside the folder subtree")
	}
	if err := SaveRootsStore(store); err != nil {
		t.Fatalf("SaveRootsStore: %v", err)
	}
	reloaded, err := LoadRootsStore()
	if err != nil {
		t.Fatalf("LoadRootsStore after save: %v", err)
	}
	if !reloaded.IsAccepted(dir) {
		t.Fatal("acceptance did not survive a reload")
	}
}

// TestEnforceWorkspaceRememberedAsksOnceThenSkips: the large-workspace
// confirm fires exactly once, the Yes persists, and the next run from the
// same folder is silent.
func TestEnforceWorkspaceRememberedAsksOnceThenSkips(t *testing.T) {
	withRootsTestHome(t)
	dir := t.TempDir()
	for i := 0; i < 3; i++ {
		if werr := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%d", i)), []byte("x"), 0o644); werr != nil {
			t.Fatalf("seed file: %v", werr)
		}
	}
	original := workspaceConfirm
	calls := 0
	workspaceConfirm = func(string) bool {
		calls++
		return true
	}
	t.Cleanup(func() { workspaceConfirm = original })

	// maxEntries 2 against 3 files forces the Large verdict on any host.
	if err := EnforceWorkspaceRemembered(dir, 2, false, true); err != nil {
		t.Fatalf("first run errored: %v", err)
	}
	if calls != 1 {
		t.Fatalf("confirm calls = %d, want 1", calls)
	}
	workspaceConfirm = func(string) bool {
		t.Error("confirm re-asked after acceptance was recorded")
		return false
	}
	if err := EnforceWorkspaceRemembered(dir, 2, false, true); err != nil {
		t.Fatalf("second run errored: %v", err)
	}
	if calls != 1 {
		t.Fatalf("confirm calls after second run = %d, want still 1", calls)
	}
}

// TestEnforceWorkspaceRememberedDeclinePersistsNothing: answering No fails
// the run and records nothing, so the warning fires again next run.
func TestEnforceWorkspaceRememberedDeclinePersistsNothing(t *testing.T) {
	withRootsTestHome(t)
	dir := t.TempDir()
	for i := 0; i < 3; i++ {
		if werr := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%d", i)), []byte("x"), 0o644); werr != nil {
			t.Fatalf("seed file: %v", werr)
		}
	}
	original := workspaceConfirm
	workspaceConfirm = func(string) bool { return false }
	t.Cleanup(func() { workspaceConfirm = original })

	if err := EnforceWorkspaceRemembered(dir, 2, false, true); !errors.Is(err, ErrWorkspaceRefused) {
		t.Fatalf("declined run error = %v, want ErrWorkspaceRefused", err)
	}
	store, err := LoadRootsStore()
	if err != nil {
		t.Fatalf("LoadRootsStore: %v", err)
	}
	if store.IsAccepted(dir) {
		t.Fatal("a declined size warning must not persist an acceptance")
	}
}

// TestDaemonRootsForgetClearsAcceptance: forget on an accepted-but-never-
// learned folder drops the acceptance instead of erroring.
func TestDaemonRootsForgetClearsAcceptance(t *testing.T) {
	withRootsTestHome(t)
	dir := t.TempDir()
	if rerr := rememberLargeExport(dir); rerr != nil {
		t.Fatalf("rememberLargeExport: %v", rerr)
	}
	DaemonRootsForget(nil, dir) // failure paths os.Exit and fail the test
	store, err := LoadRootsStore()
	if err != nil {
		t.Fatalf("LoadRootsStore: %v", err)
	}
	if store.IsAccepted(dir) {
		t.Fatal("forget must clear the size-warning acceptance")
	}
}

// TestEnforceWorkspaceRememberedHeadlessFailsClosed: headless runs never
// consult the confirm, fail closed on a large workspace, and persist
// nothing.
func TestEnforceWorkspaceRememberedHeadlessFailsClosed(t *testing.T) {
	withRootsTestHome(t)
	dir := t.TempDir()
	for i := 0; i < 3; i++ {
		if werr := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%d", i)), []byte("x"), 0o644); werr != nil {
			t.Fatalf("seed file: %v", werr)
		}
	}
	original := workspaceConfirm
	workspaceConfirm = func(string) bool {
		t.Error("headless run must not consult the confirm seam")
		return true
	}
	t.Cleanup(func() { workspaceConfirm = original })

	if err := EnforceWorkspaceRemembered(dir, 2, false, false); !errors.Is(err, ErrWorkspaceRefused) {
		t.Fatalf("headless large-workspace error = %v, want ErrWorkspaceRefused", err)
	}
	store, err := LoadRootsStore()
	if err != nil {
		t.Fatalf("LoadRootsStore: %v", err)
	}
	if store.IsAccepted(dir) {
		t.Fatal("headless runs must never persist an acceptance")
	}
}

// TestEnforceWorkspaceRememberedAcceptedSkipsGuard: once accepted, the
// folder passes with no scan and no confirm — even a confirm stub that
// fails the test stays silent.
func TestEnforceWorkspaceRememberedAcceptedSkipsGuard(t *testing.T) {
	withRootsTestHome(t)
	dir := t.TempDir()
	if rerr := rememberLargeExport(dir); rerr != nil {
		t.Fatalf("rememberLargeExport: %v", rerr)
	}
	original := workspaceConfirm
	workspaceConfirm = func(string) bool {
		t.Error("accepted folder must not re-confirm")
		return false
	}
	t.Cleanup(func() { workspaceConfirm = original })

	if err := EnforceWorkspaceRemembered(dir, 2, false, true); err != nil {
		t.Fatalf("accepted folder errored: %v", err)
	}
}

// TestAcceptLargeExportRefusesHome: the always-ask home regime must never
// gain an acceptance record, so an accepted skip can never mute the home
// danger warning.
func TestAcceptLargeExportRefusesHome(t *testing.T) {
	withRootsTestHome(t)
	home := os.Getenv("HOME")
	store, err := LoadRootsStore()
	if err != nil {
		t.Fatalf("LoadRootsStore: %v", err)
	}
	store.AcceptLargeExport(home, time.Now().UTC())
	if store.IsAccepted(home) {
		t.Fatal("home must never carry an acceptance record")
	}
	if len(store.Accepted) != 0 {
		t.Fatalf("Accepted map = %v, want empty", store.Accepted)
	}
}

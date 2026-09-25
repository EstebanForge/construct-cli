package runtime

import (
	"errors"
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

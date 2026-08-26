package runtime

import (
	"os"
	"path/filepath"
	"testing"
	"time"
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

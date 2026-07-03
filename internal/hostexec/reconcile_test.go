package hostexec

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"testing"
)

// allShims returns the basenames present in binDir, sorted, for assertions.
func allShims(t *testing.T, binDir string) []string {
	t.Helper()
	entries, err := os.ReadDir(binDir)
	if err != nil {
		t.Fatalf("readdir %s: %v", binDir, err)
	}
	var got []string
	for _, e := range entries {
		n := e.Name()
		if strings.HasPrefix(n, ".construct_host_exec") {
			continue // manifest + lock
		}
		if strings.HasPrefix(n, manifestName+".tmp-") {
			continue
		}
		got = append(got, n)
	}
	sort.Strings(got)
	return got
}

func TestReconcileCreatesLocalBinWhenAbsent(t *testing.T) {
	home := t.TempDir()
	// Precondition: .local/bin does not exist.
	if _, err := os.Stat(filepath.Join(home, ".local", "bin")); !os.IsNotExist(err) {
		t.Fatalf("expected .local/bin absent initially")
	}
	res, err := ReconcileShims(home, []string{"wicket"})
	if err != nil {
		t.Fatalf("ReconcileShims: %v", err)
	}
	if res.Skipped {
		t.Fatal("expected reconcile not skipped")
	}
	if len(res.Created) != 1 || res.Created[0] != "wicket" {
		t.Fatalf("expected Created=[wicket], got %v", res.Created)
	}
	got := allShims(t, filepath.Join(home, ".local", "bin"))
	if len(got) != 1 || got[0] != "wicket" {
		t.Fatalf("expected [wicket], got %v", got)
	}
}

func TestReconcileIdempotentWhenUnchanged(t *testing.T) {
	home := t.TempDir()
	if _, err := ReconcileShims(home, []string{"wicket", "foo"}); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	res, err := ReconcileShims(home, []string{"wicket", "foo"})
	if err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	if len(res.Created) != 0 || len(res.Removed) != 0 {
		t.Fatalf("expected no-op, got Created=%v Removed=%v", res.Created, res.Removed)
	}
}

func TestReconcileRemovesUnlisted(t *testing.T) {
	home := t.TempDir()
	if _, err := ReconcileShims(home, []string{"wicket", "foo", "bar"}); err != nil {
		t.Fatalf("first: %v", err)
	}
	res, err := ReconcileShims(home, []string{"wicket"})
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	sort.Strings(res.Removed)
	if len(res.Removed) != 2 || res.Removed[0] != "bar" || res.Removed[1] != "foo" {
		t.Fatalf("expected Removed=[bar foo], got %v", res.Removed)
	}
	got := allShims(t, filepath.Join(home, ".local", "bin"))
	if len(got) != 1 || got[0] != "wicket" {
		t.Fatalf("expected only [wicket] remaining, got %v", got)
	}
}

func TestReconcileDoesNotRemoveUserOwnedSymlink(t *testing.T) {
	home := t.TempDir()
	binDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A user-placed symlink with the same name as an allowlist entry, but
	// pointing somewhere else. Must survive reconcile.
	userTarget := "/usr/bin/something-else"
	if err := os.Symlink(userTarget, filepath.Join(binDir, "wicket")); err != nil {
		t.Fatal(err)
	}
	res, err := ReconcileShims(home, []string{"wicket"})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(res.Created) != 0 {
		t.Fatalf("expected no creation (user file present), got %v", res.Created)
	}
	dest, err := os.Readlink(filepath.Join(binDir, "wicket"))
	if err != nil {
		t.Fatal(err)
	}
	if dest != userTarget {
		t.Fatalf("user symlink clobbered: now %s", dest)
	}

	// Now remove wicket from the list: the user symlink must STILL survive,
	// because it isn't ours (target != ShimTarget).
	if _, err := ReconcileShims(home, nil); err != nil {
		t.Fatalf("reconcile removal: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(binDir, "wicket")); err != nil {
		t.Fatalf("user symlink was wrongly removed: %v", err)
	}
}

func TestReconcileRemovesStaleConstructSymlinkReplacedByUser(t *testing.T) {
	// Edge case: construct created a shim, then user replaced it with their own
	// file at the same name. The manifest still lists it as ours, but the link
	// no longer points at ShimTarget. Reconcile must drop it from the manifest
	// WITHOUT deleting the user's file.
	home := t.TempDir()
	binDir := filepath.Join(home, ".local", "bin")
	if _, err := ReconcileShims(home, []string{"wicket"}); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(binDir, "wicket")
	if err := os.Remove(linkPath); err != nil {
		t.Fatal(err)
	}
	// User drops a regular file in its place.
	if err := os.WriteFile(linkPath, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReconcileShims(home, nil); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	data, err := os.ReadFile(linkPath)
	if err != nil {
		t.Fatalf("user file deleted: %v", err)
	}
	if string(data) != "mine" {
		t.Fatalf("user file corrupted: %q", data)
	}
}

func TestReconcileLockBusySkipsCleanly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("flock is unix-only")
	}
	home := t.TempDir()
	binDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Hold the lock manually so Reconcile sees EWOULDBLOCK.
	lockPath := filepath.Join(binDir, lockName)
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("preload lock: %v", err)
	}

	res, err := ReconcileShims(home, []string{"wicket"})
	if err != nil {
		t.Fatalf("expected no error on busy, got %v", err)
	}
	if !res.Skipped {
		t.Fatal("expected Skipped=true")
	}
	if len(res.Created) != 0 {
		t.Fatalf("expected no changes when skipped, got %v", res.Created)
	}
	// Nothing should have been created.
	got := allShims(t, binDir)
	if len(got) != 0 {
		t.Fatalf("expected empty bin dir when skipped, got %v", got)
	}
}

func TestManifestAtomicNoTempLeftBehind(t *testing.T) {
	home := t.TempDir()
	if _, err := ReconcileShims(home, []string{"wicket"}); err != nil {
		t.Fatal(err)
	}
	binDir := filepath.Join(home, ".local", "bin")
	entries, err := os.ReadDir(binDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), manifestName+".tmp-") {
			t.Fatalf("temp manifest left behind: %s", e.Name())
		}
	}
}

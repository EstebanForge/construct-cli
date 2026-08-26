package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// withDaemonLockTestHome resets HOME so DaemonLockPath points into a temp
// directory; the test cleans up via t.TempDir.
func withDaemonLockTestHome(t *testing.T) string {
	t.Helper()
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	return tmpHome
}

// TestDaemonLockHeldFree: no one holds the lock, DaemonLockHeld returns
// false. The lock file is created on first access (mode 0600).
func TestDaemonLockHeldFree(t *testing.T) {
	withDaemonLockTestHome(t)

	held, err := DaemonLockHeld()
	if err != nil {
		t.Fatalf("DaemonLockHeld: %v", err)
	}
	if held {
		t.Errorf("expected held=false when nobody holds the lock")
	}
}

// TestDaemonLockHeldWhenLocked: process A holds the lock; process B
// (simulated by a goroutine that holds a separate fd) sees held=true. We
// cannot spawn a real subprocess in a unit test, so we simulate by
// acquiring the lock from the same process via a second OpenFile (the
// flock is per-fd on Linux, but the LOCK_EX is exclusive across the
// process for the same fd — to get a realistic "another holder" signal
// we need a second fd on the same file. syscall.Flock on Linux IS per-fd,
// so a second OpenFile on the same path lets us hold a lock from one fd
// and test the other fd).
func TestDaemonLockHeldWhenLocked(t *testing.T) {
	withDaemonLockTestHome(t)

	path := DaemonLockPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Open the lock file from "another holder" perspective and grab an
	// exclusive flock on that fd. Then DaemonLockHeld (which uses its own
	// fresh fd) should report held=true.
	holder, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatalf("open holder: %v", err)
	}
	defer holder.Close()
	if err := flockEx(holder.Fd()); err != nil {
		t.Fatalf("holder flock: %v", err)
	}
	defer flockUn(holder.Fd())

	held, err := DaemonLockHeld()
	if err != nil {
		t.Fatalf("DaemonLockHeld: %v", err)
	}
	if !held {
		t.Errorf("expected held=true when another fd holds the lock")
	}
}

// TestAcquireDaemonLockReleasesOnCall: acquire, release, second acquire
// succeeds immediately (no 250ms notice, no wait).
func TestAcquireDaemonLockReleasesOnCall(t *testing.T) {
	withDaemonLockTestHome(t)

	release, err := acquireDaemonLock()
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	release()

	// Second acquire should not block. Wrap in a channel + timeout to
	// catch any accidental hang.
	type result struct {
		release func()
		err     error
	}
	done := make(chan result, 1)
	go func() {
		r, e := acquireDaemonLock()
		done <- result{r, e}
	}()
	select {
	case res := <-done:
		if res.err != nil {
			t.Errorf("second acquire after release: %v", res.err)
		}
		res.release()
	case <-time.After(2 * time.Second):
		t.Fatal("second acquire hung after release")
	}
}

// TestAcquireDaemonLockBlocksUntilReleased: two concurrent acquirers, the
// second observes the lock and only proceeds after the first releases.
// This is the core guarantee of phase 1: concurrent ct invocations
// serialize cleanly.
func TestAcquireDaemonLockBlocksUntilReleased(t *testing.T) {
	withDaemonLockTestHome(t)

	// First acquirer holds the lock and waits for the test's signal.
	release1, err := acquireDaemonLock()
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}

	// Second acquirer on a goroutine. Assert it is still blocked after a
	// short wait, then release the first and assert it completes quickly.
	type acquireResult struct {
		release func()
		err     error
	}
	var secondDone atomic.Bool
	var secondResult atomic.Pointer[acquireResult]
	go func() {
		r, e := acquireDaemonLock()
		secondResult.Store(&acquireResult{release: r, err: e})
		secondDone.Store(true)
	}()

	// Brief wait to give the goroutine a chance to start flock.
	time.Sleep(100 * time.Millisecond)
	if secondDone.Load() {
		t.Fatal("second acquire completed before first released")
	}

	// Release the first; second should now complete.
	release1()

	// Wait for second with a generous timeout (250ms notice + flock
	// acquisition overhead).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if secondDone.Load() {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !secondDone.Load() {
		t.Fatal("second acquire did not complete after first released")
	}
	if res := secondResult.Load(); res != nil {
		if res.err != nil {
			t.Errorf("second acquire error: %v", res.err)
		}
		if res.release != nil {
			res.release()
		}
	} else {
		t.Fatal("second acquire did not record a result")
	}
}

// TestAcquireDaemonLockNoopReleaseOnError: when acquire fails, the
// returned release func is a no-op (does not panic on call, does not
// block on flock). The current implementation acquires the file first and
// only fails on open/mkdir/flock errors; we trigger the flock failure by
// making the path a directory.
func TestAcquireDaemonLockNoopReleaseOnError(t *testing.T) {
	withDaemonLockTestHome(t)

	// Replace the lock path with a directory — os.OpenFile will fail.
	path := DaemonLockPath()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir lock path: %v", err)
	}

	release, err := acquireDaemonLock()
	if err == nil {
		t.Fatal("expected acquireDaemonLock to fail when lock path is a directory")
	}
	if release == nil {
		t.Fatal("expected a no-op release func on error (callers always defer)")
	}
	// The no-op release must not panic.
	release()
}

// TestDaemonLockNoticeFiresAfter250ms: when the second acquirer waits
// >daemonLockNoticeAfter, a stderr line is emitted (best-effort). We
// capture stderr and assert the line is present.
func TestDaemonLockNoticeFiresAfter250ms(t *testing.T) {
	withDaemonLockTestHome(t)

	// Hold the lock so the second acquirer blocks.
	release1, err := acquireDaemonLock()
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer release1()

	// Capture stderr while the second acquirer waits.
	origStderr := os.Stderr
	r, w, perr := os.Pipe()
	if perr != nil {
		t.Fatalf("pipe: %v", perr)
	}
	os.Stderr = w
	t.Cleanup(func() { os.Stderr = origStderr })

	done := make(chan struct{})
	go func() {
		defer close(done)
		release2, err := acquireDaemonLock()
		if err == nil && release2 != nil {
			release2()
		}
	}()

	// Release the first lock so the second can acquire (after the notice).
	go func() {
		time.Sleep(daemonLockNoticeAfter + 200*time.Millisecond)
		release1()
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("second acquirer hung")
	}

	if cerr := w.Close(); cerr != nil {
		t.Fatalf("close pipe write end: %v", cerr)
	}
	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	out := string(buf[:n])
	if !strings.Contains(out, "Waiting for another construct invocation") {
		t.Errorf("expected notice line in stderr, got:\n%s", out)
	}
}

// helpers — direct syscall.Flock wrappers so the test reads naturally.
// Linux-only (LOCK_EX=2, LOCK_UN=8); the daemon backend is microvm which
// runs on Linux/macOS but Flock semantics differ by platform. The constants
// are correct for Linux; on macOS the values match.
const (
	flockLockEx = 2
	flockLockUn = 8
)

func flockEx(fd uintptr) error                 { return syscall.Flock(int(fd), flockLockEx) }
func flockUn(fd uintptr) error                 { return syscall.Flock(int(fd), flockLockUn) }
func flockUnWithBool(fd uintptr, _ bool) error { return flockUn(fd) } //nolint:unused // kept for symmetry

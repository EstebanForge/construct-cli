package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/EstebanForge/construct-cli/internal/config"
)

// DaemonLockPath is the host-local flock file. One per construct config
// dir. A 10-minute first boot behind this lock is expected (matches the
// wide critical section in EnsureMsbDaemon: read state + decide + write
// state + recreate/boot, all as one unit). See docs/VMsv2.md phase 1.
//
// Exported so the doctor check can surface the path in its details.
func DaemonLockPath() string {
	return filepath.Join(config.GetConfigDir(), "daemon.lock")
}

// daemonLockNoticeAfter is how long acquireDaemonLock waits before printing
// the "waiting for another construct invocation" notice. Tuned so a fast
// acquire (e.g. second invocation that just observes the file) does not
// produce noise; long acquires (>250ms) tell the user something is going on.
const daemonLockNoticeAfter = 250 * time.Millisecond

// acquireDaemonLock blocks until the daemon flock is held, then returns a
// release func the caller must defer on every return path. The release
// both unlocks and closes the fd; the lock is released by the OS when
// the fd is closed even if the caller forgets to call release (defer +
// process exit covers all paths).
//
// On error (mkdir, openfile, flock) returns a no-op release so callers can
// always defer without a nil check.
//
// The notice is best-effort: if the lock is acquired within 250ms the
// goroutine exits silently; if it takes longer, a single info line fires.
// A 10-minute first boot behind the lock is expected; the notice is the
// only signal the user gets that the wait is intentional.
func acquireDaemonLock() (release func(), err error) {
	path := DaemonLockPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return func() {}, err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return func() {}, err
	}

	// 250ms notice: goroutine + timer, cancel on acquire. The goroutine
	// always exits; we leak no goroutines.
	//
	// The writer is captured BEFORE the goroutine starts: reading the
	// os.Stderr global from a goroutine that can outlive the caller's
	// frame is a data race against anything swapping os.Stderr (tests do).
	//
	// Disarm goes through its own once and fires on ACQUIRE (success or
	// error), not on release: the notice measures the acquisition wait.
	// A daemon setup that holds an uncontended lock for 10 minutes must
	// not print "waiting for another construct invocation" 250ms in.
	done := make(chan struct{})
	noticeStderr := os.Stderr
	var noticeOnce sync.Once
	disarmNotice := func() { noticeOnce.Do(func() { close(done) }) }
	go func() {
		select {
		case <-done:
			return
		case <-time.After(daemonLockNoticeAfter):
			//nolint:errcheck // best-effort notice; a closed stderr must not fail the run path
			fmt.Fprintln(noticeStderr, "Waiting for another construct invocation to finish daemon setup...")
		}
	}()

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		disarmNotice()
		//nolint:errcheck // best-effort cleanup; flock failure already surfaces the error
		_ = f.Close()
		return func() {}, err
	}
	// Acquired: disarm immediately. Slow HOLDS are normal and expected;
	// slow ACQUIRES are the only thing the notice exists for.
	disarmNotice()

	// On success, hold the file open in the closure so the OS keeps the
	// flock alive. Closing the fd releases the lock. sync.Once makes the
	// release idempotent: callers may defer it AND call it from a
	// shutdown path without panicking on double-close.
	var releaseOnce sync.Once
	release = func() {
		releaseOnce.Do(func() {
			disarmNotice() // no-op on the normal path; belt and braces
			//nolint:errcheck // LOCK_UN on a held fd; the OS releases on close either way
			_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
			//nolint:errcheck // best-effort cleanup; OS releases the lock on close
			_ = f.Close()
		})
	}
	return release, nil
}

// DaemonLockHeld reports whether the daemon flock is currently held by
// another process. Used by `ct sys doctor` to surface "daemon lock:
// free/held" so the user can tell when two constructs are racing.
//
// Non-blocking LOCK_EX | LOCK_NB: returns immediately with EWOULDBLOCK
// when another process holds the lock. On any error returns false plus the
// error (file missing is not an error here — treat as "free").
func DaemonLockHeld() (bool, error) {
	path := DaemonLockPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return false, err
	}
	defer f.Close() //nolint:errcheck // best-effort release; the explicit unlock above already ran
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if err == syscall.EWOULDBLOCK {
			return true, nil
		}
		return false, err
	}
	// Got the lock immediately — release before returning.
	//nolint:errcheck // best-effort probe; the OS releases on close either way
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return false, nil
}

package runtime

import (
	"errors"
	"testing"
	"time"
)

// TestIdleWatchLockSingleInstance: stacked agent runs each spawn a watcher;
// without a shared lock N simultaneous last-exits produce N watchers whose
// redundant fires race the update lock and stop path. The flock makes the
// first watcher the only one; later spawns exit immediately.
func TestIdleWatchLockSingleInstance(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	f1, err := acquireIdleWatchLock()
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	f2, err := acquireIdleWatchLock()
	if !errors.Is(err, errIdleWatchActive) {
		t.Fatalf("second acquire must report an active watcher, got %v", err)
	}
	if f2 != nil {
		_ = f2.Close()
		t.Fatalf("second acquire must not hand out a held file")
	}
	if err := f1.Close(); err != nil { // release = watcher exit
		t.Fatalf("close: %v", err)
	}
	f3, err := acquireIdleWatchLock()
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	_ = f3.Close()
}

// TestIdleWatchRunExitsWhenWatcherActive: a spawned watcher that finds the
// lock held must exit without arming a deadline or touching the daemon.
func TestIdleWatchRunExitsWhenWatcherActive(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	holder, err := acquireIdleWatchLock()
	if err != nil {
		t.Fatalf("hold lock: %v", err)
	}
	defer func() { _ = holder.Close() }()

	finished := make(chan struct{})
	go func() {
		IdleWatchRun(1)
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatalf("IdleWatchRun must exit immediately when another watcher holds the lock")
	}
}

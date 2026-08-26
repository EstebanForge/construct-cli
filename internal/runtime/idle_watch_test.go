package runtime

import (
	"os"
	"testing"
	"time"

	"github.com/EstebanForge/construct-cli/internal/config"
)

// withIdleTestHome isolates HOME for the sessions dir and config dir.
func withIdleTestHome(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
}

// TestMaybeSpawnIdleWatcherDisabledByZero: when IdleStopMinutes is 0 the
// helper is a no-op (feature explicitly disabled by the user).
func TestMaybeSpawnIdleWatcherDisabledByZero(t *testing.T) {
	withIdleTestHome(t)
	cfg := &config.Config{Daemon: config.DaemonConfig{IdleStopMinutes: 0}}
	MaybeSpawnIdleWatcher(cfg)
	// No detach-spawned process should be findable. We don't introspect
	// child PIDs; the no-op is observable by the absence of a session
	// file and no watcher process. The session count must be zero.
	if got := LiveSessionCount(); got != 0 {
		t.Errorf("LiveSessionCount = %d, want 0 (no session created)", got)
	}
}

// TestMaybeSpawnIdleWatcherSkipsWhenSessionsLive: a live session prevents
// the spawn even when minutes > 0. We can't easily prove the spawn did
// not happen (it would detach a real subprocess), but we can prove the
// pre-condition is honored: the existing live session is unchanged.
func TestMaybeSpawnIdleWatcherSkipsWhenSessionsLive(t *testing.T) {
	withIdleTestHome(t)
	if err := Register(os.Getpid(), "test-sandbox"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	defer Unregister(os.Getpid())

	cfg := &config.Config{Daemon: config.DaemonConfig{IdleStopMinutes: 5}}
	MaybeSpawnIdleWatcher(cfg)
	// Live session still present; nothing should have changed.
	if got := LiveSessionCount(); got != 1 {
		t.Errorf("LiveSessionCount = %d, want 1 (live session preserved)", got)
	}
}

// TestIdleWatchRunDisabledByZero: minutes <= 0 exits immediately without
// touching the daemon or the registry.
func TestIdleWatchRunDisabledByZero(t *testing.T) {
	withIdleTestHome(t)
	IdleWatchRun(0)
	IdleWatchRun(-5)
	// No session created; nothing to assert beyond "did not panic or hang".
}

// TestIdleWatchRunStandsDownWhenSessionLive: a live session in the
// registry makes IdleWatchRun stand down immediately. The watch loop
// rechecks every 30s; the very first check should fire and return.
func TestIdleWatchRunStandsDownWhenSessionLive(t *testing.T) {
	withIdleTestHome(t)
	if err := Register(os.Getpid(), "test-sandbox"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	defer Unregister(os.Getpid())

	// minutes is irrelevant; the live-session recheck exits before the
	// first 30s tick. Use a long minutes to assert the early return.
	start := time.Now()
	IdleWatchRun(60)
	elapsed := time.Since(start)
	// Should return well under the 30s tick. 5s is a generous bound that
	// still flags a hang (>30s) without flaking on a slow CI.
	if elapsed > 5*time.Second {
		t.Errorf("IdleWatchRun took %v with a live session; should stand down in <5s", elapsed)
	}
}

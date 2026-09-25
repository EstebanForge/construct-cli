package runtime

import (
	"errors"
	"os"
	"strings"
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

// TestStopDaemonForUpdate walks the post-update daemon decision: absent or
// stopped daemons need nothing, idle running daemons get stopped, live
// sessions defer to the user, and a stand-down race reports busy.
func TestStopDaemonForUpdate(t *testing.T) {
	origRunning, origSessions, origStop := updateDaemonRunning, updateDaemonSessions, updateDaemonStop
	t.Cleanup(func() {
		updateDaemonRunning, updateDaemonSessions, updateDaemonStop = origRunning, origSessions, origStop
	})

	cases := []struct {
		name       string
		running    []bool // consumed per probe: entry check, then post-stop re-probe
		sessions   int
		stopErr    error
		wantStop   bool
		wantBusy   bool
		wantErr    bool
		wantStopOp bool // was the stop actually invoked?
	}{
		{"absent daemon does nothing", []bool{false}, 0, nil, false, false, false, false},
		{"busy daemon is left alone", []bool{true}, 2, nil, false, true, false, false},
		{"idle daemon is stopped", []bool{true, false}, 0, nil, true, false, false, true},
		{"stop error surfaces", []bool{true}, 0, errors.New("boom"), false, false, true, true},
		{"stand-down race reports busy", []bool{true, true}, 0, nil, false, true, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			probe := 0
			updateDaemonRunning = func() bool {
				if probe >= len(tc.running) {
					return false
				}
				v := tc.running[probe]
				probe++
				return v
			}
			updateDaemonSessions = func() int { return tc.sessions }
			stopCalled := false
			updateDaemonStop = func() error { stopCalled = true; return tc.stopErr }

			stopped, busy, err := StopDaemonForUpdate()
			if (err != nil) != tc.wantErr {
				t.Errorf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if stopped != tc.wantStop || busy != tc.wantBusy {
				t.Errorf("got (stopped=%t busy=%t), want (stopped=%t busy=%t)", stopped, busy, tc.wantStop, tc.wantBusy)
			}
			if stopCalled != tc.wantStopOp {
				t.Errorf("stop invoked = %t, want %t", stopCalled, tc.wantStopOp)
			}
		})
	}
}

// TestDestroyMsbDaemon: the recreate verb's destroy primitive refuses
// busy daemons, no-ops on an absent sandbox, surfaces stop/remove errors,
// and orders the session re-check between stop and remove so a racing
// session finds a stopped daemon instead of a deleted one.
func TestDestroyMsbDaemon(t *testing.T) {
	originalSessions := destroyDaemonSessions
	originalStop := destroyDaemonStop
	originalRemove := destroyDaemonRemove
	t.Cleanup(func() {
		destroyDaemonSessions = originalSessions
		destroyDaemonStop = originalStop
		destroyDaemonRemove = originalRemove
	})

	t.Run("busy before stop refuses without touching the sandbox", func(t *testing.T) {
		destroyDaemonSessions = func() int { return 2 }
		destroyDaemonStop = func() (bool, error) {
			t.Error("stop must not run while sessions are live")
			return true, nil
		}
		destroyed, busy, err := DestroyMsbDaemon()
		if err != nil || !busy || destroyed {
			t.Fatalf("got destroyed=%v busy=%v err=%v, want busy", destroyed, busy, err)
		}
	})

	t.Run("absent sandbox reports nothing destroyed", func(t *testing.T) {
		destroyDaemonSessions = func() int { return 0 }
		destroyDaemonStop = func() (bool, error) { return false, nil }
		destroyDaemonRemove = func() error {
			t.Error("remove must not run when no sandbox exists")
			return nil
		}
		destroyed, busy, err := DestroyMsbDaemon()
		if err != nil || busy || destroyed {
			t.Fatalf("got destroyed=%v busy=%v err=%v, want nothing destroyed", destroyed, busy, err)
		}
	})

	t.Run("idle daemon is stopped then removed", func(t *testing.T) {
		destroyDaemonSessions = func() int { return 0 }
		stopCalled := false
		destroyDaemonStop = func() (bool, error) { stopCalled = true; return true, nil }
		destroyDaemonRemove = func() error { return nil }
		destroyed, busy, err := DestroyMsbDaemon()
		if err != nil || busy || !destroyed || !stopCalled {
			t.Fatalf("got destroyed=%v busy=%v err=%v stopCalled=%v", destroyed, busy, err, stopCalled)
		}
	})

	t.Run("session racing between stop and remove keeps the root disk", func(t *testing.T) {
		calls := 0
		destroyDaemonSessions = func() int {
			calls++
			if calls == 1 {
				return 0 // pre-stop check passes
			}
			return 1 // session appeared while the stop ran
		}
		destroyDaemonStop = func() (bool, error) { return true, nil }
		destroyDaemonRemove = func() error {
			t.Error("remove must not run after a session raced in")
			return nil
		}
		destroyed, busy, err := DestroyMsbDaemon()
		if err != nil || !busy || destroyed {
			t.Fatalf("got destroyed=%v busy=%v err=%v, want busy with disk intact", destroyed, busy, err)
		}
	})

	t.Run("stop and remove errors surface", func(t *testing.T) {
		destroyDaemonSessions = func() int { return 0 }
		destroyDaemonStop = func() (bool, error) { return true, errors.New("stop boom") }
		if _, _, err := DestroyMsbDaemon(); err == nil || !strings.Contains(err.Error(), "stop boom") {
			t.Fatalf("stop error = %v, want stop boom", err)
		}
		destroyDaemonStop = func() (bool, error) { return true, nil }
		destroyDaemonRemove = func() error { return errors.New("remove boom") }
		if _, _, err := DestroyMsbDaemon(); err == nil || !strings.Contains(err.Error(), "remove boom") {
			t.Fatalf("remove error = %v, want remove boom", err)
		}
	})
}

package agent

import (
	"testing"

	"github.com/EstebanForge/construct-cli/internal/config"
)

// TestTeardownIdleWatcherGatedOnRegistration: Teardown arms the idle
// watcher only for runs that joined the live-session registry. Failed-
// early runs (declined cwd, boot error) never registered and must not
// spawn a watcher for a daemon they never held.
func TestTeardownIdleWatcherGatedOnRegistration(t *testing.T) {
	orig := teardownSpawnIdleWatcher
	t.Cleanup(func() { teardownSpawnIdleWatcher = orig })

	spawned := 0
	teardownSpawnIdleWatcher = func(*config.Config) { spawned++ }

	e := &RuntimeEngine{cwd: t.TempDir()}
	e.Teardown()
	if spawned != 0 {
		t.Fatalf("unregistered run spawned the idle watcher %d time(s)", spawned)
	}

	e.registeredSession = true
	e.Teardown()
	if spawned != 1 {
		t.Fatalf("registered run did not arm the idle watcher (spawned=%d)", spawned)
	}
}

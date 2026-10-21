package runtime

import (
	"context"
	"errors"
	"os"
	"syscall"
	"testing"

	msb "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/EstebanForge/construct-cli/internal/config"
)

// stubEnsureMsbDaemonCore swaps the daemon ensure core for the duration
// of a test; restore belongs in a defer.
func stubEnsureMsbDaemonCore(core func(ctx context.Context, cfg *config.Config, projectDir string) (*msb.Sandbox, error)) (restore func()) {
	prev := ensureMsbDaemonCore
	ensureMsbDaemonCore = core
	return func() { ensureMsbDaemonCore = prev }
}

// TestEnsureMsbDaemonAndRegisterRegistersUnderLock pins the
// register-vs-idle-watch race closure: registration must happen INSIDE
// the daemon flock section, so an idle watcher that acquires the lock
// afterwards already sees the session and stands down instead of
// stopping the daemon under the incoming run.
func TestEnsureMsbDaemonAndRegisterRegistersUnderLock(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(config.GetConfigDir(), 0o700); err != nil {
		t.Fatal(err)
	}

	var lockHeldDuringEnsure bool
	restore := stubEnsureMsbDaemonCore(func(_ context.Context, _ *config.Config, _ string) (*msb.Sandbox, error) {
		fd, err := syscall.Open(DaemonLockPath(), syscall.O_RDONLY, 0)
		if err != nil {
			t.Errorf("open lock file: %v", err)
			return &msb.Sandbox{}, nil
		}
		defer syscall.Close(fd)
		if err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			if errors.Is(err, syscall.EWOULDBLOCK) {
				lockHeldDuringEnsure = true
			}
		} else {
			_ = syscall.Flock(fd, syscall.LOCK_UN)
		}
		return &msb.Sandbox{}, nil
	})
	defer restore()

	cfg := config.DefaultConfig()
	if _, err := EnsureMsbDaemonAndRegister(context.Background(), &cfg, t.TempDir()); err != nil {
		t.Fatalf("EnsureMsbDaemonAndRegister: %v", err)
	}
	if !lockHeldDuringEnsure {
		t.Error("ensure core ran without the daemon flock held; a watcher could stop the daemon between ensure and register")
	}
	sessions, err := ActiveSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].PID != os.Getpid() {
		t.Fatalf("sessions %+v, want exactly the calling pid registered", sessions)
	}
}

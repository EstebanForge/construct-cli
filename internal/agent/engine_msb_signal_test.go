package agent

import (
	"os"
	"syscall"
	"testing"
	"time"
)

// TestSignalRunContextCancelsOnSignals: the msb exec path's run context
// must cancel on SIGINT and SIGTERM so the deferred Teardown in
// runWithProviderEnv still runs after a Ctrl-C or kill (unregister,
// copy-back, bridge stops) instead of the process dying mid-run.
func TestSignalRunContextCancelsOnSignals(t *testing.T) {
	ctx, stop := signalRunContext()
	defer stop()

	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("send SIGTERM: %v", err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatalf("SIGTERM did not cancel the run context")
	}

	// A fresh context must also react to SIGINT: stop() restored the
	// default disposition, so the new Notify replaces it.
	ctx2, stop2 := signalRunContext()
	defer stop2()
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatalf("send SIGINT: %v", err)
	}
	select {
	case <-ctx2.Done():
	case <-time.After(2 * time.Second):
		t.Fatalf("SIGINT did not cancel the run context")
	}
}

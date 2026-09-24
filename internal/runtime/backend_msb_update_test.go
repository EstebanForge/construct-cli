package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EstebanForge/construct-cli/internal/config"
)

// stubAbandonExit swaps the watchdog termination seam for a signal and
// registers the restore. Returns the channel the exit code arrives on.
func stubAbandonExit(t *testing.T) <-chan int {
	t.Helper()
	exited := make(chan int, 1)
	orig := updateAbandonExit
	updateAbandonExit = func(code int) { exited <- code }
	t.Cleanup(func() { updateAbandonExit = orig })
	return exited
}

func watchdogLog(t *testing.T) *os.File {
	t.Helper()
	lf, err := os.Create(filepath.Join(t.TempDir(), "update.log"))
	if err != nil {
		t.Fatalf("create log: %v", err)
	}
	t.Cleanup(func() { _ = lf.Close() })
	return lf
}

// Telemetry disabled so the fire path writes nothing outside the test.
func watchdogCfg() *config.Config {
	cfg := &config.Config{}
	cfg.Runtime.Telemetry = false
	return cfg
}

func TestUpdateAbandonWatchdogFires(t *testing.T) {
	lf := watchdogLog(t)
	exited := stubAbandonExit(t)

	start := time.Now()
	armUpdateAbandonWatchdog(watchdogCfg(), "test", start, lf, 30*time.Millisecond)

	select {
	case code := <-exited:
		if code != 1 {
			t.Fatalf("exit code = %d, want 1", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("watchdog did not fire within 2s")
	}

	data, err := os.ReadFile(lf.Name())
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	line := string(data)
	if !strings.Contains(line, "update abandoned") || !strings.Contains(line, "duration=") {
		t.Fatalf("abandon line missing or malformed: %q", line)
	}
}

func TestUpdateAbandonWatchdogDisarmPreventsExit(t *testing.T) {
	lf := watchdogLog(t)
	exited := stubAbandonExit(t)

	disarm := armUpdateAbandonWatchdog(watchdogCfg(), "test", time.Now(), lf, 30*time.Millisecond)
	disarm()

	select {
	case code := <-exited:
		t.Fatalf("disarmed watchdog still exited with code %d", code)
	case <-time.After(200 * time.Millisecond):
	}
}

package runtime

// Idle-window package updater (docs/TODO.md "Idle-Window Package
// Updater"). When the daemon's idle watcher fires with zero sessions, the
// optional-layer packages (update-all.sh) are refreshed BEFORE the daemon
// stops. Contract from the design + peer review:
//   - NO daemon flock during the pass (holding it would block
//     EnsureMsbDaemon for the whole timeout on any incoming session).
//   - LiveSessionCount is polled during the pass; a session appearing
//     cancels the pass (npm re-runs clean next window) and the caller
//     re-makes the stop decision under the flock.
//   - Bounded by a hard timeout; failures are best-effort and retried at
//     the next idle window.
// The baked baseline is NEVER updated in-guest: it moves on the image
// lane. update-all.sh is the single entry point (it already wraps the
// generated topgrade run).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/EstebanForge/construct-cli/internal/config"
	"github.com/EstebanForge/construct-cli/internal/ui"
)

// updatePassTimeout bounds the whole update pass.
const updatePassTimeout = 30 * time.Minute

// ErrUpdateStoodDown reports the pass was canceled because a session
// appeared. Callers must NOT stop the daemon in this case: real work just
// arrived and deserves the running daemon.
var ErrUpdateStoodDown = errors.New("update stood down: session appeared")

// acquireUpdateLock takes a non-blocking exclusive lock on update.lock so
// two idle windows (or a manual + automatic pass) cannot run concurrent
// update-all.sh invocations against the same sandbox. The lock lives in
// the config dir and is held for the duration of the pass.
func acquireUpdateLock() (*os.File, error) {
	p := filepath.Join(config.GetConfigDir(), "update.lock")
	f, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close() //nolint:errcheck // best-effort close on lock failure
		return nil, fmt.Errorf("another update pass is running: %w (lock: %s; holder is likely the idle-window updater or a concurrent 'construct sys update'; see update.log next to this lock; passes are bounded to 30 minutes)", err, p)
	}
	return f, nil
}

// updateAbandonGrace extends updatePassTimeout into the hard process
// bound below. The exec-level ctx normally ends the pass, but the SDK's
// post-cancel drain (h.Kill + Recv on a background ctx) has been
// observed to hang indefinitely, wedging the whole process while it
// still holds update.lock and burning CPU inside the FFI library. The
// watchdog below is the only bound that survives that state.
const updateAbandonGrace = 2 * time.Minute

// updateAbandonExit is the termination seam; a var so tests can observe
// the fire path without killing the test process.
var updateAbandonExit = os.Exit

// armUpdateAbandonWatchdog bounds one update pass at the process level.
// If the pass is still running after bound (exec stuck in a blocking FFI
// call, drain never returning), the watchdog logs "update abandoned",
// records telemetry, prints a stderr notice, and exits the process:
// flock release happens at process death and the orphaned goroutines die
// with it. Go timers run on runtime threads, which stay schedulable even
// when user goroutines are parked in cgo. The caller must defer the
// returned disarm on every normal return path.
func armUpdateAbandonWatchdog(cfg *config.Config, source string, started time.Time, lf *os.File, bound time.Duration) (disarm func()) {
	var ended atomic.Bool
	t := time.AfterFunc(bound, func() {
		if ended.Load() {
			return
		}
		seconds := int(time.Since(started).Seconds())
		if lf != nil {
			//nolint:errcheck // best-effort log line during teardown
			fmt.Fprintf(lf, "%s update abandoned: pass exceeded the %s hard bound; exiting to release update.lock (duration=%ds)\n",
				time.Now().UTC().Format(time.RFC3339), bound, seconds)
		}
		updateTelemetry(cfg, "abandoned", seconds, source)
		ui.InfoF("⛔ update abandoned: the pass did not return within %s; releasing update.lock by exiting.\n", bound)
		updateAbandonExit(1)
	})
	return func() {
		ended.Store(true)
		t.Stop()
	}
}

// IdleWindowUpdate runs update-all.sh inside the daemon sandbox. It is
// the sole update entry point: the script already invokes the generated
// topgrade config internally.
func IdleWindowUpdate(cfg *config.Config) error {
	start := time.Now()

	logDir := config.GetLogsDir()
	if logDir == "" {
		logDir = filepath.Join(config.GetConfigDir(), "logs")
	}
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return fmt.Errorf("update log dir: %w", err)
	}
	lf, err := os.OpenFile(filepath.Join(logDir, "update.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open update log: %w", err)
	}
	defer lf.Close() //nolint:errcheck // best-effort log close
	logLine := func(format string, args ...interface{}) {
		ts := time.Now().UTC().Format(time.RFC3339)
		//nolint:errcheck // best-effort log line
		fmt.Fprintf(lf, "%s %s\n", ts, fmt.Sprintf(format, args...))
	}

	// Single-pass guard (design: dedicated update state).
	lockF, err := acquireUpdateLock()
	if err != nil {
		logLine("update skipped: %v", err)
		return nil
	}
	defer func() {
		_ = syscall.Flock(int(lockF.Fd()), syscall.LOCK_UN) //nolint:errcheck // best-effort unlock
		_ = lockF.Close()                                   //nolint:errcheck // best-effort close
	}()
	disarm := armUpdateAbandonWatchdog(cfg, "idle", start, lf, updatePassTimeout+updateAbandonGrace)
	defer disarm()

	// Zero-session precondition (the caller also checked; re-verify).
	if LiveSessionCount() > 0 {
		logLine("update skipped: sessions appeared before start")
		return nil
	}

	logLine("update started")
	// Refresh the helper the pass is about to execute: msb has no per-file
	// binds, so update-all.sh is read from the home volume and may be stale.
	// Best-effort for the idle path: a failed refresh skips this window and
	// retries on the next one rather than running brew-era logic.
	if err := EnsureMountedTemplateFiles(config.GetConfigDir()); err != nil {
		logLine("update skipped: refresh update helpers: %v", err)
		return nil
	}
	// The pass runs WITHOUT the daemon flock by design. The exec targets
	// the running daemon sandbox; additive installs coexist with sessions.
	ctx, cancel := context.WithTimeout(context.Background(), updatePassTimeout)
	defer cancel()

	m := NewMsbBackend()
	type execResult struct {
		code int
		err  error
	}
	done := make(chan execResult, 1)
	go func() {
		code, eerr := m.ExecStream(ctx, ExecOptions{
			Name:    msbDaemonName,
			Command: []string{"bash", "/home/construct/.config/construct-cli/container/update-all.sh"},
		})
		done <- execResult{code: code, err: eerr}
	}()

	// Stand-down poll: a session appearing mid-pass cancels the pass.
	var res execResult
	standDown := false
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	waiting := true
	for waiting {
		select {
		case res = <-done:
			waiting = false
		case <-tick.C:
			if LiveSessionCount() > 0 {
				standDown = true
				cancel()
				res = <-done
				waiting = false
			}
		}
	}

	outcome := "ok"
	if standDown {
		outcome = "stood-down"
		logLine("update stood down: a session appeared")
	} else if res.err != nil {
		outcome = "failed"
	}
	seconds := int(time.Since(start).Seconds())
	logLine("update %s (duration=%ds)", outcome, seconds)
	updateTelemetry(cfg, outcome, seconds, "idle")

	if standDown {
		return ErrUpdateStoodDown
	}
	return res.err
}

// RunForegroundUpdate is the manual `construct sys update` path for the
// microvm backend: it brings the daemon sandbox up if needed and runs
// update-all.sh with output streamed to the terminal (teeed into
// update.log, same as the idle-window pass). Unlike the idle path there is
// no stand-down and no zero-session precondition: the user asked for the
// update, and additive installs coexist with live sessions. The
// update.lock guard is shared with the idle watcher; contention surfaces
// as an error instead of a silent skip.
func RunForegroundUpdate(cfg *config.Config) error {
	start := time.Now()

	lockF, err := acquireUpdateLock()
	if err != nil {
		return err
	}
	defer func() {
		_ = syscall.Flock(int(lockF.Fd()), syscall.LOCK_UN) //nolint:errcheck // best-effort unlock
		_ = lockF.Close()                                   //nolint:errcheck // best-effort close
	}()

	logDir := config.GetLogsDir()
	if logDir == "" {
		logDir = filepath.Join(config.GetConfigDir(), "logs")
	}
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return fmt.Errorf("update log dir: %w", err)
	}
	lf, err := os.OpenFile(filepath.Join(logDir, "update.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open update log: %w", err)
	}
	defer lf.Close() //nolint:errcheck // best-effort log close
	logLine := func(format string, args ...interface{}) {
		ts := time.Now().UTC().Format(time.RFC3339)
		//nolint:errcheck // best-effort log line
		fmt.Fprintf(lf, "%s %s\n", ts, fmt.Sprintf(format, args...))
	}
	disarm := armUpdateAbandonWatchdog(cfg, "manual", start, lf, updatePassTimeout+updateAbandonGrace)
	defer disarm()

	// Refresh the helper the pass is about to execute: msb has no per-file
	// binds, so update-all.sh is read from the home volume and may be stale.
	if err := EnsureMountedTemplateFiles(config.GetConfigDir()); err != nil {
		return fmt.Errorf("refresh update helpers: %w", err)
	}

	// projectDir is deliberately empty: the update pass is workspace-
	// independent (it only execs update-all.sh in the guest), so neither
	// the multi-path unmapped-cwd rejection nor single-path root learning
	// (which can consent-and-recreate a running daemon) may trigger.
	ctx, cancel := context.WithTimeout(context.Background(), updatePassTimeout)
	defer cancel()
	sb, err := EnsureMsbDaemon(ctx, cfg, "")
	if err != nil {
		return fmt.Errorf("prepare microvm sandbox: %w", err)
	}
	// ExecStream opens its own connection; release this one.
	_ = sb.Detach(context.Background()) //nolint:errcheck // best-effort detach

	fmt.Println("Updating agents and packages inside the microVM sandbox...")
	logLine("manual update started")

	m := NewMsbBackend()
	code, execErr := m.ExecStream(ctx, ExecOptions{
		Name:    msbDaemonName,
		Command: []string{"bash", "/home/construct/.config/construct-cli/container/update-all.sh"},
		Stdout:  io.MultiWriter(os.Stdout, lf),
		Stderr:  io.MultiWriter(os.Stderr, lf),
	})

	outcome := "ok"
	if execErr != nil || code != 0 {
		outcome = "failed"
	}
	seconds := int(time.Since(start).Seconds())
	logLine("update %s (duration=%ds, source=manual)", outcome, seconds)
	updateTelemetry(cfg, outcome, seconds, "manual")

	fmt.Printf("Update log: %s\n", filepath.Join(logDir, "update.log"))
	if execErr != nil {
		return execErr
	}
	if code != 0 {
		return fmt.Errorf("update script exited %d (see update.log)", code)
	}
	return nil
}

// updateTelemetry appends one canonical update event to
// logs/update-telemetry.jsonl (best-effort; respects [runtime].telemetry).
func updateTelemetry(cfg *config.Config, outcome string, seconds int, source string) {
	if cfg != nil && !cfg.Runtime.Telemetry {
		return
	}
	logDir := config.GetLogsDir()
	if logDir == "" {
		return
	}
	ev := map[string]interface{}{
		"time":     time.Now().UTC().Format(time.RFC3339),
		"event":    "update",
		"outcome":  outcome,
		"duration": seconds,
		"source":   source,
	}
	data, err := json.Marshal(ev)
	if err != nil {
		return
	}
	p := filepath.Join(logDir, "update-telemetry.jsonl")
	f, ferr := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if ferr != nil {
		return
	}
	//nolint:errcheck // telemetry is best-effort
	_, _ = f.Write(append(data, '\n'))
	//nolint:errcheck // telemetry is best-effort
	_ = f.Close()
}

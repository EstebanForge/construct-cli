package runtime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	msb "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/EstebanForge/construct-cli/internal/config"
	"github.com/EstebanForge/construct-cli/internal/ui"
)

// setDetachedAttrs configures cmd to run as a true daemon: new session
// (setsid) so the child outlives the parent's terminal. The caller still
// needs to close stdin/stdout/stderr and call Process.Release after
// Start to keep the watcher alive past the parent's exit.
func setDetachedAttrs(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setsid = true
}

// MaybeSpawnIdleWatcher is the "last unregister" hook. Called by the
// engine after a session is unregistered. When the session count is zero
// AND IdleStopMinutes > 0, spawn a detached idle-watch process and
// return. Best-effort: failures log a single line and do not propagate;
// the user can still stop the daemon manually.
//
// The detached process sleeps IdleStopMinutes, rechecks the registry,
// and stops the daemon only if the count is still zero. A new run
// arriving during the sleep registers a session; the watcher's recheck
// sees count > 0 and stands down without touching the daemon.
func MaybeSpawnIdleWatcher(cfg *config.Config) {
	if cfg == nil || cfg.Daemon.IdleStopMinutes <= 0 {
		return
	}
	if LiveSessionCount() > 0 {
		return
	}

	exe, err := os.Executable()
	if err != nil {
		ui.InfoF("⚠️  Could not resolve executable for idle-watch: %v\n", err)
		return
	}

	cmd := exec.Command(exe, "sys", "daemon", "idle-watch",
		"--minutes", fmt.Sprintf("%d", cfg.Daemon.IdleStopMinutes))
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	setDetachedAttrs(cmd)
	if err := cmd.Start(); err != nil {
		ui.InfoF("⚠️  Could not spawn idle-watch: %v\n", err)
		return
	}
	// Release the child so it survives; we intentionally do not Wait.
	if err := cmd.Process.Release(); err != nil {
		ui.InfoF("⚠️  Could not release idle-watch: %v\n", err)
		return
	}
	ui.InfoLn("⏳ Spawned idle-watch (daemon stops after zero sessions for the configured interval).")
}

// Seams for StopDaemonForUpdate so tests can fake daemon state.
var (
	updateDaemonRunning = func() bool {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		h, err := msb.GetSandbox(ctx, msbDaemonName)
		if err != nil {
			return false
		}
		fresh, ferr := h.Refresh(ctx)
		if ferr != nil {
			return false
		}
		return fresh.Status() == msb.SandboxStatusRunning
	}
	updateDaemonSessions = LiveSessionCount
	updateDaemonStop     = StopMsbDaemonBestEffort
)

// Seams for DestroyMsbDaemon so tests can fake daemon state.
var (
	destroyDaemonSessions = LiveSessionCount
	destroyDaemonStop     = stopDaemonForDestroy
	destroyDaemonRemove   = removeDaemonSandbox
)

// stopDaemonForDestroy stops the daemon sandbox and waits for stopped.
// Runs WITHOUT the flock and session checks — DestroyMsbDaemon owns
// those. Returns found=false when no sandbox record exists.
func stopDaemonForDestroy() (found bool, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	h, err := msb.GetSandbox(ctx, msbDaemonName)
	if err != nil {
		return false, nil // no sandbox record: nothing to destroy
	}
	if err := h.Stop(ctx); err != nil {
		return true, fmt.Errorf("failed to stop microvm daemon: %w", err)
	}
	for i := 0; i < 30; i++ {
		fresh, ferr := h.Refresh(ctx)
		if ferr != nil || fresh.Status() == msb.SandboxStatusStopped {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	return true, nil
}

// removeDaemonSandbox deletes the daemon sandbox record with its guest
// root disk (the `msb rm` half of a recreate).
func removeDaemonSandbox() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	h, err := msb.GetSandbox(ctx, msbDaemonName)
	if err != nil {
		return nil // record vanished between stop and remove: done
	}
	if err := h.Remove(ctx); err != nil {
		return fmt.Errorf("failed to remove microvm daemon sandbox: %w", err)
	}
	return nil
}

// DestroyMsbDaemon stops the daemon sandbox and removes it with its guest
// root disk — the deliberate full reset behind `construct sys daemon
// recreate`. The whole sequence holds the daemon flock so a concurrent
// EnsureMsbDaemon cannot recreate the sandbox between stop and remove.
// Returns (destroyed, busy, error): destroyed=false means no sandbox
// existed; busy=true means live sessions hold the daemon and the root
// disk was left intact (the daemon may have been stopped).
func DestroyMsbDaemon() (destroyed, busy bool, err error) {
	release, err := acquireDaemonLock()
	if err != nil {
		return false, false, fmt.Errorf("acquire daemon lock: %w", err)
	}
	defer release()

	if destroyDaemonSessions() > 0 {
		return false, true, nil
	}
	found, err := destroyDaemonStop()
	if err != nil || !found {
		return false, false, err
	}
	// The session re-check between stop and remove is the real guard:
	// removing the root disk under a live session is the one outcome to
	// refuse. A session that raced the lock window finds a stopped
	// daemon instead of a deleted one.
	if destroyDaemonSessions() > 0 {
		return false, true, nil
	}
	if err := destroyDaemonRemove(); err != nil {
		return false, false, err
	}
	return true, false, nil
}

// StopDaemonForUpdate arms the post-update rebuild. Sandbox setup (sudoers
// policy, mounts, skills) applies at build time only, so a daemon that was
// running during a self-update keeps the old setup until it is rebuilt.
// Stops the daemon when it is running idle; reports busy when live sessions
// hold it. An absent or already-stopped daemon needs nothing: the next ct
// command rebuilds (stale labels) or boots normally. Returns (stopped,
// busy, error).
func StopDaemonForUpdate() (stopped, busy bool, err error) {
	if !updateDaemonRunning() {
		return false, false, nil
	}
	if updateDaemonSessions() > 0 {
		return false, true, nil
	}
	if err := updateDaemonStop(); err != nil {
		return false, false, err
	}
	// StopMsbDaemonBestEffort stands down (returns nil) when a session
	// appeared under the flock; re-probe so the user gets honest output.
	if updateDaemonRunning() {
		return false, true, nil
	}
	return true, false, nil
}

// LiveSessionCount returns the number of currently live sessions.
func LiveSessionCount() int {
	sessions, err := ActiveSessions()
	if err != nil {
		return 0
	}
	return len(sessions)
}

// IdleWatchRun is the body of `construct sys daemon idle-watch`. Sleeps
// for the configured minutes (rechecked every 30s) and stops the daemon
// if and only if the session count is still zero at the deadline. The
// recheck is the core guarantee: a new ct invocation that registered a
// session during the sleep stands the watcher down.
//
// Invoked as a detached process. Writes its own log line on every
// decision so a user investigating "why did the daemon stop" can trace
// the watcher's reasoning via the construct log dir.
func IdleWatchRun(minutes int) {
	if minutes <= 0 {
		ui.InfoLn("idle-watch: minutes <= 0, exiting")
		return
	}
	ui.InfoF("⏳ idle-watch armed: %d minutes; checking every 30s\n", minutes)
	deadline := time.Now().Add(time.Duration(minutes) * time.Minute)
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	for {
		if LiveSessionCount() > 0 {
			ui.InfoLn("idle-watch: a session appeared, standing down")
			return
		}
		if time.Now().After(deadline) {
			break
		}
		<-tick.C
	}
	// Final check before stopping: a session may have appeared in the
	// Last 30s window.
	if LiveSessionCount() > 0 {
		ui.InfoLn("idle-watch: a session appeared in the final window, standing down")
		return
	}

	// Idle-window package update: refresh the optional layer before the
	// stop (docs/TODO.md "Idle-Window Package Updater"). Runs WITHOUT the
	// daemon flock; polls sessions and stands down if work arrives. The
	// stop below re-checks under the flock, so a session that appeared
	// mid-update keeps the daemon. Config is loaded here because the
	// idle-watch is a detached process (no cfg is passed in).
	if cfg, _, cerr := config.Load(); cerr == nil && cfg != nil && cfg.Daemon.AutoUpdatePackages {
		ui.InfoLn("⏳ idle-watch: running package updates before stop")
		if uerr := IdleWindowUpdate(cfg); uerr != nil {
			if errors.Is(uerr, ErrUpdateStoodDown) {
				// A session appeared: real work arrived. Do NOT stop the
				// daemon - the next last-unregister re-arms this watcher.
				ui.InfoLn("idle-watch: update stood down for a new session; daemon stays up")
				return
			}
			ui.InfoF("⚠️ idle-watch: update pass failed (retried next window): %v\n", uerr)
		}
		if LiveSessionCount() > 0 {
			ui.InfoLn("idle-watch: a session appeared during the update, standing down")
			return
		}
	} else if cerr != nil {
		ui.InfoF("⚠️ idle-watch: config load failed, skipping package update: %v\n", cerr)
	}

	ui.InfoLn("💤 idle-watch: zero sessions past the deadline; stopping the daemon")
	if err := StopMsbDaemonBestEffort(); err != nil {
		ui.InfoF("⚠️  idle-watch: stop failed: %v\n", err)
	}
}

// StopMsbDaemonBestEffort stops the msb daemon with a 30s budget. Mirrors
// the stop path in internal/daemon/daemon.go so the watcher can stop the
// daemon from a detached process without an import cycle (daemon imports
// runtime, not the other way around). Best-effort: if stop fails the
// user can still run `construct sys daemon stop` manually.
//
// Acquires the daemon flock (phase 1) before stopping so a concurrent ct
// invocation's EnsureMsbDaemon cannot be torn down mid-flight, and so two
// concurrent watchers serialize on the same lock. The flock is per-process
// on the same file description; distinct processes contending on the
// same file path DO block, which is the property we want.
func StopMsbDaemonBestEffort() error {
	release, err := acquireDaemonLock()
	if err != nil {
		return fmt.Errorf("acquire daemon lock: %w", err)
	}
	defer release()

	// Re-check under the lock: a new session may have registered between
	// the watcher's last tick and now. If so, stand down — the
	// previously-live session deserves a daemon.
	if LiveSessionCount() > 0 {
		ui.InfoLn("idle-watch: a session appeared under the lock, standing down")
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	h, err := msb.GetSandbox(ctx, msbDaemonName)
	if err != nil {
		return fmt.Errorf("daemon is not running: %w", err)
	}
	if err := h.Stop(ctx); err != nil {
		return fmt.Errorf("failed to stop microvm daemon: %w", err)
	}
	for i := 0; i < 30; i++ {
		fresh, ferr := h.Refresh(ctx)
		if ferr != nil || fresh.Status() == msb.SandboxStatusStopped {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	return nil
}

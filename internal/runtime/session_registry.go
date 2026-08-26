package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/EstebanForge/construct-cli/internal/config"
)

// Session is one live run of construct that has the daemon in use. The
// file lives at ~/.config/construct-cli/sessions/<pid>.json so each PID
// has a unique file (no lock needed across PIDs in the same process tree
// — only the current PID ever writes). Stale files (pid not alive) are
// swept on every Register and on demand.
//
// Used by phase 3 (idle stop) to know when zero sessions are live and the
// daemon can be safely stopped.
type Session struct {
	PID       int       `json:"pid"`
	Sandbox   string    `json:"sandbox"`
	StartedAt time.Time `json:"started_at"`
}

// sessionsDirName is the directory inside the construct config dir that
// holds per-pid session files.
const sessionsDirName = "sessions"

// sessionFileName returns the per-pid session file path.
func sessionFileName(pid int) string {
	return filepath.Join(config.GetConfigDir(), sessionsDirName, fmt.Sprintf("%d.json", pid))
}

// Register writes a session file for pid. Creates the sessions dir on
// demand (mode 0700 — only the construct user can list it). Sweeps stale
// entries first so dead siblings do not pile up over time.
func Register(pid int, sandbox string) error {
	if err := os.MkdirAll(filepath.Join(config.GetConfigDir(), sessionsDirName), 0o700); err != nil {
		return err
	}
	// Best-effort sweep; failure here is not fatal (the new write still
	// succeeds and the next Register will retry the sweep).
	//nolint:errcheck // best-effort; sweep failure is non-fatal here
	_, _ = SweepStaleSessions()
	s := Session{PID: pid, Sandbox: sandbox, StartedAt: time.Now()}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(sessionFileName(pid), data, 0o600)
}

// Unregister removes the session file for pid. Missing file is not an
// error: a Teardown that runs after an external kill (or after the sweep
// already removed the file) is still a clean teardown.
func Unregister(pid int) error {
	err := os.Remove(sessionFileName(pid))
	if err == nil || os.IsNotExist(err) {
		return nil
	}
	return err
}

// ActiveSessions returns the live (pid still alive) sessions, oldest first.
// The caller uses this to drive "last unregister" logic in phase 3.
func ActiveSessions() ([]Session, error) {
	dir := filepath.Join(config.GetConfigDir(), sessionsDirName)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]Session, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		var s Session
		data, rerr := os.ReadFile(filepath.Join(dir, e.Name()))
		if rerr != nil {
			continue
		}
		if jerr := json.Unmarshal(data, &s); jerr != nil {
			continue
		}
		if !pidAlive(s.PID) {
			continue
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.Before(out[j].StartedAt) })
	return out, nil
}

// SweepStaleSessions removes session files whose PID is no longer alive.
// Returns the count of removed files. Safe to call concurrently with
// Register: Register writes a fresh file for a known live PID, and the
// sweep only touches files whose PID is gone. Two processes calling this
// at the same time is fine; the worst case is a duplicate os.Remove call
// returning ENOENT (ignored).
func SweepStaleSessions() (int, error) {
	dir := filepath.Join(config.GetConfigDir(), sessionsDirName)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	removed := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		var s Session
		data, rerr := os.ReadFile(filepath.Join(dir, e.Name()))
		if rerr != nil {
			continue
		}
		if jerr := json.Unmarshal(data, &s); jerr != nil {
			continue
		}
		if pidAlive(s.PID) {
			continue
		}
		if rerr := os.Remove(filepath.Join(dir, e.Name())); rerr == nil {
			removed++
		}
	}
	return removed, nil
}

// pidAlive reports whether pid is still running. On Linux signal 0 is the
// standard no-op probe; errors other than ESRCH mean "still alive" (EPERM
// is the common one — process exists but is owned by another user).
func pidAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	// permission denied -> process exists, owned by another user
	if errors.Is(err, syscall.EPERM) {
		return true
	}
	return false
}

package runtime

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// withSessionTestHome isolates HOME so the sessions dir lands in temp.
func withSessionTestHome(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
}

// TestSessionRegisterUnregisterRoundTrip: register then unregister
// produces an empty sessions dir.
func TestSessionRegisterUnregisterRoundTrip(t *testing.T) {
	withSessionTestHome(t)
	pid := os.Getpid()
	if err := Register(pid, "test-sandbox"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	// File should exist.
	if _, err := os.Stat(sessionFileName(pid)); err != nil {
		t.Fatalf("session file missing after Register: %v", err)
	}
	if err := Unregister(pid); err != nil {
		t.Fatalf("Unregister: %v", err)
	}
	if _, err := os.Stat(sessionFileName(pid)); !os.IsNotExist(err) {
		t.Errorf("session file should be gone after Unregister, got err=%v", err)
	}
}

// TestSessionRegisterSweepsStale: pre-existing stale entry is removed by
// Register. Uses a non-existent PID; pidAlive returns false so the sweep
// picks it up.
func TestSessionRegisterSweepsStale(t *testing.T) {
	withSessionTestHome(t)
	stalePID := 999999
	if err := os.MkdirAll(filepath.Join(os.Getenv("HOME"), ".config", "construct-cli", "sessions"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	stale := Session{PID: stalePID, Sandbox: "stale", StartedAt: time.Now().Add(-time.Hour)}
	if err := writeSessionFile(stale); err != nil {
		t.Fatalf("write stale: %v", err)
	}
	if err := Register(os.Getpid(), "live"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := os.Stat(sessionFileName(stalePID)); !os.IsNotExist(err) {
		t.Errorf("expected stale entry swept, got err=%v", err)
	}
}

// TestSessionActiveSessionsFiltersDead: only live PIDs surface in
// ActiveSessions; dead ones are ignored.
func TestSessionActiveSessionsFiltersDead(t *testing.T) {
	withSessionTestHome(t)
	live := os.Getpid()
	dead := 999998

	if err := Register(live, "live-sandbox"); err != nil {
		t.Fatalf("Register live: %v", err)
	}
	if err := writeSessionFile(Session{PID: dead, Sandbox: "dead", StartedAt: time.Now()}); err != nil {
		t.Fatalf("write dead: %v", err)
	}

	sessions, err := ActiveSessions()
	if err != nil {
		t.Fatalf("ActiveSessions: %v", err)
	}
	if len(sessions) != 1 || sessions[0].PID != live {
		t.Errorf("expected only live session, got %+v", sessions)
	}
	if LiveSessionCount() != 1 {
		t.Errorf("LiveSessionCount = %d, want 1", LiveSessionCount())
	}
}

// TestSessionUnregisterMissing: unregistering a never-registered PID
// is a no-op (returns nil).
func TestSessionUnregisterMissing(t *testing.T) {
	withSessionTestHome(t)
	if err := Unregister(999997); err != nil {
		t.Errorf("Unregister of missing pid should be a no-op, got %v", err)
	}
}

// TestSessionSweepStaleReturnsCount: SweepStaleSessions returns the
// number of removed files (and leaves live files alone).
func TestSessionSweepStaleReturnsCount(t *testing.T) {
	withSessionTestHome(t)
	live := os.Getpid()
	if err := Register(live, "live"); err != nil {
		t.Fatalf("Register live: %v", err)
	}
	if err := writeSessionFile(Session{PID: 999996, Sandbox: "dead-a", StartedAt: time.Now()}); err != nil {
		t.Fatalf("write dead a: %v", err)
	}
	if err := writeSessionFile(Session{PID: 999995, Sandbox: "dead-b", StartedAt: time.Now()}); err != nil {
		t.Fatalf("write dead b: %v", err)
	}
	removed, err := SweepStaleSessions()
	if err != nil {
		t.Fatalf("SweepStaleSessions: %v", err)
	}
	if removed != 2 {
		t.Errorf("expected 2 removed, got %d", removed)
	}
	if _, err := os.Stat(sessionFileName(live)); err != nil {
		t.Errorf("live session should survive sweep, got err=%v", err)
	}
}

// writeSessionFile is a test helper that writes a session file bypassing
// Register (so tests can plant stale entries without sweeping).
func writeSessionFile(s Session) error {
	data, err := jsonMarshal(s)
	if err != nil {
		return err
	}
	return os.WriteFile(sessionFileName(s.PID), data, 0o600)
}

// jsonMarshal is split out so the test file does not import encoding/json
// directly when only one test uses it. Kept tiny: indent, no validation.
func jsonMarshal(s Session) ([]byte, error) {
	// Indent manually to avoid pulling in encoding/json for one line.
	out := []byte("{\n")
	out = append(out, "  \"pid\": "...)
	out = append(out, []byte(itoa(s.PID))...)
	out = append(out, []byte(",\n  \"sandbox\": \"")...)
	out = append(out, []byte(s.Sandbox)...)
	out = append(out, []byte("\",\n  \"started_at\": \"")...)
	out = append(out, []byte(s.StartedAt.Format(time.RFC3339))...)
	out = append(out, []byte("\"\n}")...)
	return out, nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

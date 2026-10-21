package runtime

import (
	"os"
	"sort"
	"testing"

	"pgregory.net/rapid"

	"github.com/EstebanForge/construct-cli/internal/config"
)

// sessionMachine drives the real session registry against a model under
// random register/unregister/sweep sequences. Alive pids are this
// process's pid; dead pids are negative numbers, which pidAlive always
// reports dead, so the fixture is deterministic.
type sessionMachine struct {
	alivePID int
	// registered tracks which pids currently have a session file.
	registered map[int]bool
}

func newSessionMachine(t *rapid.T) *sessionMachine {
	// Per-iteration isolated HOME: the registry derives its dir from HOME
	// on every call, so each Check iteration gets a fresh registry.
	home, err := os.MkdirTemp("", "session-property")
	if err != nil {
		t.Fatal(err)
	}
	prevHome, hadPrev := os.LookupEnv("HOME")
	if err := os.Setenv("HOME", home); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if hadPrev {
			_ = os.Setenv("HOME", prevHome)
		} else {
			_ = os.Unsetenv("HOME")
		}
		_ = os.RemoveAll(home)
	})
	if err := os.MkdirAll(config.GetConfigDir(), 0o700); err != nil {
		t.Fatalf("config dir: %v", err)
	}
	return &sessionMachine{alivePID: os.Getpid(), registered: map[int]bool{}}
}

// deadPID is guaranteed non-alive: Linux pid_max tops out at 4194304,
// so any pid above it is ESRCH by construction. Negative pids are NOT
// safe fixtures (pid -1 means "all processes" and survives the probe).
func (m *sessionMachine) deadPID(t *rapid.T) int {
	return 4194304 + rapid.IntMin(0).Draw(t, "deadPID")
}

func (m *sessionMachine) register(t *rapid.T) {
	pid := m.alivePID
	if rapid.Bool().Draw(t, "alive") == false {
		pid = m.deadPID(t)
	}
	if err := Register(pid, "sandbox"); err != nil {
		t.Fatalf("register %d: %v", pid, err)
	}
	m.registered[pid] = true
}

func (m *sessionMachine) unregister(t *rapid.T) {
	for pid := range m.registered {
		if err := Unregister(pid); err != nil {
			t.Fatalf("unregister %d: %v", pid, err)
		}
		delete(m.registered, pid)
		return
	}
	t.Skip("nothing registered")
}

func (m *sessionMachine) sweep(t *rapid.T) {
	if _, err := SweepStaleSessions(); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	for pid := range m.registered {
		if pid != m.alivePID {
			// The sweep removes dead-pid files; the model follows.
			delete(m.registered, pid)
		}
	}
}

// Check pins the invariant: ActiveSessions reports exactly the
// registered alive pids, regardless of interleaving. Dead pids never
// surface even while their files linger, and a sweep converges the
// registry on the same live set as the model.
func (m *sessionMachine) Check(t *rapid.T) {
	sessions, err := ActiveSessions()
	if err != nil {
		t.Fatalf("active sessions: %v", err)
	}
	got := make([]int, 0, len(sessions))
	for _, s := range sessions {
		got = append(got, s.PID)
	}
	want := []int{}
	if m.registered[m.alivePID] {
		want = append(want, m.alivePID)
	}
	sort.Ints(got)
	sort.Ints(want)
	if len(got) != len(want) || (len(got) > 0 && got[0] != want[0]) {
		t.Fatalf("active sessions %v, want %v (model %+v)", got, want, m.registered)
	}
}

func TestSessionRegistryProperty(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		m := newSessionMachine(t)
		// In map form the "" key is the per-action invariant check.
		t.Repeat(map[string]func(*rapid.T){
			"":           m.Check,
			"register":   m.register,
			"unregister": m.unregister,
			"sweep":      m.sweep,
		})
	})
}

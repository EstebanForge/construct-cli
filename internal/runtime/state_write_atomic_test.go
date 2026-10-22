package runtime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// TestRegisterNeverTearsSessionFile: a concurrent reader of the session
// registry must never observe a partially-written file. In-place
// os.WriteFile windows expose truncated JSON to readers, and
// ActiveSessions treats a parse failure as zero sessions — which makes
// the daemon look idle while sessions are live. Temp+rename guarantees a
// reader sees only a complete old file, ENOENT, or a complete new file.
func TestRegisterNeverTearsSessionFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	const livePid = 4194304 // above pid_max: dead by construction for the sweep
	if err := Register(livePid, "construct-cli-daemon"); err != nil {
		t.Fatalf("seed register: %v", err)
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		target := sessionFileName(livePid)
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			// Rewrite the watched file (StartedAt changes) as fast as possible.
			if err := Register(livePid, "construct-cli-daemon"); err != nil {
				t.Errorf("writer register: %v", err)
				return
			}
			data, err := os.ReadFile(target)
			if err != nil {
				continue // between rename and first write windows are fine
			}
			var s Session
			if uerr := json.Unmarshal(data, &s); uerr != nil {
				t.Errorf("reader observed a torn file: %v (bytes=%d)", uerr, len(data))
				return
			}
			if s.PID != livePid || s.Sandbox != "construct-cli-daemon" {
				t.Errorf("reader observed impossible content: %+v", s)
				return
			}
		}
	}()

	for i := 0; i < 300; i++ {
		if err := Register(livePid, "construct-cli-daemon"); err != nil {
			t.Fatalf("register %d: %v", i, err)
		}
	}
	close(stop)
	wg.Wait()
}

// TestSaveDaemonSpecLeavesNoTempLitter guards the rename contract of the
// daemon spec write: the recreate decision reads this file, so a crash
// mid-write must leave the previous complete spec, never a truncated one,
// and the temp file must never linger.
func TestSaveDaemonSpecLeavesNoTempLitter(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := saveMsbDaemonSpec(&msbDaemonSpec{ProjectDir: "/tmp/proj"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	matches, _ := filepath.Glob(msbDaemonSpecPath() + ".tmp-*")
	if len(matches) != 0 {
		t.Fatalf("temp litter left behind: %v", matches)
	}
	if _, err := os.Stat(msbDaemonSpecPath()); err != nil {
		t.Fatalf("spec file missing after save: %v", err)
	}
}

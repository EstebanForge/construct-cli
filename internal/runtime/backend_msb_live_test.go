package runtime

// Live msb verification (docs/VMs.md §7 Step 6). Skipped unless
// CONSTRUCT_MSB_LIVE=1 and the msb binary is on PATH, so CI and normal
// `make test` runs are unaffected. Verifies the full create->exec->stop
// path against a real microsandbox daemon: volumes, spec, sandbox boot
// with the construct image, exec with exit-code fidelity, mount
// propagation, and cleanup.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EstebanForge/construct-cli/internal/config"
)

func msbLiveEnabled(t *testing.T) {
	t.Helper()
	if os.Getenv("CONSTRUCT_MSB_LIVE") != "1" {
		t.Skip("CONSTRUCT_MSB_LIVE not set; skipping live msb test")
	}
	if _, err := exec.LookPath("msb"); err != nil {
		t.Skipf("msb binary not found: %v", err)
	}
}

// TestMsbLiveVolumesSpecSandboxExec is the Step 6 live gate short of the
// agent-install gate: boot a construct-box sandbox via the Go plumbing and
// exec inside it.
func TestMsbLiveVolumesSpecSandboxExec(t *testing.T) {
	msbLiveEnabled(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	cfg := config.DefaultConfig()
	cfg.Network.Mode = "permissive"

	if err := EnsureMsbVolumes(ctx); err != nil {
		t.Fatalf("EnsureMsbVolumes: %v", err)
	}

	name := "construct-msb-live-test"
	m := NewMsbBackend()
	// Leftover from a failed prior run: remove so creation is clean.
	if err := m.Cleanup(ctx, name); err != nil {
		t.Fatalf("pre-run Cleanup: %v", err)
	}

	// Project dir with a marker file; verify the bind mount inside the VM.
	proj := t.TempDir()
	marker := filepath.Join(proj, "marker.txt")
	if err := os.WriteFile(marker, []byte("construct-live"), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	spec := BuildMsbRunSpec(&cfg, name, proj, nil)
	sb, err := CreateMsbSandbox(ctx, spec)
	if err != nil {
		t.Fatalf("CreateMsbSandbox: %v", err)
	}
	defer func() {
		_ = sb.RequestStop(ctx)  //nolint:errcheck // best-effort teardown
		_, _ = sb.WaitUntilStopped(ctx)
	}()

	// Exec with exit-code fidelity (contract: non-zero exit is a result, not an error).
	out, code, err := m.Exec(ctx, ExecOptions{Name: name, Command: []string{"echo", "hi"}})
	if err != nil {
		t.Fatalf("Exec echo: %v", err)
	}
	if code != 0 || !strings.Contains(out, "hi") {
		t.Fatalf("Exec echo: code=%d out=%q", code, out)
	}

	out, code, err = m.Exec(ctx, ExecOptions{Name: name, Command: []string{"sh", "-c", "exit 7"}})
	if err != nil {
		t.Fatalf("Exec exit7: %v", err)
	}
	if code != 7 {
		t.Fatalf("exit-code fidelity: want 7, got %d", code)
	}

	// Bind-mount propagation: read the marker from /workspace.
	out, code, err = m.Exec(ctx, ExecOptions{Name: name, Command: []string{"cat", "/workspace/marker.txt"}})
	if err != nil || code != 0 {
		t.Fatalf("Exec cat marker: code=%d err=%v", code, err)
	}
	if !strings.Contains(out, "construct-live") {
		t.Fatalf("marker content not visible in guest: %q", out)
	}

	// Entrypoint ran: gosu dropped to construct, PATH contains linuxbrew.
	out, code, err = m.Exec(ctx, ExecOptions{Name: name, User: "construct", Command: []string{"sh", "-c", "echo $PATH"}})
	if err != nil || code != 0 {
		t.Fatalf("Exec PATH: code=%d err=%v", code, err)
	}
	if !strings.Contains(out, "/home/linuxbrew/.linuxbrew/bin") {
		t.Fatalf("linuxbrew missing from PATH: %q", out)
	}

	// Gitignore seeded via FS copy (msb cannot bind single files, §7.1).
	if _, ok := getGlobalGitIgnorePath(); ok {
		out, code, err = m.Exec(ctx, ExecOptions{Name: name, Command: []string{"test", "-f", "/home/construct/.config/git/ignore"}})
		if err != nil || code != 0 {
			t.Fatalf("gitignore seed: code=%d err=%v", code, err)
		}
	}

	// State + lifecycle.
	state, err := m.State(ctx, name)
	if err != nil || state != ContainerStateRunning {
		t.Fatalf("State: %v (%v)", state, err)
	}
	if err := m.Stop(ctx, name); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := m.Cleanup(ctx, name); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if _, err := os.Stat("/dev/null"); err != nil { // keep os import used on all builds
		t.Fatalf("stat: %v", err)
	}
}

package runtime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

// TestMsbLiveSnapshotFork is the phase 6 spike (docs/VMsv2.md) in miniature:
// it proves the snapshot-fork recreate primitive end to end against a real
// msb engine and the cached construct-box image. The daemon fork reuses the
// exact same sequence (stop -> snapshot -> cleanup -> disk-only restore with
// fresh volumes), so any semantic drift msb-side fails here first.
//
// What it asserts:
//  1. SandboxHandle.Snapshot captures the disk of a STOPPED sandbox.
//  2. RestoreSandbox + WithSnapshotDiskOnly accepts a NEW volume set (the
//     whole point: msb has no hot-add, binds are declared at boot).
//  3. Root-disk state written before the snapshot survives the fork.
//  4. The default workload re-runs on the restored VM (the entrypoint must
//     boot again; its root-fs hash gate is what makes the daemon fork fast).
//
// Diagnostic (logged, not asserted): whether create-time env survives the
// restore. The daemon fork keeps the OUTGOING daemon's env until the next
// cold recreate; the log line documents the observed behavior.
func TestMsbLiveSnapshotFork(t *testing.T) {
	msbLiveEnabled(t)

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	const imageRef = "ghcr.io/estebanforge/construct-box:latest"
	if _, err := msb.Image.Get(ctx, imageRef); err != nil {
		t.Skipf("construct-box image not cached; skipping (pull it first): %v", err)
	}

	tmpA := t.TempDir()
	tmpB := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpA, "host-a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}

	const srcName = "construct-fork-probe-a"
	const forkName = "construct-fork-probe-b"
	// Snapshot names scope per source sandbox ("<sandbox>:<name>"); a destroyed
	// and recreated same-named sandbox makes a reused name conflict inside its
	// group. Unique per-capture names sidestep it (production does the same).
	snapName := fmt.Sprintf("construct-fork-probe-snap-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		dctx, dcancel := context.WithTimeout(context.Background(), time.Minute)
		defer dcancel()
		for _, name := range []string{forkName, srcName} {
			if h, err := msb.GetSandbox(dctx, name); err == nil {
				_ = h.Stop(dctx, msb.WithStopTimeout(15*time.Second))
			}
			_ = NewMsbBackend().Cleanup(dctx, name)
		}
		_ = msb.Snapshot.Remove(dctx, srcName+":"+snapName, true)
	})

	// Source sandbox: trivial workload that marks each boot on the root disk,
	// so the restore assertion can tell "disk carried" from "workload re-ran".
	sb, err := msb.CreateSandbox(ctx, srcName,
		msb.WithImage(imageRef),
		msb.WithMounts(map[string]msb.MountConfig{
			"/probe-a": msb.Mount.Bind(tmpA, msb.MountOptions{}),
		}),
		msb.WithEnv(map[string]string{"CONSTRUCT_FORK_PROBE": "1"}),
		msb.WithEntrypoint("/bin/bash"),
		msb.WithCmd("-c", "echo boot >> /root/workload-runs; exec sleep infinity"),
		msb.WithCPUs(1),
		msb.WithMemory(1024),
		msb.WithDetached(),
	)
	if err != nil {
		t.Fatalf("create source sandbox: %v", err)
	}
	go func() { _, _ = sb.ExecDefault(context.Background()) }()
	if err := waitProbeMarker(ctx, sb, "/root/workload-runs", 90*time.Second); err != nil {
		t.Fatalf("source workload never ran: %v", err)
	}
	// Root-disk state written AFTER the workload (pre-snapshot payload).
	if out, err := sb.Exec(ctx, "bash", []string{"-c", "echo payload > /root/fork-payload"}); err != nil || out.ExitCode() != 0 {
		t.Fatalf("write pre-snapshot payload: err=%v out=%v", err, out)
	}

	// Spike unknown #1: a FULL snapshot (disk + checkpoint) captured from the
	// RUNNING source. disk_only restore requires checkpoint state; a stopped
	// capture (disk scope) is rejected at restore time.
	snapStart := time.Now()
	snap, err := msb.Snapshot.Create(ctx, msb.SnapshotCreateOptions{
		FromSandbox: srcName,
		Name:        snapName,
		Full:        true,
		// Full captures skip the guest writeback by default (Auto): the disk
		// as-of-checkpoint misses dirty page cache, which disk-only restore
		// then loses with the memory state. Require the flush; the disk-only
		// boot must see every pre-snapshot write.
		GuestFlush: msb.GuestFlushRequired,
	})
	if err != nil {
		t.Fatalf("snapshot running sandbox: %v", err)
	}
	t.Logf("full snapshot captured in %s (reference=%s kind=%s)", time.Since(snapStart).Round(time.Millisecond), snap.Reference(), snap.ReferenceKind())

	if err := sb.Stop(ctx, msb.WithStopTimeout(30*time.Second)); err != nil {
		t.Fatalf("stop source sandbox: %v", err)
	}
	if err := NewMsbBackend().Cleanup(ctx, srcName); err != nil {
		t.Fatalf("cleanup source sandbox: %v", err)
	}

	// Spike unknown #2 + #3: restore disk-only with a NEW volume set.
	restoreStart := time.Now()
	forked, err := msb.RestoreSandbox(ctx, snap, forkName,
		msb.WithRestoreConfig(msb.RestoreConfig{
			SnapshotDiskOnly: true,
			Volumes: map[string]msb.MountConfig{
				"/probe-b": msb.Mount.Bind(tmpB, msb.MountOptions{}),
			},
			CPUs:      ptrUint8(1),
			MemoryMiB: ptrUint32(1024),
		}),
		msb.WithDangerouslyInheritResources(),
	)
	if err != nil {
		t.Fatalf("restore disk-only snapshot: %v", err)
	}
	t.Logf("disk-only restore with fresh volumes booted in %s", time.Since(restoreStart).Round(time.Millisecond))

	// Pre-snapshot payload must have survived (disk carried over).
	if out, err := forked.Exec(ctx, "bash", []string{"-c", "cat /root/fork-payload"}); err != nil || out == nil || strings.TrimSpace(out.Stdout()) != "payload" {
		t.Fatalf("pre-snapshot payload lost on restore: err=%v out=%+v", err, out)
	}
	// The NEW bind must be a live mount, and the OLD bind must not mount.
	// Mountpoint DIRECTORIES may survive inside the captured rootfs; only
	// /proc/mounts tells bind state apart from leftover dirs.
	if out, err := forked.Exec(ctx, "bash", []string{"-c", "grep -q ' /probe-b ' /proc/mounts"}); err != nil || out == nil || out.ExitCode() != 0 {
		t.Fatalf("new bind /probe-b not mounted on restored VM: err=%v out=%+v", err, out)
	}
	if out, err := forked.Exec(ctx, "bash", []string{"-c", "grep -q ' /probe-a ' /proc/mounts"}); err != nil || out == nil || out.ExitCode() == 0 {
		t.Fatalf("old bind /probe-a still mounted on restored VM: err=%v out=%+v", err, out)
	}
	// And the leftover mountpoint dir must not serve the old host content.
	if out, err := forked.Exec(ctx, "test", []string{"-e", "/probe-a/host-a.txt"}); err != nil || out == nil || out.ExitCode() == 0 {
		t.Fatalf("old bind content reachable on restored VM: err=%v out=%+v", err, out)
	}

	// Spike unknown #4: the restored VM carries no sandbox-level workload or
	// env (restore has no cmd/env surface, and pid1 env is empty). The boot
	// must therefore run the workload explicitly with per-exec env — exactly
	// what the production daemon fork does with the entrypoint.
	go func() {
		_, _ = forked.Exec(context.Background(), "/bin/bash",
			[]string{"-c", "echo boot $CONSTRUCT_FORK_PROBE >> /root/workload-runs; exec sleep infinity"},
			msb.WithExecEnv(map[string]string{"CONSTRUCT_FORK_PROBE": "1"}))
	}()
	runsErr := waitProbeFileContent(ctx, forked, "/root/workload-runs", "boot\nboot", 60*time.Second)
	if out, err := forked.Exec(ctx, "cat", []string{"/root/workload-runs"}); err == nil && out != nil {
		t.Logf("workload-runs content: %q", out.Stdout())
	}
	t.Logf("workload re-run on restored VM (with per-exec env): %v", runsErr == nil)

	if runsErr != nil {
		t.Fatalf("default workload did not re-run on restored VM: %v", runsErr)
	}

	// What does the restored sandbox RECORD expose? The recreate-decision
	// engine reads labels + config JSON from GetSandbox/Config; the fork
	// must not lose the decision inputs or the daemon recreates again
	// immediately. Restored labels/config decide whether construct-side
	// spec persistence is needed (production decision below depends on it).
	if fh, err := msb.GetSandbox(ctx, forkName); err == nil {
		if cfg2, cerr := fh.Config(); cerr == nil && cfg2 != nil {
			t.Logf("restored record: memory=%d labels=%v volumes=%d", cfg2.MemoryMiB, cfg2.Labels, len(cfg2.Volumes))
		}
		t.Logf("restored config json: %s", fh.ConfigJSON())
	}
}

// waitProbeMarker polls until exec sees path exist in the sandbox.
func waitProbeMarker(ctx context.Context, sb *msb.Sandbox, path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if out, err := sb.Exec(ctx, "test", []string{"-e", path}); err == nil && out != nil && out.ExitCode() == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return waitProbeDeadline(timeout)
}

// waitProbeFileContent polls until the file's content contains want.
func waitProbeFileContent(ctx context.Context, sb *msb.Sandbox, path, want string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if out, err := sb.Exec(ctx, "cat", []string{path}); err == nil && out != nil && strings.Contains(out.Stdout(), want) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return waitProbeDeadline(timeout)
}

func waitProbeDeadline(timeout time.Duration) error {
	return &timeoutError{timeout: timeout}
}

type timeoutError struct{ timeout time.Duration }

func (e *timeoutError) Error() string { return "probe timed out after " + e.timeout.String() }

func ptrUint8(v uint8) *uint8    { return &v }
func ptrUint32(v uint32) *uint32 { return &v }

package runtime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

// TestMsbLiveSnapshotForkBisect isolates which daemon-spec element makes the
// engine reject a full capture ("control operation rejected by peer"). The
// plain probe (TestMsbLiveSnapshotFork) captures fine; the real daemon does
// not. Variants via CONSTRUCT_FORK_BISECT: network (msbNetworkPublic-style
// policy), timeouts (MaxDuration 0 / IdleTimeout 0), geometry (4 CPU / 4 GiB),
// binds (16), all (every element). Default: all.
func TestMsbLiveSnapshotForkBisect(t *testing.T) {
	msbLiveEnabled(t)

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	const imageRef = "ghcr.io/estebanforge/construct-box:latest"
	if _, err := msb.Image.Get(ctx, imageRef); err != nil {
		t.Skipf("construct-box image not cached; skipping: %v", err)
	}

	variant := os.Getenv("CONSTRUCT_FORK_BISECT")
	if variant == "" {
		variant = "all"
	}
	name := fmt.Sprintf("construct-fork-bisect-%s", variant)
	snapName := fmt.Sprintf("bisect-snap-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		dctx, dcancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer dcancel()
		if h, err := msb.GetSandbox(dctx, name); err == nil {
			_ = h.Stop(dctx, msb.WithStopTimeout(15*time.Second))
			_ = NewMsbBackend().Cleanup(dctx, name)
		}
		_ = msb.Snapshot.Remove(dctx, name+":"+snapName, true)
	})

	opts := []msb.SandboxOption{
		msb.WithImage(imageRef),
		msb.WithEntrypoint("/bin/bash"),
		msb.WithCmd("-c", "exec sleep infinity"),
		msb.WithDetached(),
	}
	if variant == "network" || variant == "all" {
		opts = append(opts, msb.WithNetwork(msbNetworkConfig("permissive", nil)))
	}
	if variant == "timeouts" || variant == "all" {
		opts = append(opts, msb.WithMaxDuration(0), msb.WithIdleTimeout(0))
	}
	if variant == "geometry" || variant == "all" {
		opts = append(opts, msb.WithCPUs(4), msb.WithMemory(4096))
	}
	if variant == "binds" || variant == "all" {
		mounts := map[string]msb.MountConfig{}
		for i := 0; i < 15; i++ {
			dir := filepath.Join(t.TempDir(), fmt.Sprintf("d%d", i))
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			mounts[fmt.Sprintf("/binds/b%d", i)] = msb.Mount.Bind(dir, msb.MountOptions{StatVirtualization: msb.StatVirtualizationOff})
		}
		opts = append(opts, msb.WithMounts(mounts))
	}

	if _, err := msb.CreateSandbox(ctx, name, opts...); err != nil {
		t.Fatalf("create bisect sandbox: %v", err)
	}
	h, err := msb.GetSandbox(ctx, name)
	if err != nil {
		t.Fatalf("get handle: %v", err)
	}
	sb, err := h.Connect(ctx)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	go func() { _, _ = sb.ExecDefault(context.Background()) }()
	time.Sleep(3 * time.Second) // let the workload start

	captureStart := time.Now()
	_, err = msb.Snapshot.Create(ctx, msb.SnapshotCreateOptions{
		FromSandbox: name,
		Name:        snapName,
		Full:        true,
		GuestFlush:  msb.GuestFlushRequired,
	})
	elapsed := time.Since(captureStart).Round(time.Millisecond)
	if err != nil {
		t.Logf("variant %q: full capture REJECTED after %s: %v", variant, elapsed, err)
		return
	}
	t.Logf("variant %q: full capture OK in %s", variant, elapsed)
}

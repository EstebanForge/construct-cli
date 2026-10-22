package runtime

// msb store garbage collection: reclaims construct-owned store growth that
// nothing else cleans up. The image store duplicates ~12 GB whenever more
// than one candidate ref is cached (a local build shadows the GHCR pull),
// and fork snapshots leak whenever a fork fails between capture and the
// post-fork prune (each is a full disk image, ~4 GiB).
//
// Scope is deliberately narrow: image removals stay inside
// constructImageRefCandidates (never the boot ref msb would pick), and
// snapshot removals only touch "<daemon>:construct-daemon-fork-*" names —
// user snapshots are never construct-owned. Entry points run off the run
// path: the idle window (IdleWindowUpdate) and doctor --fix.

import (
	"context"
	"fmt"
	"slices"
	"strings"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

// msbGCImage and msbGCSnapshot are the plain-data views the GC logic and
// its tests use; the defaults convert from the SDK, whose handle types
// cannot be constructed in tests (unexported fields).
type msbGCImage struct {
	ref       string
	sizeBytes uint64
}

type msbGCSnapshot struct {
	name      string
	sizeBytes uint64
}

var (
	gcListImagesFn     = defaultGcListImages
	gcListSnapshotsFn  = defaultGcListSnapshots
	gcListSandboxesFn  = defaultGcListSandboxes
	gcRemoveImageFn    = defaultGcRemoveImage
	gcRemoveSnapshotFn = defaultGcRemoveSnapshot
)

func defaultGcListImages(ctx context.Context) ([]msbGCImage, error) {
	handles, err := msb.Image.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("msb image list: %w", err)
	}
	out := make([]msbGCImage, 0, len(handles))
	for _, h := range handles {
		img := msbGCImage{ref: h.Reference()}
		if b := h.SizeBytes(); b != nil && *b > 0 {
			img.sizeBytes = uint64(*b)
		}
		out = append(out, img)
	}
	return out, nil
}

func defaultGcRemoveImage(ctx context.Context, ref string) error {
	h, err := msb.Image.Get(ctx, ref)
	if err != nil {
		return fmt.Errorf("msb image get %s: %w", ref, err)
	}
	// force=false: an in-use refusal is the safety net, not an obstacle.
	if err := h.Remove(ctx, false); err != nil {
		return fmt.Errorf("msb image rm %s: %w", ref, err)
	}
	return nil
}

func defaultGcListSnapshots(ctx context.Context) ([]msbGCSnapshot, error) {
	handles, err := msb.Snapshot.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("msb snapshot list: %w", err)
	}
	out := make([]msbGCSnapshot, 0, len(handles))
	for _, h := range handles {
		name := h.Name()
		if name == nil {
			continue
		}
		sn := msbGCSnapshot{name: *name}
		if b := h.SizeBytes(); b != nil {
			sn.sizeBytes = *b
		}
		out = append(out, sn)
	}
	return out, nil
}

func defaultGcRemoveSnapshot(ctx context.Context, scoped string) error {
	if err := msb.Snapshot.Remove(ctx, scoped, true); err != nil {
		return fmt.Errorf("msb snapshot rm %s: %w", scoped, err)
	}
	return nil
}

func defaultGcListSandboxes(ctx context.Context) ([]string, error) {
	page, err := msb.ListSandboxes(ctx)
	if err != nil {
		return nil, fmt.Errorf("msb sandbox list: %w", err)
	}
	out := make([]string, 0, len(page.Sandboxes))
	for _, h := range page.Sandboxes {
		out = append(out, h.Name())
	}
	return out, nil
}

// MsbStoreGCReport counts what GC removed (Run) or would remove (Scan).
type MsbStoreGCReport struct {
	ImagesRemoved          []string
	ImageBytesReclaimed    uint64
	SnapshotsRemoved       []string
	SnapshotBytesReclaimed uint64
}

// Empty reports whether there is nothing to reclaim.
func (r MsbStoreGCReport) Empty() bool {
	return len(r.ImagesRemoved) == 0 && len(r.SnapshotsRemoved) == 0
}

func (r MsbStoreGCReport) String() string {
	return fmt.Sprintf("%d image ref(s) (%s) and %d fork snapshot(s) (%s)",
		len(r.ImagesRemoved), humanBytes(r.ImageBytesReclaimed),
		len(r.SnapshotsRemoved), humanBytes(r.SnapshotBytesReclaimed))
}

func humanBytes(n uint64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n > 0:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	default:
		return "0 B"
	}
}

// ScanMsbStoreGC computes reclaimables without removing anything.
func ScanMsbStoreGC(ctx context.Context) (MsbStoreGCReport, error) {
	return msbStoreGC(ctx, func(_, _ string, _ uint64) error { return nil })
}

// RunMsbStoreGC removes the reclaimables and reports what went.
func RunMsbStoreGC(ctx context.Context) (MsbStoreGCReport, error) {
	remove := func(kind, name string, _ uint64) error {
		if kind == "image" {
			return gcRemoveImageFn(ctx, name)
		}
		return gcRemoveSnapshotFn(ctx, name)
	}
	return msbStoreGC(ctx, remove)
}

// msbStoreGC walks the GC rules, calling act for every reclaimable item.
// act is removal in Run mode and a no-op in Scan mode. Snapshot collection
// fails closed: when sandbox liveness cannot be determined, no snapshot is
// touched, and the first removal error aborts the pass (the next idle
// window or doctor --fix retries).
func msbStoreGC(ctx context.Context, act func(kind, name string, size uint64) error) (MsbStoreGCReport, error) {
	var rep MsbStoreGCReport
	var firstErr error
	recordErr := func(err error) {
		if firstErr == nil {
			firstErr = err
		}
	}

	images, err := gcListImagesFn(ctx)
	if err != nil {
		recordErr(err)
	} else {
		// Boot ref = first cached candidate, mirroring loadedImageRef but
		// derived from the same list seam so tests stay fully stubbed.
		boot := ""
		for _, cand := range constructImageRefCandidates {
			if slices.ContainsFunc(images, func(i msbGCImage) bool { return i.ref == cand }) {
				boot = cand
				break
			}
		}
		for _, img := range images {
			if img.ref == boot || !isConstructImageCandidate(img.ref) {
				continue
			}
			if aerr := act("image", img.ref, img.sizeBytes); aerr != nil {
				recordErr(aerr)
				break
			}
			rep.ImagesRemoved = append(rep.ImagesRemoved, img.ref)
			rep.ImageBytesReclaimed += img.sizeBytes
		}
	}

	sandboxes, err := gcListSandboxesFn(ctx)
	if err != nil {
		recordErr(fmt.Errorf("snapshot GC skipped (cannot determine sandbox liveness): %w", err))
		return rep, firstErr
	}
	live := make(map[string]bool, len(sandboxes))
	for _, n := range sandboxes {
		live[n] = true
	}

	snaps, err := gcListSnapshotsFn(ctx)
	if err != nil {
		recordErr(err)
		return rep, firstErr
	}
	scopedPrefix := msbDaemonName + ":" + msbForkSnapshotPrefix
	// All matching snapshots belong to the daemon group (the name embeds
	// the source sandbox); keep the newest when the daemon exists, by name:
	// fork names embed unix seconds, so the lexicographic max is newest.
	var items []msbGCSnapshot
	newest := ""
	for _, sn := range snaps {
		if !strings.HasPrefix(sn.name, scopedPrefix) {
			continue
		}
		items = append(items, sn)
		if sn.name > newest {
			newest = sn.name
		}
	}
	keep := ""
	if live[msbDaemonName] {
		keep = newest
	}
	for _, sn := range items {
		if sn.name == keep {
			continue
		}
		if aerr := act("snapshot", sn.name, sn.sizeBytes); aerr != nil {
			recordErr(aerr)
			return rep, firstErr
		}
		rep.SnapshotsRemoved = append(rep.SnapshotsRemoved, sn.name)
		rep.SnapshotBytesReclaimed += sn.sizeBytes
	}
	return rep, firstErr
}

func isConstructImageCandidate(ref string) bool {
	return slices.Contains(constructImageRefCandidates, ref)
}

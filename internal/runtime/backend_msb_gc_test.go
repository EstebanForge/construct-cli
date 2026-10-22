package runtime

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

type gcStub struct {
	images     []msbGCImage
	snapshots  []msbGCSnapshot
	sandboxes  []string
	imgErr     error
	snapErr    error
	sbErr      error
	removedImg []string
	removedSn  []string
	rmErr      error
}

// installGcStubs replaces every GC seam for one test and restores all of
// them via t.Cleanup, mirroring the seam-hygiene rule for package vars.
func installGcStubs(t *testing.T, s *gcStub) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	gcListImagesFn = func(_ context.Context) ([]msbGCImage, error) { return s.images, s.imgErr }
	gcListSnapshotsFn = func(_ context.Context) ([]msbGCSnapshot, error) { return s.snapshots, s.snapErr }
	gcListSandboxesFn = func(_ context.Context) ([]string, error) { return s.sandboxes, s.sbErr }
	gcRemoveImageFn = func(_ context.Context, ref string) error {
		if s.rmErr != nil {
			return s.rmErr
		}
		s.removedImg = append(s.removedImg, ref)
		return nil
	}
	gcRemoveSnapshotFn = func(_ context.Context, scoped string) error {
		if s.rmErr != nil {
			return s.rmErr
		}
		s.removedSn = append(s.removedSn, scoped)
		return nil
	}
	t.Cleanup(func() {
		gcListImagesFn = defaultGcListImages
		gcListSnapshotsFn = defaultGcListSnapshots
		gcListSandboxesFn = defaultGcListSandboxes
		gcRemoveImageFn = defaultGcRemoveImage
		gcRemoveSnapshotFn = defaultGcRemoveSnapshot
	})
}

func TestMsbStoreGCRemovesNonBootImageRefs(t *testing.T) {
	s := &gcStub{images: []msbGCImage{
		{ref: "localhost/construct-box:latest", sizeBytes: 12 << 30},
		{ref: "construct-box:latest", sizeBytes: 12 << 30},
		{ref: "ghcr.io/estebanforge/construct-box:latest", sizeBytes: 11 << 30},
	}, sandboxes: []string{msbDaemonName}}
	installGcStubs(t, s)
	rep, err := RunMsbStoreGC(context.Background())
	if err != nil {
		t.Fatalf("gc: %v", err)
	}
	if len(rep.ImagesRemoved) != 2 {
		t.Fatalf("want 2 image removals (every cached candidate except the boot ref), got %v", rep.ImagesRemoved)
	}
	if !slices.Contains(rep.ImagesRemoved, "construct-box:latest") || !slices.Contains(rep.ImagesRemoved, "ghcr.io/estebanforge/construct-box:latest") {
		t.Fatalf("wrong refs removed: %v", rep.ImagesRemoved)
	}
	if slices.Contains(rep.ImagesRemoved, "localhost/construct-box:latest") {
		t.Fatalf("boot ref must stay: %v", rep.ImagesRemoved)
	}
	if rep.ImageBytesReclaimed != (12<<30)+(11<<30) {
		t.Fatalf("bytes: got %d", rep.ImageBytesReclaimed)
	}
}

func TestMsbStoreGCIgnoresForeignImages(t *testing.T) {
	s := &gcStub{images: []msbGCImage{
		{ref: "docker.io/library/alpine:latest", sizeBytes: 8 << 20},
	}, sandboxes: []string{}}
	installGcStubs(t, s)
	rep, err := RunMsbStoreGC(context.Background())
	if err != nil {
		t.Fatalf("gc: %v", err)
	}
	if !rep.Empty() {
		t.Fatalf("foreign images must be untouched: %+v", rep)
	}
}

func TestMsbStoreGCNoImagesNoError(t *testing.T) {
	s := &gcStub{}
	installGcStubs(t, s)
	rep, err := RunMsbStoreGC(context.Background())
	if err != nil {
		t.Fatalf("empty store must not error: %v", err)
	}
	if !rep.Empty() {
		t.Fatalf("want empty report, got %+v", rep)
	}
}

func TestMsbStoreGCDeadDaemonSnapshotsRemoved(t *testing.T) {
	s := &gcStub{snapshots: []msbGCSnapshot{
		{name: msbDaemonName + ":construct-daemon-fork-100", sizeBytes: 4 << 30},
		{name: msbDaemonName + ":construct-daemon-fork-200", sizeBytes: 4 << 30},
	}, sandboxes: []string{}}
	installGcStubs(t, s)
	rep, err := RunMsbStoreGC(context.Background())
	if err != nil {
		t.Fatalf("gc: %v", err)
	}
	if len(rep.SnapshotsRemoved) != 2 {
		t.Fatalf("dead daemon's fork snapshots must all go, got %v", rep.SnapshotsRemoved)
	}
	if rep.SnapshotBytesReclaimed != 8<<30 {
		t.Fatalf("bytes: got %d", rep.SnapshotBytesReclaimed)
	}
}

func TestMsbStoreGCKeepsNewestForkSnapshotForLiveDaemon(t *testing.T) {
	s := &gcStub{snapshots: []msbGCSnapshot{
		{name: msbDaemonName + ":construct-daemon-fork-100", sizeBytes: 4 << 30},
		{name: msbDaemonName + ":construct-daemon-fork-300", sizeBytes: 4 << 30},
		{name: msbDaemonName + ":construct-daemon-fork-200", sizeBytes: 4 << 30},
	}, sandboxes: []string{msbDaemonName, "other-sb"}}
	installGcStubs(t, s)
	rep, err := RunMsbStoreGC(context.Background())
	if err != nil {
		t.Fatalf("gc: %v", err)
	}
	if len(rep.SnapshotsRemoved) != 2 {
		t.Fatalf("want the two stale forks removed, got %v", rep.SnapshotsRemoved)
	}
	for _, n := range rep.SnapshotsRemoved {
		if strings.HasSuffix(n, "-300") {
			t.Fatalf("newest fork snapshot must stay: %v", rep.SnapshotsRemoved)
		}
	}
}

func TestMsbStoreGCLeavesForeignSnapshots(t *testing.T) {
	s := &gcStub{snapshots: []msbGCSnapshot{
		{name: "mysandbox:my-manual-snapshot", sizeBytes: 1 << 30},
		{name: msbDaemonName + ":unrelated-name", sizeBytes: 1 << 30},
	}, sandboxes: []string{}}
	installGcStubs(t, s)
	rep, err := RunMsbStoreGC(context.Background())
	if err != nil {
		t.Fatalf("gc: %v", err)
	}
	for _, n := range rep.SnapshotsRemoved {
		if n == "mysandbox:my-manual-snapshot" {
			t.Fatalf("user snapshots must never be touched")
		}
		if strings.HasSuffix(n, ":unrelated-name") {
			t.Fatalf("only construct-daemon-fork- names are construct-owned: %v", rep.SnapshotsRemoved)
		}
	}
	if !rep.Empty() {
		t.Fatalf("want empty report, got %+v", rep)
	}
}

func TestMsbStoreGCSandboxListFailsClosed(t *testing.T) {
	s := &gcStub{
		snapshots: []msbGCSnapshot{{name: msbDaemonName + ":construct-daemon-fork-100", sizeBytes: 4 << 30}},
		sbErr:     errors.New("msb down"),
	}
	installGcStubs(t, s)
	rep, err := RunMsbStoreGC(context.Background())
	if err == nil {
		t.Fatalf("snapshot GC must fail closed when liveness is unknown")
	}
	if len(rep.SnapshotsRemoved) != 0 {
		t.Fatalf("nothing may be removed without liveness: %v", rep.SnapshotsRemoved)
	}
}

func TestMsbStoreGCScanDoesNotRemove(t *testing.T) {
	s := &gcStub{
		images: []msbGCImage{
			{ref: "localhost/construct-box:latest", sizeBytes: 12 << 30},
			{ref: "ghcr.io/estebanforge/construct-box:latest", sizeBytes: 11 << 30},
		},
		snapshots: []msbGCSnapshot{{name: msbDaemonName + ":construct-daemon-fork-100", sizeBytes: 4 << 30}},
		sandboxes: []string{},
	}
	installGcStubs(t, s)
	rep, err := ScanMsbStoreGC(context.Background())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(s.removedImg) != 0 || len(s.removedSn) != 0 {
		t.Fatalf("scan must not remove anything: %v %v", s.removedImg, s.removedSn)
	}
	if len(rep.ImagesRemoved) != 1 || len(rep.SnapshotsRemoved) != 1 { // ghcr reclaimable, localhost is boot
		t.Fatalf("scan must still report reclaimables: %+v", rep)
	}
}

package runtime

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

func TestMsbRecreateForkableReasons(t *testing.T) {
	forkable := []string{
		"host skills mounts changed (source, mode, or targets)",
		"daemon.mount_paths changed",
		"workspace roots changed (learned root added, removed, or evicted)",
	}
	for _, reason := range forkable {
		if !msbRecreateForkable(reason) {
			t.Errorf("reason %q should be forkable", reason)
		}
	}
	cold := []string{
		"sudo policy changed (sandbox.passwordless_sudo)",
		"construct-box image changed",
		"memory below minimum",
		"daemon has no workspace label",
		"workspace label does not match mounted state",
		"mounted workspace is no longer allowed",
		"",
	}
	for _, reason := range cold {
		if msbRecreateForkable(reason) {
			t.Errorf("reason %q must cold-recreate, not fork", reason)
		}
	}
}

// TestCaptureForkSnapshotLadderRungOrder pins the ladder the fork capture
// walks: full capture with Required flush, full capture with Auto flush,
// then a disk-only capture. The disk-only rung is the poison escape: a
// guest-tracked passthrough inode deleted on the host fails every full
// capture (device-state serialization), while disk capture never touches
// device state and is all the SnapshotDiskOnly restore consumes.
func TestCaptureForkSnapshotLadderRungOrder(t *testing.T) {
	art := &msb.SnapshotArtifact{}
	attempts := 0
	var seen []msb.SnapshotCreateOptions
	create := func(opts msb.SnapshotCreateOptions) (*msb.SnapshotArtifact, error) {
		seen = append(seen, opts)
		attempts++
		if attempts < 3 {
			return nil, errors.New("control operation rejected by peer: cannot reopen tracked passthrough inode")
		}
		return art, nil
	}

	snap, err := captureForkSnapshotLadder(create, "snap-a")
	if err != nil || snap != art {
		t.Fatalf("snap=%v err=%v, want artifact on the disk-only rung", snap, err)
	}
	if len(seen) != 3 {
		t.Fatalf("%d attempts, want 3", len(seen))
	}
	wantFlush := []msb.GuestFlush{msb.GuestFlushRequired, msb.GuestFlushAuto, msb.GuestFlushAuto}
	for i, opts := range seen {
		if opts.Name != "snap-a" || opts.FromSandbox != msbDaemonName {
			t.Errorf("attempt %d: name/sandbox = %q/%q, want snap-a/%s", i, opts.Name, opts.FromSandbox, msbDaemonName)
		}
		if want := i < 2; opts.Full != want {
			t.Errorf("attempt %d: Full = %v, want %v", i, opts.Full, want)
		}
		if opts.GuestFlush != wantFlush[i] {
			t.Errorf("attempt %d: GuestFlush = %q, want %q", i, opts.GuestFlush, wantFlush[i])
		}
	}
}

// TestCaptureForkSnapshotLadderFirstRungWins: a healthy daemon captures on
// the first rung; no fallback attempt may fire.
func TestCaptureForkSnapshotLadderFirstRungWins(t *testing.T) {
	art := &msb.SnapshotArtifact{}
	attempts := 0
	snap, err := captureForkSnapshotLadder(func(msb.SnapshotCreateOptions) (*msb.SnapshotArtifact, error) {
		attempts++
		return art, nil
	}, "snap-b")
	if err != nil || snap != art {
		t.Fatalf("snap=%v err=%v, want artifact on the first rung", snap, err)
	}
	if attempts != 1 {
		t.Fatalf("%d attempts, want 1", attempts)
	}
}

// TestCaptureForkSnapshotLadderAllRungsFail: exhausted ladder returns the
// last error so the caller's recycle-and-retry path still sees it.
func TestCaptureForkSnapshotLadderAllRungsFail(t *testing.T) {
	wantErr := errors.New("rejected")
	_, err := captureForkSnapshotLadder(func(msb.SnapshotCreateOptions) (*msb.SnapshotArtifact, error) {
		return nil, wantErr
	}, "snap-c")
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want the last rung error", err)
	}
}

func TestMsbDaemonInstallComplete(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if msbDaemonInstallComplete() {
		t.Fatal("no daemon home: install must count as incomplete")
	}
	home := filepath.Join(t.TempDir(), "link-target")
	marker := filepath.Join(home, ".local", ".construct_setup_complete")
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	// msbHostConstructHome resolves $HOME/.config/construct-cli/home; point
	// it at the prepared tree via a symlink like the real layout uses.
	configHome := filepath.Join(t.TempDir(), ".config", "construct-cli")
	if err := os.MkdirAll(configHome, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", filepath.Dir(filepath.Dir(configHome)))
	if err := os.Symlink(home, filepath.Join(configHome, "home")); err != nil {
		t.Fatal(err)
	}
	if !msbDaemonInstallComplete() {
		t.Fatal("marker present: install must count as complete")
	}
}

func TestMsbDaemonSpecRoundtripAndDecision(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// Absent file: not ok, no error.
	if _, ok, err := loadMsbDaemonSpec(); ok || err != nil {
		t.Fatalf("absent spec: ok=%v err=%v", ok, err)
	}

	labels := map[string]string{
		"construct.project_dir":   "/Users/x/proj",
		DaemonMountsLabelKey:      "mounts-hash",
		DaemonSkillsLabelKey:      "skills-hash",
		DaemonSudoLabelKey:        "free",
		DaemonImageDigestLabelKey: "sha256:abc",
		DaemonSDKVersionLabelKey:  "0.7.6",
		"construct.unrelated":     "ignored",
	}
	got := specFromLabels(labels).toLabels()
	for _, k := range []string{"construct.project_dir", DaemonMountsLabelKey, DaemonSkillsLabelKey, DaemonSudoLabelKey, DaemonImageDigestLabelKey, DaemonSDKVersionLabelKey} {
		if got[k] != labels[k] {
			t.Errorf("label %s: got %q want %q", k, got[k], labels[k])
		}
	}
	if _, ok := got["construct.unrelated"]; ok {
		t.Error("unrelated label leaked into the spec")
	}

	// Save + load roundtrip preserves every field.
	spec := specFromLabels(labels)
	if err := saveMsbDaemonSpec(spec); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, ok, err := loadMsbDaemonSpec()
	if err != nil || !ok {
		t.Fatalf("load: ok=%v err=%v", ok, err)
	}
	if *loaded != *spec {
		t.Fatalf("roundtrip mismatch: got %+v want %+v", *loaded, *spec)
	}

	// Decision labels prefer the spec file over record labels.
	recordLabels := map[string]string{DaemonMountsLabelKey: "stale-record-hash"}
	if decided := msbDaemonDecisionLabels(recordLabels); decided[DaemonMountsLabelKey] != "mounts-hash" {
		t.Fatalf("decision must prefer the spec file, got %v", decided)
	}

	// No file + record labels: migrate once, then serve the file.
	if err := os.Remove(msbDaemonSpecPath()); err != nil {
		t.Fatal(err)
	}
	decided := msbDaemonDecisionLabels(recordLabels)
	if decided[DaemonMountsLabelKey] != "stale-record-hash" {
		t.Fatalf("migration must return the record labels, got %v", decided)
	}
	if _, ok, err := loadMsbDaemonSpec(); err != nil || !ok {
		t.Fatalf("migration did not persist the spec: ok=%v err=%v", ok, err)
	}

	// Corrupt file: error (never silently treated as no-drift).
	if err := os.WriteFile(msbDaemonSpecPath(), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := loadMsbDaemonSpec(); err == nil || ok {
		t.Fatal("corrupt spec must surface an error")
	}
}

func TestConditionalAutoMountsAptCache(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))

	mounts := conditionalAutoMounts(nil)
	found := false
	for _, m := range mounts {
		if m.Dest == "/var/cache/apt/archives" {
			found = true
			if m.Readonly {
				t.Error("apt cache mount must be read-write")
			}
			if m.Src == "" {
				t.Error("apt cache mount source is empty")
			}
			if info, err := os.Stat(m.Src); err != nil || !info.IsDir() {
				t.Errorf("helper must create the cache dir: %v", err)
			}
		}
	}
	if !found {
		t.Error("apt cache auto-mount missing from conditionalAutoMounts")
	}
}

func TestSaveMsbDaemonSpecFromRunSpecNil(t *testing.T) {
	if err := saveMsbDaemonSpecFromRunSpec(nil); err != nil {
		t.Fatalf("nil spec must be a no-op, got %v", err)
	}
}

package msbembed

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestStubBuildBehavior pins the no-embed contract: every entry point
// degrades to the pre-embed behavior instead of erroring, so dev builds
// and unit tests keep using the host msb + launch gate.
func TestStubBuildBehavior(t *testing.T) {
	if Available() {
		t.Log("embedded build: stub-specific assertions skipped")
		return
	}
	if _, err := Ensure(); !errors.Is(err, ErrNotEmbedded) {
		t.Fatalf("Ensure on a stub build must return ErrNotEmbedded, got: %v", err)
	}
	cmd, err := Command()
	if err != nil || cmd != "msb" {
		t.Fatalf("Command on a stub build must be (\"msb\", nil), got (%q, %v)", cmd, err)
	}
	if err := Activate(); err != nil {
		t.Fatalf("Activate on a stub build must be a no-op, got: %v", err)
	}
}

// TestMarkerMatches covers the short-circuit decision: exact version,
// asset names, checksums, and non-truncated file sizes all match.
func TestMarkerMatches(t *testing.T) {
	dir := t.TempDir()
	pair := &Pair{
		MsbPath:       filepath.Join(dir, "msb"),
		LibkrunfwPath: filepath.Join(dir, "libkrunfw.so"),
		Version:       "0.7.6",
	}
	if err := os.WriteFile(pair.MsbPath, make([]byte, 10), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pair.LibkrunfwPath, make([]byte, 20), 0o755); err != nil {
		t.Fatal(err)
	}
	good := &extractionMarker{
		Version: "0.7.6",
		MsbSize: 10,
		LibSize: 20,
		MsbSum:  "aa",
		LibSum:  "bb",
		// Names must equal whatever this build compiled in; the stub build
		// carries empty names and still short-circuits on the full match.
		MsbName: embeddedMsbName,
		LibName: embeddedLibkrunfwName,
	}
	if !markerMatches(good, pair, "aa", "bb") {
		t.Fatal("matching marker must short-circuit")
	}

	cases := map[string]func(m *extractionMarker){
		"version drift":  func(m *extractionMarker) { m.Version = "0.7.7" },
		"asset drift":    func(m *extractionMarker) { m.MsbName = embeddedMsbName + "-other" },
		"checksum drift": func(m *extractionMarker) { m.LibSum = "cc" },
		"truncated msb":  func(m *extractionMarker) { m.MsbSize = 9 },
		"truncated lib":  func(m *extractionMarker) { m.LibSize = 19 },
	}
	for name, mutate := range cases {
		m := *good
		mutate(&m)
		if markerMatches(&m, pair, "aa", "bb") {
			t.Fatalf("%s: marker must not match", name)
		}
	}
	if markerMatches(nil, pair, "aa", "bb") {
		t.Fatal("nil marker must not match")
	}
}

// TestGCOldVersions removes sibling version dirs and keeps the live one,
// including odd entries (a stray file must not block the sweep).
func TestGCOldVersions(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root) // rootFn derives from the config dir
	runtimeRoot := filepath.Join(root, ".config", "construct-cli", "msb-runtime")
	for _, v := range []string{"0.7.5", "0.7.6", "0.8.0"} {
		if err := os.MkdirAll(filepath.Join(runtimeRoot, v), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	stray := filepath.Join(runtimeRoot, "leftover.tmp")
	if err := os.WriteFile(stray, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	gcOldVersions("0.7.6")
	entries, err := os.ReadDir(runtimeRoot)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(entries))
	for _, e := range entries {
		got = append(got, e.Name())
	}
	if len(got) != 1 || got[0] != "0.7.6" {
		t.Fatalf("GC must keep only 0.7.6, got: %v", got)
	}
}

// TestSumMatchesDetectsCorruption pins the fail-closed extraction gate.
func TestSumMatchesDetectsCorruption(t *testing.T) {
	if err := sumMatches([]byte("payload"), sha256Hex("payload"), "x"); err != nil {
		t.Fatalf("matching sum must pass, got: %v", err)
	}
	err := sumMatches([]byte("payload"), sha256Hex("other"), "x")
	if err == nil || errors.Is(err, ErrNotEmbedded) {
		t.Fatalf("corrupt payload must fail with a checksum error, got: %v", err)
	}
}

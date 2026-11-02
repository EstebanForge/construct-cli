package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EstebanForge/construct-cli/internal/config"
)

// withPrepullTestHome isolates HOME so the prepull log path lands in temp.
func withPrepullTestHome(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
}

// TestMaybePrepullImageDisabledByFlag: cfg.Runtime.PrepullImage = false
// is a no-op. Verified by the absence of side-effects: no log file is
// created (the spawn never happened), and the process exits cleanly.
func TestMaybePrepullImageDisabledByFlag(t *testing.T) {
	withPrepullTestHome(t)
	cfg := &config.Config{
		Runtime: config.RuntimeConfig{
			Backend:      config.BackendMsb,
			PrepullImage: false,
		},
	}
	MaybePrepullImage(cfg)
	// No log file means we never spawned. The test runner never has
	// `msb` on PATH, so a real spawn would have failed and logged.
	if _, err := os.Stat(prepullLogFilePath()); err == nil {
		t.Errorf("prepull log was written; expected no-op when PrepullImage=false")
	}
}

// TestMaybePrepullImageSkipsNonMicrovm: the prepull only makes sense for
// the microvm backend. For other backends, the helper is a no-op even
// when PrepullImage=true. The imageLoadedForPrepull probe is short-
// circuited to false (test runner has no msb), but the backend gate
// should fire first.
func TestMaybePrepullImageSkipsNonMicrovm(t *testing.T) {
	withPrepullTestHome(t)
	cfg := &config.Config{
		Runtime: config.RuntimeConfig{
			Backend:      "docker",
			PrepullImage: true,
		},
	}
	MaybePrepullImage(cfg)
	if _, err := os.Stat(prepullLogFilePath()); err == nil {
		t.Errorf("prepull log was written; expected no-op for non-microvm backend")
	}
}

func TestPullImageRetriesWithResumableLayers(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "calls")
	statePath := filepath.Join(dir, "state")
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$MSB_TEST_LOG"
count=0
if [ -f "$MSB_TEST_STATE" ]; then count=$(cat "$MSB_TEST_STATE"); fi
count=$((count + 1))
printf '%s' "$count" > "$MSB_TEST_STATE"
if [ "$count" -lt 3 ]; then exit 1; fi
`
	installFakeMsb(t, dir, script)
	t.Setenv("MSB_TEST_LOG", logPath)
	t.Setenv("MSB_TEST_STATE", statePath)

	if err := PullImage(&config.Config{Runtime: config.RuntimeConfig{Backend: config.BackendMsb}}); err != nil {
		t.Fatalf("PullImage() error = %v", err)
	}

	calls := readFakeMsbCalls(t, logPath)
	if len(calls) != 3 {
		t.Fatalf("msb call count = %d, want 3; calls = %q", len(calls), calls)
	}
	for i, call := range calls {
		if call != "image pull "+PrepullImageRef {
			t.Errorf("call %d = %q, want resumable image pull", i+1, call)
		}
	}
}

func TestPullImageStopsAfterThreeFailedAttempts(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "calls")
	installFakeMsb(t, dir, "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$MSB_TEST_LOG\"\nexit 1\n")
	t.Setenv("MSB_TEST_LOG", logPath)

	err := PullImage(&config.Config{Runtime: config.RuntimeConfig{Backend: config.BackendMsb}})
	if err == nil {
		t.Fatal("PullImage() error = nil, want terminal pull failure")
	}
	if calls := readFakeMsbCalls(t, logPath); len(calls) != 3 {
		t.Fatalf("msb call count = %d, want 3; calls = %q", len(calls), calls)
	}
}

func installFakeMsb(t *testing.T, dir, script string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "msb"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func readFakeMsbCalls(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

// TestPrepullLogPathStable: the log path is fixed so users know where
// to look; verified by computing the expected path from config.
func TestPrepullLogPathStable(t *testing.T) {
	withPrepullTestHome(t)
	got := prepullLogFilePath()
	want := config.GetConfigDir() + "/logs/prepull.log"
	if got != want {
		t.Errorf("prepullLogFilePath = %q, want %q", got, want)
	}
}

// toConfig is a tiny helper to lift a RuntimeConfig literal into a
// *config.Config for the MaybePrepullImage signature. The struct in
// the standard library has no method to do this cleanly without
// re-typing every field; we only need the test, so a one-line wrapper
// keeps the test code readable.
//
// (Removed in favor of constructing the *config.Config inline in each
// test; the field set is small and the inline form is clearer.)

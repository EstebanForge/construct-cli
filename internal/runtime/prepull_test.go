package runtime

import (
	"os"
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
			Backend:      "microvm",
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

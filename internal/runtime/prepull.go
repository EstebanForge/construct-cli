package runtime

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/EstebanForge/construct-cli/internal/config"
	"github.com/EstebanForge/construct-cli/internal/ui"
)

// prepullImageRef is the GHCR tag the prepuller pulls and re-tags as
// construct-box:latest (matching EnsureImage's expectation).
const prepullImageRef = "ghcr.io/estebanforge/construct-box:latest"

// prepullLogName is the file under the construct logs dir where the
// prepuller writes its output. Best-effort: missing logs dir is created.
const prepullLogName = "prepull.log"

// MaybePrepullImage spawns a detached `msb pull` + `msb image tag` to
// stage construct-box:latest in the background. The foreground command
// (SelfUpdate, first-run init) returns immediately; the next ct
// invocation finds the image already pulled and skips the slow path in
// EnsureImage.
//
// No-op when:
//   - cfg is nil
//   - cfg.Runtime.PrepullImage is false
//   - cfg.Runtime.Backend is not "microvm" (the prepull pulls the msb
//     image; the docker backend does not benefit)
//   - the construct-box image is already loaded locally (skip the work)
//
// Best-effort: failures log one line and do not propagate. The detached
// process inherits the host's HOME, PATH, and msb config dir so it can
// authenticate to GHCR the same way the foreground ct did.
func MaybePrepullImage(cfg *config.Config) {
	if cfg == nil || !cfg.Runtime.PrepullImage {
		return
	}
	if !strings.EqualFold(strings.TrimSpace(cfg.Runtime.Backend), "microvm") {
		return
	}
	if imageLoadedForPrepull() {
		ui.InfoLn("✓ construct-box image already present; skipping prepull")
		return
	}

	exe, err := os.Executable()
	if err != nil {
		ui.InfoF("⚠️  Could not resolve executable for prepull: %v\n", err)
		return
	}

	cmd := exec.Command(exe, "sys", "prepull", "--once")
	cmd.Stdin = nil
	logPath := prepullLogFilePath()
	cmd.Stdout = prepullLogWriter(logPath, false)
	cmd.Stderr = prepullLogWriter(logPath, true)
	setDetachedAttrs(cmd)
	if err := cmd.Start(); err != nil {
		ui.InfoF("⚠️  Could not spawn prepull: %v\n", err)
		return
	}
	if err := cmd.Process.Release(); err != nil {
		ui.InfoF("⚠️  Could not release prepull: %v\n", err)
		return
	}
	ui.InfoF("⏳ Spawned background prepull (log: %s)\n", logPath)
}

// PrepullRun is the body of `construct sys prepull --once`. Runs the GHCR
// pull and tag, logs progress to the prepull log file, exits 0 on success
// and 1 on any error. Invoked as a detached process by MaybePrepullImage;
// not meant for direct user invocation (but harmless if run).
func PrepullRun() {
	logPath := prepullLogFilePath()
	f := openPrepullLog(logPath)
	//nolint:errcheck // best-effort; log data lost on early close is acceptable
	defer f.Close()

	logPrepull := func(format string, args ...interface{}) {
		ts := time.Now().Format(time.RFC3339)
		//nolint:errcheck // best-effort; log file is line-buffered enough that we accept the rare dropped line
		fmt.Fprintf(f, "%s %s\n", ts, fmt.Sprintf(format, args...))
	}

	logPrepull("prepull started")
	pull := exec.Command("msb", "pull", prepullImageRef)
	pull.Stdout = f
	pull.Stderr = f
	if err := pull.Run(); err != nil {
		logPrepull("prepull FAILED at pull: %v", err)
		fmt.Fprintf(os.Stderr, "prepull pull failed: %v (see %s)\n", err, logPath)
		os.Exit(1)
	}
	tag := exec.Command("msb", "image", "tag", prepullImageRef, "construct-box:latest")
	tag.Stdout = f
	tag.Stderr = f
	if err := tag.Run(); err != nil {
		logPrepull("prepull FAILED at tag: %v", err)
		fmt.Fprintf(os.Stderr, "prepull tag failed: %v (see %s)\n", err, logPath)
		os.Exit(1)
	}
	logPrepull("prepull done")
}

// imageLoadedForPrepull is a fast probe of `msb image ls` for the local
// construct-box tag. Best-effort: any error returns false (the prepull
// will run, and EnsureImage's slower probe will decide).
func imageLoadedForPrepull() bool {
	out, err := exec.Command("msb", "image", "ls").CombinedOutput()
	if err != nil {
		return false
	}
	return containsBytes(out, "construct-box:latest")
}

// containsBytes is a tiny helper to avoid the strings import in this
// file (the larger runtime package already pulls it elsewhere).
func containsBytes(haystack []byte, needle string) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if string(haystack[i:i+len(needle)]) == needle {
			return true
		}
	}
	return false
}

// prepullLogFilePath returns the path to the prepull log inside the
// construct logs dir. Creates the dir on demand.
func prepullLogFilePath() string {
	dir := filepath.Join(config.GetConfigDir(), "logs")
	//nolint:errcheck // best-effort; PrepullRun writes to stderr if the dir cannot be created
	_ = os.MkdirAll(dir, 0o755)
	return filepath.Join(dir, prepullLogName)
}

// openPrepullLog opens the log file for append-and-create. Falls back to
// os.Stderr when the file cannot be opened (e.g. read-only logs dir).
func openPrepullLog(path string) *os.File {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "prepull: cannot open log %s: %v; using stderr\n", path, err)
		return os.Stderr
	}
	return f
}

// prepullLogWriter returns a writer that appends each line to the log
// file with a timestamp. When append is true, the writer is the log
// file (stderr); when false, stdout.
func prepullLogWriter(path string, stderr bool) *os.File {
	// For simplicity we just route both stdout and stderr to the same
	// log file via openPrepullLog. The shell captures them together;
	// the prepull log file is line-prefixed by the script.
	_ = path
	_ = stderr
	return openPrepullLog(path)
}

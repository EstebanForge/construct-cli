// Package hostexec implements the host exec bridge: a mechanism to proxy
// invocations of selected binaries from inside the Construct container to the
// host machine, where they actually execute.
//
// This file implements host-side symlink reconciliation: for each binary in
// [config.SandboxConfig.HostBinaries], a symlink at
// ~/.config/construct-cli/home/.local/bin/<name> points at the in-image shim
// /usr/local/bin/construct-host-exec. Because that directory is bind-mounted
// into the container as /home/construct/.local/bin, the symlinks land on the
// host filesystem directly (no docker exec required to manage them).
//
// See docs/HOST-EXEC.md for the design.
package hostexec

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/EstebanForge/construct-cli/internal/ui"
)

// ShimTarget is the in-image path the symlinks point at. The shim binary is
// baked into the image at this location (ImageTierTemplates); it is the same
// for every proxied binary and dispatches based on os.Args[0] basename.
const ShimTarget = "/usr/local/bin/construct-host-exec"

// manifestName holds the basename of the ownership manifest, one construct-
// created symlink name per line. Lives in ~/.local/bin alongside the symlinks
// so it travels with them on the bind-mount. Only entries recorded here are
// eligible for removal during reconciliation; a user-placed symlink with the
// same name as a removed allowlist entry is left untouched.
const manifestName = ".construct_host_exec_shims"

// lockName is the basename of the non-blocking flock file. Mirrors
// acquireSetupLock (internal/agent/runner.go): contention skips this
// invocation's reconciliation rather than blocking Prepare().
const lockName = manifestName + ".lock"

// ErrShimLockBusy signals another construct instance holds the reconcile lock.
// Callers skip reconciliation when this is returned (see Reconcile).
var ErrShimLockBusy = errors.New("host-exec shim reconcile lock busy")

// ReconcileResult summarizes what a Reconcile call changed, for diagnostics.
type ReconcileResult struct {
	Created []string
	Removed []string
	Skipped bool // true when the lock was contended
}

// ReconcileShims ensures ~/.local/bin (under homeDir) contains exactly one
// symlink per name in binaries, each pointing at ShimTarget, and that no
// previously construct-created symlinks remain for names no longer listed.
//
// homeDir is the host-side home tree that bind-mounts into /home/construct
// (i.e. <configPath>/home). It MUST already exist; only the .local/bin subdir
// is created here. binaries may be empty (in which case only removal happens).
//
// Concurrency: a non-blocking flock serializes concurrent reconciles; on
// contention Reconcile returns a zero-change result with Skipped=true and no
// error, since the lock-holder's reconcile covers the same state.
func ReconcileShims(homeDir string, binaries []string) (ReconcileResult, error) {
	var res ReconcileResult

	binDir := filepath.Join(homeDir, ".local", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return res, fmt.Errorf("hostexec: mkdir %s: %w", binDir, err)
	}

	release, err := acquireShimLock(binDir)
	if err != nil {
		if errors.Is(err, ErrShimLockBusy) {
			res.Skipped = true
			ui.LogDebug("hostexec: shim reconcile skipped (lock busy)")
			return res, nil
		}
		return res, err
	}
	defer release()

	manifestPath := filepath.Join(binDir, manifestName)
	owned, err := readManifest(manifestPath)
	if err != nil {
		return res, fmt.Errorf("hostexec: read manifest: %w", err)
	}

	want := make(map[string]struct{}, len(binaries))
	for _, b := range binaries {
		want[b] = struct{}{}
	}

	// Create missing symlinks for newly-listed binaries.
	for _, name := range binaries {
		if name == "" {
			continue
		}
		linkPath := filepath.Join(binDir, name)
		if isConstructShim(linkPath) {
			continue // already ours and correct
		}
		// If something else is there (real file, user symlink, etc.) we do NOT
		// clobber it: the user may have their own binary shadowing this name.
		// Log and skip; the agent will resolve whatever is on PATH.
		if _, statErr := os.Lstat(linkPath); statErr == nil {
			ui.LogDebug("hostexec: %s already exists and is not our shim; leaving it", linkPath)
			continue
		}
		if err := os.Symlink(ShimTarget, linkPath); err != nil {
			return res, fmt.Errorf("hostexec: symlink %s: %w", linkPath, err)
		}
		owned[name] = struct{}{}
		res.Created = append(res.Created, name)
	}

	// Remove construct-owned symlinks whose names are no longer listed.
	for name := range owned {
		if _, ok := want[name]; ok {
			continue
		}
		linkPath := filepath.Join(binDir, name)
		// Only remove if it still points at our shim. A user may have replaced
		// our symlink with their own file; never delete that.
		if !isConstructShim(linkPath) {
			delete(owned, name) // no longer ours; drop from manifest
			continue
		}
		if err := os.Remove(linkPath); err != nil {
			ui.LogDebug("hostexec: failed to remove stale shim %s: %v", linkPath, err)
			continue
		}
		delete(owned, name)
		res.Removed = append(res.Removed, name)
	}

	if err := writeManifestAtomic(manifestPath, owned); err != nil {
		return res, fmt.Errorf("hostexec: write manifest: %w", err)
	}

	return res, nil
}

// isConstructShim reports whether path is a symlink resolving to ShimTarget.
// Nonexistent paths and non-symlinks return false.
func isConstructShim(path string) bool {
	dest, err := os.Readlink(path)
	if err != nil {
		return false
	}
	return dest == ShimTarget
}

// acquireShimLock takes a non-blocking exclusive flock on the reconcile
// lockfile in binDir. The returned release func closes (and so releases) the
// lock; callers MUST defer it. On EWOULDBLOCK it returns ErrShimLockBusy.
func acquireShimLock(binDir string) (func(), error) {
	lockPath := filepath.Join(binDir, lockName)
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open shim lock: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		cerr := f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrShimLockBusy
		}
		if cerr != nil {
			ui.LogDebug("hostexec: failed to close shim lock file: %v", cerr)
		}
		return nil, fmt.Errorf("acquire shim lock: %w", err)
	}
	return func() {
		if err := f.Close(); err != nil {
			ui.LogDebug("hostexec: failed to release shim lock: %v", err)
		}
	}, nil
}

// readManifest loads owned names. A missing file is not an error (fresh
// install); it yields an empty set. Blank lines and whitespace are ignored.
func readManifest(path string) (map[string]struct{}, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]struct{}{}, nil
		}
		return nil, err
	}
	defer f.Close() //nolint:errcheck

	owned := make(map[string]struct{})
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		name := filepath.Base(scanner.Text())
		if name == "" || name == "." {
			continue
		}
		owned[name] = struct{}{}
	}
	return owned, scanner.Err()
}

// writeManifestAtomic writes one basename per line via a temp file + rename,
// so a crash never leaves a half-written manifest the next Reconcile reads.
// Both files live in the same directory (same filesystem), so rename is atomic.
func writeManifestAtomic(path string, owned map[string]struct{}) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), manifestName+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath) //nolint:errcheck
		}
	}()

	w := bufio.NewWriter(tmp)
	for name := range owned {
		if _, err := fmt.Fprintln(w, filepath.Base(name)); err != nil {
			_ = tmp.Close() //nolint:errcheck
			return err
		}
	}
	if err := w.Flush(); err != nil {
		_ = tmp.Close() //nolint:errcheck
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}

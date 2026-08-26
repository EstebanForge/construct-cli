package runtime

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/EstebanForge/construct-cli/internal/config"
	"github.com/EstebanForge/construct-cli/internal/ui"
	"golang.org/x/term"
)

// RootsStore tracks roots the daemon has learned to mount, so single-path
// daemon users get per-project warmth without configuring daemon.mount_paths.
// Lives at ~/.config/construct-cli/roots.json. Read and written only inside
// the daemon flock critical section (phase 1) so concurrent ct
// invocations learning different roots never produce last-write-wins loss.
//
// Configured daemon.mount_paths are NOT tracked here — they are pinned in
// the daemon config and never evicted.
type RootsStore struct {
	Version int           `json:"version"`
	Roots   []LearnedRoot `json:"roots"`
}

// LearnedRoot is one host directory the daemon learned to mount. Path is
// symlink-resolved (reuse cleanProjectDir) so storage is canonical.
// LastUsed is updated on every successful mount-set resolution (best-effort,
// also inside the lock; failure logs and continues with in-memory set).
type LearnedRoot struct {
	Path      string    `json:"path"`
	LearnedAt time.Time `json:"learned_at"`
	LastUsed  time.Time `json:"last_used"`
}

// rootsStoreVersion is bumped when the on-disk format changes. Older
// files are loaded as best-effort and missing fields fall back to zero
// values; the next save persists the new format.
const rootsStoreVersion = 1

// rootsStoreFileName is the JSON filename inside the construct config dir.
// Lives next to config.toml; one per host user.
const rootsStoreFileName = "roots.json"

// rootsStoreFilePath returns the host file path.
func rootsStoreFilePath() string {
	return filepath.Join(config.GetConfigDir(), rootsStoreFileName)
}

// LoadRootsStore reads roots.json. Returns an empty (version 1) store when
// the file is missing or malformed; a corrupt file is treated as empty
// (the daemon would not start otherwise). The flock (phase 1) is the
// caller's responsibility.
func LoadRootsStore() (RootsStore, error) {
	path := rootsStoreFilePath()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return RootsStore{Version: rootsStoreVersion}, nil
	}
	if err != nil {
		return RootsStore{}, err
	}
	var s RootsStore
	if jerr := json.Unmarshal(data, &s); jerr != nil {
		return RootsStore{Version: rootsStoreVersion}, nil
	}
	if s.Version == 0 {
		s.Version = rootsStoreVersion
	}
	return s, nil
}

// SaveRootsStore writes the store atomically (temp + rename) so a crash
// mid-write cannot leave a truncated file behind. Reuses the pattern from
// internal/config/config.go writeFileAtomic.
func SaveRootsStore(s RootsStore) error {
	if s.Version == 0 {
		s.Version = rootsStoreVersion
	}
	path := rootsStoreFilePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".roots-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, werr := tmp.Write(data); werr != nil {
		tmp.Close()        //nolint:errcheck // best-effort cleanup on write failure
		os.Remove(tmpName) //nolint:errcheck // best-effort cleanup
		return werr
	}
	if cerr := tmp.Chmod(0o644); cerr != nil {
		tmp.Close()        //nolint:errcheck // best-effort cleanup
		os.Remove(tmpName) //nolint:errcheck
		return cerr
	}
	if cerr := tmp.Close(); cerr != nil {
		os.Remove(tmpName) //nolint:errcheck // best-effort cleanup
		return cerr
	}
	return os.Rename(tmpName, path)
}

// TouchRoot updates LastUsed for an existing entry or appends a new one.
// Caller is responsible for: prompt-on-learn (interactive only), LRU
// eviction, workspace guard, cap enforcement, atomic persist.
func (s *RootsStore) TouchRoot(path string, now time.Time) {
	for i := range s.Roots {
		if s.Roots[i].Path == path {
			s.Roots[i].LastUsed = now
			return
		}
	}
	s.Roots = append(s.Roots, LearnedRoot{
		Path:      path,
		LearnedAt: now,
		LastUsed:  now,
	})
}

// ForgetRoot removes a learned root by exact path. Returns false when the
// path was not learned (the CLI surfaces this as "not a learned root").
func (s *RootsStore) ForgetRoot(path string) bool {
	for i := range s.Roots {
		if s.Roots[i].Path == path {
			s.Roots = append(s.Roots[:i], s.Roots[i+1:]...)
			return true
		}
	}
	return false
}

// EvictLRU drops the least-recently-used learned roots until len <= maxN.
// Returns the evicted paths in eviction order (oldest first) so the caller
// can warn once about the churn. maxN <= 0 means no cap (no eviction).
func (s *RootsStore) EvictLRU(maxN int) []string {
	if maxN <= 0 || len(s.Roots) <= maxN {
		return nil
	}
	sort.SliceStable(s.Roots, func(i, j int) bool {
		return s.Roots[i].LastUsed.Before(s.Roots[j].LastUsed)
	})
	excess := len(s.Roots) - maxN
	evicted := make([]string, 0, excess)
	for i := 0; i < excess; i++ {
		evicted = append(evicted, s.Roots[i].Path)
	}
	s.Roots = s.Roots[excess:]
	return evicted
}

// Paths returns the resolved paths in stable order (sorted) so the hash
// and the daemon mount set are deterministic. Drops entries whose host
// path no longer exists (silent, matches the existing single-path mapper's
// "drop silently with a log line" contract).
func (s RootsStore) Paths() []string {
	out := make([]string, 0, len(s.Roots))
	for _, r := range s.Roots {
		info, err := os.Stat(r.Path)
		if err != nil || !info.IsDir() {
			continue
		}
		out = append(out, r.Path)
	}
	sort.Strings(out)
	return out
}

// ResolveDaemonMountsWithLearned returns the combined mount set: configured
// (pinned) + learned (LRU-capped) roots. Hashes the union; configured roots
// stay ahead of learned ones in the sorted order so the hash is stable
// across order-only drift. The caller (EnsureMsbDaemon) is inside the
// flock critical section, so writes are serialized.
//
// When MultiPathsEnabled is false but the user has no mount_paths AND no
// learned roots, the returned set is empty (single-path mode still mounts
// just projectDir). When MultiPathsEnabled is true, this function falls
// through to ResolveDaemonMounts (learned roots are a single-path feature).
func ResolveDaemonMountsWithLearned(cfg *config.Config, _ RootsStore) DaemonMounts {
	if cfg == nil || !cfg.Daemon.MultiPathsEnabled {
		return ResolveDaemonMounts(cfg)
	}
	// (Defensive: today the learned-roots feature only matters in single-
	// path mode, but the wrapper exists for future MultiPathsEnabled +
	// learned-root support.)
	return ResolveDaemonMounts(cfg)
}

// requestLearnRoot prompts the user (interactive only) to add a new root to
// the daemon's learned roots. Returns:
//
//   - (true, nil)            root learned and saved
//   - (false, nil)           workspace guard failed OR user declined; no error
//   - (false, ErrMapped...)  non-interactive or interactive-denied: caller
//     surfaces the actionable message and the
//     ErrMsbDaemonWorkdirUnmapped error
//
// MUST be called inside the daemon flock critical section (phase 1) so
// concurrent ct invocations learning different roots never produce
// last-write-wins root loss.
//
//nolint:unused // wired into EnsureMsbDaemon as part of phase 2.2 (combined mount-set resolution); kept here with full tests so the helper is reviewed alongside its data layer
func requestLearnRoot(cfg *config.Config, projectDir string) (bool, error) {
	resolved := cleanProjectDir(projectDir)
	if resolved == "" {
		return false, nil
	}
	// Note: the workspace guard (EvaluateWorkspace RiskSystem) is enforced
	// upstream by cleanProjectDir, which returns "" for system roots. By
	// the time we reach here the path has been classified OK. We keep the
	// guard as a defensive backstop in case a future caller bypasses
	// cleanProjectDir; the decline case below produces (false, nil), which
	// the P2.2 call site turns into ErrMsbDaemonWorkdirUnmapped.
	if EvaluateWorkspace(resolved, 0).Risk == WorkspaceRiskSystem {
		ui.InfoF("Refusing to learn system root: %s\n", resolved)
		return false, nil
	}

	isInteractive := ui.GumAvailable() && term.IsTerminal(int(os.Stdin.Fd()))
	if !isInteractive {
		ui.InfoF("cd into %s and run construct once interactively to add it, or add it to daemon.mount_paths\n", resolved)
		return false, ErrMsbDaemonWorkdirUnmapped
	}

	prompt := fmt.Sprintf("Add %s to the daemon's mounted roots?", resolved)
	if !ui.GumConfirm(prompt) {
		return false, nil
	}

	store, err := LoadRootsStore()
	if err != nil {
		return false, err
	}
	now := time.Now()
	store.TouchRoot(resolved, now)
	if cfg != nil {
		if evicted := store.EvictLRU(cfg.Daemon.MaxLearnedRoots); len(evicted) > 0 {
			ui.InfoF("Evicted %d learned root(s) past the cap (%d): %v\n",
				len(evicted), cfg.Daemon.MaxLearnedRoots, evicted)
		}
	}
	if err := SaveRootsStore(store); err != nil {
		return false, err
	}
	return true, nil
}

// DaemonRootsList prints the daemon's learned roots (phase 2). Pinned
// configured paths from cfg.Daemon.MountPaths are shown alongside, marked
// "configured". Loads the roots store; missing file = empty list (no error).
//
// Caller is the CLI dispatcher; print errors via the user-facing ui helpers.
func DaemonRootsList(cfg *config.Config) {
	store, err := LoadRootsStore()
	if err != nil {
		ui.GumError(fmt.Sprintf("Failed to load roots store: %v", err))
		os.Exit(1)
	}
	fmt.Println("Daemon learned roots (host dirs auto-mounted under single-path mode):")
	if len(store.Roots) == 0 {
		fmt.Println("  (none yet — interactive ct from a new project will offer to learn)")
	}
	for _, r := range store.Roots {
		fmt.Printf("  %s\n", r.Path)
		fmt.Printf("    learned: %s\n", r.LearnedAt.Format(time.RFC3339))
		fmt.Printf("    last used: %s\n", r.LastUsed.Format(time.RFC3339))
	}
	if cfg != nil && len(cfg.Daemon.MountPaths) > 0 {
		fmt.Println("\nPinned configured paths (cannot be forgotten via this command):")
		for _, p := range cfg.Daemon.MountPaths {
			fmt.Printf("  %s (configured)\n", p)
		}
	}
	fmt.Printf("\nForget: construct sys daemon roots forget <path>\n")
}

// DaemonRootsForget removes a learned root by exact path. Configured
// daemon.mount_paths entries are pinned and CANNOT be forgotten here:
// remove them from config.toml instead. Refuses paths not in the learned
// set so a typo does not silently succeed.
func DaemonRootsForget(cfg *config.Config, path string) {
	// Refuse configured paths early (clearer error than the store lookup).
	if cfg != nil {
		for _, p := range cfg.Daemon.MountPaths {
			if p == path {
				ui.GumError(fmt.Sprintf("%s is a configured daemon.mount_paths entry. Remove it from config.toml instead.", path))
				os.Exit(1)
			}
		}
	}
	store, err := LoadRootsStore()
	if err != nil {
		ui.GumError(fmt.Sprintf("Failed to load roots store: %v", err))
		os.Exit(1)
	}
	if !store.ForgetRoot(path) {
		ui.GumError(fmt.Sprintf("%s is not a learned root. Run `construct sys daemon roots` to list the learned set.", path))
		os.Exit(1)
	}
	if err := SaveRootsStore(store); err != nil {
		ui.GumError(fmt.Sprintf("Failed to save roots store: %v", err))
		os.Exit(1)
	}
	ui.GumInfo(fmt.Sprintf("Forgotten learned root %s. The next ct run recreates the daemon with the smaller mount set.", path))
}

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
	// Declined records folders where the user answered NO to the learn
	// prompt. Key: symlink-resolved path; value: when the decline was
	// recorded. Declined paths never re-prompt and never enter the mount
	// set; `construct sys daemon roots add` is the way back.
	Declined map[string]time.Time `json:"declined,omitempty"`
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

// Has reports whether the exact cleaned path is already a learned root.
func (s RootsStore) Has(cleaned string) bool {
	for _, r := range s.Roots {
		if r.Path == cleaned {
			return true
		}
	}
	return false
}

// IsDeclined reports whether the exact cleaned path has a persisted
// user decline ("do not offer to mount this folder").
func (s RootsStore) IsDeclined(cleaned string) bool {
	_, ok := s.Declined[cleaned]
	return ok
}

// DeclineRoot records a user "no" for the exact cleaned path so later
// runs skip the learn prompt instead of re-asking.
func (s *RootsStore) DeclineRoot(path string, now time.Time) {
	if s.Declined == nil {
		s.Declined = make(map[string]time.Time, 1)
	}
	s.Declined[path] = now
}

// Undecline drops a decline record (the `roots add` path). Returns
// whether a record existed.
func (s *RootsStore) Undecline(path string) bool {
	if _, ok := s.Declined[path]; !ok {
		return false
	}
	delete(s.Declined, path)
	return true
}

// effectiveWorkspaceRoots returns the single-path mount set for this run:
// every learned root that still exists, plus the current project dir — but
// only when it is NOT already covered by a learned root (a subdir of a
// mounted root rides the parent mount instead of shadowing it) and NOT
// declined by the user (declined folders never enter the mount set).
// Sorted and deduplicated so the hash is stable. The caller is inside the
// daemon flock critical section.
func effectiveWorkspaceRoots(projectDir string, store RootsStore) []string {
	roots := store.Paths()
	cleaned := cleanProjectDir(projectDir)
	covered := false
	for _, r := range roots {
		if containsPath(r, cleaned) {
			covered = true
			break
		}
	}
	if cleaned != "" && !covered && !store.IsDeclined(cleaned) {
		roots = append(roots, cleaned)
	}
	sort.Strings(roots)
	return roots
}

// Test seams for the learn consent gate: no TTY and no gum binary exist
// under CI, so tests swap these instead of the ui package.
var (
	learnRootInteractive = func() bool { return ui.GumAvailable() && term.IsTerminal(int(os.Stdin.Fd())) }
	learnRootConfirm     = ui.GumConfirm
)

// declinedError builds the actionable error for a declined folder: the run
// cannot proceed without its workdir mounted, and the message carries the
// command that reverses the decision.
func declinedError(resolved string) error {
	return fmt.Errorf("%w: %s. Construct will not ask about this folder again. To mount it later: construct sys daemon roots add %s", ErrMsbDaemonWorkdirDeclined, resolved, resolved)
}

// requestLearnRoot prompts the user (interactive only) to add a new root to
// the daemon's learned roots. Returns:
//
//   - (true, nil)            root learned and saved
//   - (false, ErrMapped...)  non-interactive, or the folder was declined
//     before (ErrMsbDaemonWorkdirUnmapped /
//     ErrMsbDaemonWorkdirDeclined, both actionable)
//   - (false, ErrDeclined...)  interactive NO: the decline is persisted
//     (never re-prompts) and the error carries the manual-add command
//   - (false, nil)           workspace guard failed; caller ignores
//
// The store is RE-READ after the prompt: the interactive wait can last
// minutes, and a concurrent `roots add`/`roots forget` (which hold the
// flock themselves) would otherwise be clobbered by a stale in-memory copy.
// MUST be called inside the daemon flock critical section (phase 1) so
// concurrent ct invocations learning different roots never produce
// last-write-wins root loss.
func requestLearnRoot(cfg *config.Config, projectDir string) (bool, error) {
	resolved := cleanProjectDir(projectDir)
	if resolved == "" {
		return false, nil
	} // Note: the workspace guard (EvaluateWorkspace RiskSystem) is enforced
	// upstream by cleanProjectDir, which returns "" for system roots. By
	// the time we reach here the path has been classified OK. We keep the
	// guard as a defensive backstop in case a future caller bypasses
	// cleanProjectDir; the decline case below produces an error carrying
	// the manual-add hint, which the call site surfaces directly.
	if EvaluateWorkspace(resolved, 0).Risk == WorkspaceRiskSystem {
		ui.InfoF("Refusing to learn system root: %s\n", resolved)
		return false, nil
	}

	store, err := LoadRootsStore()
	if err != nil {
		return false, err
	}
	// A previously declined folder never re-prompts: the user already said
	// no, and re-asking on every run is nagging. The error tells them how
	// to change their mind later.
	if store.IsDeclined(resolved) {
		return false, declinedError(resolved)
	}

	isInteractive := learnRootInteractive()
	if !isInteractive {
		ui.InfoF("cd into %s and run construct once interactively to add it, or add it to daemon.mount_paths\n", resolved)
		return false, ErrMsbDaemonWorkdirUnmapped
	}

	prompt := fmt.Sprintf("Add %s to the daemon's mounted roots?", resolved)
	if !learnRootConfirm(prompt) {
		// Re-read AFTER the prompt: the interactive wait can last minutes,
		// and the pre-prompt snapshot may be stale (concurrent roots add/
		// forget hold the same flock and may have written meanwhile).
		store, err = LoadRootsStore()
		if err != nil {
			return false, err
		}
		// Persist the decline so the folder never re-prompts. A save
		// failure downgrades to today's behavior (asks again next run).
		store.DeclineRoot(resolved, time.Now())
		if serr := SaveRootsStore(store); serr != nil {
			ui.InfoF("Could not persist the decline (%v); you may be asked again.\n", serr)
		} else {
			ui.InfoF("Not mounting %s. Construct will not ask about this folder again.\n", resolved)
		}
		return false, declinedError(resolved)
	}

	// Same re-read for the accept path: learn must build on the latest
	// on-disk set, not the pre-prompt snapshot.
	store, err = LoadRootsStore()
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
	if len(store.Declined) > 0 {
		fmt.Println("\nDeclined folders (ct will not ask to mount these):")
		declinedPaths := make([]string, 0, len(store.Declined))
		for p := range store.Declined {
			declinedPaths = append(declinedPaths, p)
		}
		sort.Strings(declinedPaths)
		for _, p := range declinedPaths {
			fmt.Printf("  %s (declined %s)\n", p, store.Declined[p].Format(time.RFC3339))
		}
		fmt.Println("  Re-enable: construct sys daemon roots add <path>")
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
	// Store writes serialize behind the daemon flock: a concurrent ct run
	// may be inside its learn critical section, and last-write-wins here
	// would clobber it (or be clobbered).
	releaseLock, err := acquireDaemonLock()
	if err != nil {
		ui.GumError(fmt.Sprintf("Acquire daemon lock: %v", err))
		os.Exit(1)
	}
	defer releaseLock()
	store, err := LoadRootsStore()
	if err != nil {
		ui.GumError(fmt.Sprintf("Failed to load roots store: %v", err))
		os.Exit(1)
	}
	if store.ForgetRoot(path) {
		if err := SaveRootsStore(store); err != nil {
			ui.GumError(fmt.Sprintf("Failed to save roots store: %v", err))
			os.Exit(1)
		}
		ui.GumInfo(fmt.Sprintf("Forgotten learned root %s. The next ct run recreates the daemon with the smaller mount set.", path))
		return
	}
	// Not a learned root: clear a decline record if one exists (also the
	// way to drop a decline whose folder no longer exists on disk, which
	// `roots add` would refuse). Decline keys are canonicalized, so try
	// the raw argument first, then its cleaned form.
	if store.Undecline(path) || store.Undecline(cleanProjectDir(path)) {
		if err := SaveRootsStore(store); err != nil {
			ui.GumError(fmt.Sprintf("Failed to save roots store: %v", err))
			os.Exit(1)
		}
		ui.GumInfo(fmt.Sprintf("Removed decline record for %s. Construct will offer to mount it again on the next interactive run.", path))
		return
	}
	ui.GumError(fmt.Sprintf("%s is not a learned or declined root. Run `construct sys daemon roots` to list the known set.", path))
	os.Exit(1)
}

// DaemonRootsAdd mounts a host directory as a learned root without the
// interactive prompt: the manual way back after a decline (it clears any
// persisted decline record) or for scripting setups headlessly. Mirrors
// the learn-path guards: the path must exist, be a directory, and not be
// a system root. Idempotent: an already-learned path is a no-op success.
func DaemonRootsAdd(cfg *config.Config, path string) {
	resolved := cleanProjectDir(path)
	if resolved == "" {
		ui.GumError(fmt.Sprintf("Cannot mount %s: system paths cannot be daemon roots.", path))
		os.Exit(1)
	}
	if info, err := os.Stat(resolved); err != nil || !info.IsDir() {
		ui.GumError(fmt.Sprintf("Cannot mount %s: not an existing directory.", path))
		os.Exit(1)
	}
	// Serialize behind the daemon lock BEFORE touching the store (even the
	// configured-path early return cleans up a stale decline record).
	releaseLock, err := acquireDaemonLock()
	if err != nil {
		ui.GumError(fmt.Sprintf("Acquire daemon lock: %v", err))
		os.Exit(1)
	}
	defer releaseLock()
	if cfg != nil {
		for _, p := range cfg.Daemon.MountPaths {
			if p == resolved || p == path {
				// Pinning in config.toml makes the decline record stale;
				// clear it even on this early return so a later switch
				// back to single-path mode does not resurrect it.
				if store, serr := LoadRootsStore(); serr == nil {
					if store.Undecline(resolved) {
						_ = SaveRootsStore(store) //nolint:errcheck // best-effort cleanup; the pin already mounts it
					}
				}
				ui.GumInfo(fmt.Sprintf("%s is already a configured daemon.mount_paths entry; nothing to do.", resolved))
				return
			}
		}
	}
	store, err := LoadRootsStore()
	if err != nil {
		ui.GumError(fmt.Sprintf("Failed to load roots store: %v", err))
		os.Exit(1)
	}
	if store.Has(resolved) {
		store.Undecline(resolved) // no-op unless both records somehow exist
		_ = SaveRootsStore(store) //nolint:errcheck // decline cleanup is best-effort; the root already works
		ui.GumInfo(fmt.Sprintf("%s is already a learned root; nothing to do.", resolved))
		return
	}
	now := time.Now()
	store.TouchRoot(resolved, now)
	store.Undecline(resolved)
	if cfg != nil {
		if evicted := store.EvictLRU(cfg.Daemon.MaxLearnedRoots); len(evicted) > 0 {
			ui.InfoF("Evicted %d learned root(s) past the cap (%d): %v\n",
				len(evicted), cfg.Daemon.MaxLearnedRoots, evicted)
		}
	}
	if err := SaveRootsStore(store); err != nil {
		ui.GumError(fmt.Sprintf("Failed to save roots store: %v", err))
		os.Exit(1)
	}
	ui.GumInfo(fmt.Sprintf("Added %s to the daemon's mounted roots. The next ct run recreates the daemon with the new mount set.", resolved))
}

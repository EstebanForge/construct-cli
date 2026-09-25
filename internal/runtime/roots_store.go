package runtime

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
	// Accepted records folders where the user answered YES to the
	// large-workspace export warning (workspace_max_entries). Key:
	// symlink-resolved path; value: when the acceptance was recorded.
	// Accepted paths skip the warning and confirm on later runs; entries
	// are exempt from max_learned_roots and `roots forget` clears them.
	Accepted map[string]time.Time `json:"accepted,omitempty"`
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
	// Sanitize any legacy home entries: home is never a persistable learned root
	// and never a persistable decline record.
	if len(s.Roots) > 0 {
		cleanRoots := make([]LearnedRoot, 0, len(s.Roots))
		for _, r := range s.Roots {
			if !isUserHome(r.Path) {
				cleanRoots = append(cleanRoots, r)
			}
		}
		s.Roots = cleanRoots
	}
	if len(s.Declined) > 0 {
		for k := range s.Declined {
			if isUserHome(k) {
				delete(s.Declined, k)
			}
		}
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
	if isUserHome(path) {
		return
	}
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
// "drop silently with a log line" contract). Home itself is filtered out.
func (s RootsStore) Paths() []string {
	out := make([]string, 0, len(s.Roots))
	for _, r := range s.Roots {
		if isUserHome(r.Path) {
			continue
		}
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

// IsDeclined reports whether cleaned has a persisted user decline ("do
// not offer to mount this folder"), matching the declined path ITSELF or
// anything below it: a decline on ~/Secret excludes ~/Secret/sub too.
func (s RootsStore) IsDeclined(cleaned string) bool {
	return s.DeclineKeyFor(cleaned) != ""
}

// DeclineKeyFor returns the persisted decline record that covers cleaned
// (exact match or a declined ancestor), "" when none. The record key is
// what error messages should name: it is what the user originally said no
// to, and what `roots add` reverses. Home itself is never a valid decline.
func (s RootsStore) DeclineKeyFor(cleaned string) string {
	if cleaned == "" || isUserHome(cleaned) {
		return ""
	}
	if _, ok := s.Declined[cleaned]; ok {
		return cleaned
	}
	for declined := range s.Declined {
		if isUserHome(declined) {
			continue
		}
		if containsPath(declined, cleaned) {
			return declined
		}
	}
	return ""
}

// DeclineRoot records a user "no" for the exact cleaned path so later
// runs skip the learn prompt instead of re-asking.
func (s *RootsStore) DeclineRoot(path string, now time.Time) {
	if isUserHome(path) {
		return
	}
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

// IsAccepted reports whether cleaned carries a persisted large-workspace
// acceptance ("export anyway"), matching the accepted path ITSELF or
// anything below it: accepting ~/Dev covers runs from ~/Dev/projects too.
func (s RootsStore) IsAccepted(cleaned string) bool {
	return s.AcceptedKeyFor(cleaned) != ""
}

// AcceptedKeyFor returns the persisted acceptance record that covers
// cleaned (exact match or an accepted ancestor), "" when none. Home
// itself is never a valid acceptance (mirror DeclineKeyFor): the home
// warning is the always-ask regime, so an acceptance must never mute it.
func (s RootsStore) AcceptedKeyFor(cleaned string) string {
	if cleaned == "" || isUserHome(cleaned) {
		return ""
	}
	if _, ok := s.Accepted[cleaned]; ok {
		return cleaned
	}
	for accepted := range s.Accepted {
		if containsPath(accepted, cleaned) {
			return accepted
		}
	}
	return ""
}

// AcceptLargeExport records the user's "export anyway" for the exact
// cleaned path so later runs skip the large-workspace warning. Home
// itself is refused (mirror DeclineRoot): an acceptance must never mute
// the always-ask home warning.
func (s *RootsStore) AcceptLargeExport(path string, now time.Time) {
	if isUserHome(path) {
		return
	}
	if s.Accepted == nil {
		s.Accepted = make(map[string]time.Time, 1)
	}
	s.Accepted[path] = now
}

// Unaccept drops a large-workspace acceptance record. Returns whether a
// record existed.
func (s *RootsStore) Unaccept(path string) bool {
	if _, ok := s.Accepted[path]; !ok {
		return false
	}
	delete(s.Accepted, path)
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
	// Default-NO variant for high-blast-radius confirmations ($HOME).
	learnRootConfirmDanger = ui.GumConfirmNoDefault
)

// declinedError builds the actionable error for a declined folder: the run
// cannot proceed without its workdir mounted, and the message carries the
// command that reverses the decision.
func declinedError(record string) error {
	return fmt.Errorf("%w: %s. Construct will not ask about this folder again. To mount it later: construct sys daemon roots add %s", ErrMsbDaemonWorkdirDeclined, record, record)
}

// requestLearnRoot adds a new root to the daemon's learned roots. Three
// regimes:
//
//   - Inside the user's home directory: consent is implied by the
//     invocation itself (the mounted folder is the folder the user ran ct
//     from; the Docker backend mounts any cwd unconditionally). The root
//     is learned and persisted with NO prompt, interactive or headless.
//     Everyday ~/... flows never ask. Sensitive home trees (hidden
//     top-level dirs, ~/Library) and the home directory ITSELF are not
//     "inside": sensitive trees keep the human checkpoint, and home is
//     always warned and asked about, every run, with nothing remembered.
//   - Outside home: interactive runs get the gum prompt (a NO persists a
//     decline that never re-prompts); headless runs fail closed with
//     ErrMsbDaemonWorkdirUnmapped and the config hint.
//
// Return contract:
//   - (true, nil): root newly learned and saved to disk.
//   - (false, nil): proceed with one-off cwd mount without persisting
//     (home directory confirmed by human, or defensive system-root backstop).
//   - (false, err): mounting was refused, declined, or unmapped.
//
// Both regimes honor the manual exclusion list first: a declined folder
// returns ErrMsbDaemonWorkdirDeclined (carrying the `roots add` command)
// and is never learned or mounted. System-risk paths are refused upstream
// by cleanProjectDir and re-checked here as a backstop.
//
// The store is RE-READ inside learnRoot (and after the prompt): the
// interactive wait can last minutes, and a concurrent `roots add`/`roots
// forget` (which hold the flock themselves) would otherwise be clobbered
// by a stale in-memory copy.
// MUST be called inside the daemon flock critical section (phase 1) so
// concurrent ct invocations learning different roots never produce
// last-write-wins root loss.
func requestLearnRoot(cfg *config.Config, projectDir string) (bool, error) {
	resolved := cleanProjectDir(projectDir)
	if resolved == "" {
		return false, nil
	} // Defensive backstop: cleanProjectDir already refuses system roots.
	if EvaluateWorkspace(resolved, 0).Risk == WorkspaceRiskSystem {
		ui.InfoF("Refusing to learn system root: %s\n", resolved)
		return false, nil
	}

	// The home directory itself is a different beast: mounting it hands
	// the sandbox read-write access to every user file in the system,
	// ~/.ssh included. Nothing is EVER remembered (no persisted acceptance
	// and no persisted refusal), so every run from $HOME warns and asks
	// again. This check runs BEFORE exclusion checks so a stale or manual
	// decline on home never blocks the warning and prompt. Acceptance
	// mounts via the run's own cwd (one-off), never as a learned root;
	// `roots add` refuses home for the same reason.
	if isUserHome(resolved) {
		if !learnRootInteractive() {
			ui.InfoF("Refusing to mount %s without a human decision: mounting the entire home directory exposes every user file (including ~/.ssh private keys, browser data, and credentials) to the sandbox.\n", resolved)
			ui.InfoF("Run construct from a project directory instead, or add a narrower root to [daemon] mount_paths.\n")
			return false, ErrMsbDaemonWorkdirUnmapped
		}
		ui.InfoF("WARNING: %s is your ENTIRE home directory.\n", resolved)
		ui.InfoF("Mounting it gives the sandbox read-write access to every user file in the system: SSH keys, browser profiles, credential stores, documents. Everything.\n")
		ui.InfoF("A narrower root (a project directory) is almost always the safer choice.\n")
		prompt := "Mount your ENTIRE home directory into the VM anyway?"
		if !learnRootConfirmDanger(prompt) {
			return false, fmt.Errorf("%w: %s (nothing was remembered; construct will warn and ask again on the next run from this folder)", ErrMsbDaemonWorkdirDeclined, resolved)
		}
		// Accepted: proceed with the one-off cwd mount, persist nothing.
		ui.InfoF("Home mounted for this run only. Construct will ask again next time.\n")
		return false, nil
	}

	store, err := LoadRootsStore()
	if err != nil {
		return false, err
	}
	// The exclusion list wins everywhere: a declined folder never
	// re-prompts and never auto-learns (for the folder itself OR anything
	// below it: declining ~/Secret excludes ~/Secret/sub). The error names
	// the record the user originally declined, which `roots add` reverses.
	if record := store.DeclineKeyFor(resolved); record != "" {
		return false, declinedError(record)
	}

	// Learning opt-out: max_learned_roots = 0 disables the learned-root
	// mechanism entirely (config-only setups). Headless or not, an
	// uncovered folder fails with the config hint.
	if cfg != nil && cfg.Daemon.MaxLearnedRoots <= 0 {
		ui.InfoF("Root learning is disabled (daemon.max_learned_roots = 0). Add %s to [daemon] mount_paths to mount it.\n", resolved)
		return false, ErrMsbDaemonWorkdirUnmapped
	}

	// Everyday case: inside home, learn silently and get on with the run,
	// EXCEPT sensitive home locations (hidden top-level dirs like ~/.ssh,
	// ~/.aws, ~/.gnupg, ~/.cache; macOS ~/Library), which route to the
	// checkpoint regime below: auto-mounting host credentials into a
	// persistent shared sandbox is not a silent default.
	if insideUserHome(resolved) && !isSensitiveHomePath(resolved) {
		if lerr := learnRoot(cfg, resolved); lerr != nil {
			return false, lerr
		}
		ui.InfoF("Auto-mounted %s (learned root; remove with `construct sys daemon roots forget %s`)\n", resolved, resolved)
		return true, nil
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
		// failure downgrades to ask-again-next-run.
		store.DeclineRoot(resolved, time.Now())
		if serr := SaveRootsStore(store); serr != nil {
			ui.InfoF("Could not persist the decline (%v); you may be asked again.\n", serr)
		} else {
			ui.InfoF("Not mounting %s. Construct will not ask about this folder again.\n", resolved)
		}
		return false, declinedError(resolved)
	}

	return true, learnRoot(cfg, resolved)
}

// learnRoot persists a learned root: fresh store load, TouchRoot, LRU
// eviction past the cap, save. The reload matters after an interactive
// prompt (minutes-long wait) and keeps every mutation on the latest
// on-disk set. Caller must hold the daemon flock.
func learnRoot(cfg *config.Config, resolved string) error {
	store, err := LoadRootsStore()
	if err != nil {
		return err
	}
	now := time.Now()
	store.TouchRoot(resolved, now)
	if cfg != nil {
		if evicted := store.EvictLRU(cfg.Daemon.MaxLearnedRoots); len(evicted) > 0 {
			ui.InfoF("Evicted %d learned root(s) past the cap (%d): %v\n",
				len(evicted), cfg.Daemon.MaxLearnedRoots, evicted)
		}
	}
	return SaveRootsStore(store)
}

// isSensitiveHomePath reports whether resolved lives inside a sensitive
// home tree: a directly-nested hidden directory (credential and cache
// trees like ~/.ssh, ~/.aws, ~/.gnupg, ~/.cache — anything below them
// inherits) or macOS ~/Library. Such paths are never auto-learned; they
// keep the interactive checkpoint so no host credential tree lands in
// the shared sandbox without a human saying yes. Hidden dirs DEEPER in
// the tree (~/projects/.env-tooling) are deliberate project locations
// and stay auto-learnable.
func isSensitiveHomePath(resolved string) bool {
	for _, h := range homeCandidates() {
		if resolved == h || !containsPath(h, resolved) {
			continue
		}
		rel, err := filepath.Rel(h, resolved)
		if err != nil || rel == "." {
			continue
		}
		first := rel
		if i := strings.Index(rel, string(os.PathSeparator)); i >= 0 {
			first = rel[:i]
		}
		if strings.HasPrefix(first, ".") || first == "Library" {
			return true
		}
	}
	return false
}

// insideUserHome reports whether resolved lies strictly BELOW the user's
// home directory. The home directory itself does not count (learning it
// would mount the entire home; that keeps the checkpoint flow). Both the
// raw and the symlink-resolved home are accepted as containment roots:
// resolved comes from cleanProjectDir (EvalSymlinks), so on hosts where
// /home is a symlink (Fedora Atomic, /var/root under macOS root) the
// raw-home comparison alone would miss every path. Unknown home counts
// as outside.
func insideUserHome(resolved string) bool {
	if isUserHome(resolved) {
		return false
	}
	for _, h := range homeCandidates() {
		if containsPath(h, resolved) {
			return true
		}
	}
	return false
}

// isUserHome reports whether resolved IS the home directory itself (raw
// or symlink-resolved). Home gets its own regime: always warned, always
// asked, never remembered.
func isUserHome(resolved string) bool {
	for _, h := range homeCandidates() {
		if resolved == h {
			return true
		}
	}
	return false
}

// homeCandidates returns the cleaned raw home plus its symlink-resolved
// form when that differs. Callers must treat an empty result as unknown.
func homeCandidates() []string {
	home := hostHomeDir()
	if home == "" {
		return nil
	}
	candidates := []string{filepath.Clean(home)}
	if r, err := filepath.EvalSymlinks(home); err == nil && r != candidates[0] {
		candidates = append(candidates, r)
	}
	return candidates
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
	if len(store.Accepted) > 0 {
		fmt.Println("\nLarge exports accepted (size warnings skipped for these):")
		acceptedPaths := make([]string, 0, len(store.Accepted))
		for p := range store.Accepted {
			acceptedPaths = append(acceptedPaths, p)
		}
		sort.Strings(acceptedPaths)
		for _, p := range acceptedPaths {
			fmt.Printf("  %s (accepted %s)\n", p, store.Accepted[p].Format(time.RFC3339))
		}
		fmt.Println("  Ask again: construct sys daemon roots forget <path>")
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
		// Forget is a full reset of the folder's relationship with the
		// daemon: any size-warning acceptance rides along.
		store.Unaccept(path)
		store.Unaccept(cleanProjectDir(path))
		if err := SaveRootsStore(store); err != nil {
			ui.GumError(fmt.Sprintf("Failed to save roots store: %v", err))
			os.Exit(1)
		}
		ui.GumInfo(fmt.Sprintf("Forgotten learned root %s. The next ct run recreates the daemon with the smaller mount set.", path))
		return
	}
	// Not a learned root: clear a decline or a large-workspace acceptance
	// if one exists (also the way to drop records whose folder no longer
	// exists on disk, which `roots add` would refuse). Record keys are
	// canonicalized, so try the raw argument first, then its cleaned form.
	clearedDecline := store.Undecline(path) || store.Undecline(cleanProjectDir(path))
	clearedAccept := store.Unaccept(path) || store.Unaccept(cleanProjectDir(path))
	if clearedDecline || clearedAccept {
		if err := SaveRootsStore(store); err != nil {
			ui.GumError(fmt.Sprintf("Failed to save roots store: %v", err))
			os.Exit(1)
		}
		switch {
		case clearedDecline && clearedAccept:
			ui.GumInfo(fmt.Sprintf("Removed decline and size-warning acceptance records for %s. Construct starts fresh with this folder on the next run.", path))
		case clearedDecline:
			ui.GumInfo(fmt.Sprintf("Removed decline record for %s. Construct will offer to mount it again on the next interactive run.", path))
		default:
			ui.GumInfo(fmt.Sprintf("Removed size-warning acceptance for %s. Construct warns and asks again on the next run from that folder.", path))
		}
		return
	}
	ui.GumError(fmt.Sprintf("%s is not a learned, declined, or accepted root. Run `construct sys daemon roots` to list the known set.", path))
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
	// Home is never persistable as a root: a home mount hands the sandbox
	// every user file, so it must stay a per-run, warned decision. If we
	// learned it here, later runs from $HOME would silently skip the
	// always-ask checkpoint.
	if isUserHome(resolved) {
		ui.GumError(fmt.Sprintf("Refusing to add %s: mounting the entire home directory exposes every user file (SSH keys, browser profiles, credentials) to the sandbox. Construct will warn and ask every time you run from it; add a narrower project directory instead.", resolved))
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

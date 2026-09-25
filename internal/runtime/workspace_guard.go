package runtime

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/EstebanForge/construct-cli/internal/ui"
)

// WorkspaceRisk represents the assessed risk level of mounting a directory into a microVM.
type WorkspaceRisk int

// Workspace risk levels.
const (
	// WorkspaceRiskOK indicates a safe workspace directory.
	WorkspaceRiskOK WorkspaceRisk = iota
	// WorkspaceRiskLarge indicates the directory exceeds the entry budget.
	WorkspaceRiskLarge
	// WorkspaceRiskHome indicates the directory is the host user home directory.
	WorkspaceRiskHome
	// WorkspaceRiskSystem indicates the directory is a system root.
	WorkspaceRiskSystem
)

// WorkspaceVerdict holds the outcome of evaluating a candidate workspace path.
type WorkspaceVerdict struct {
	Path     string
	Risk     WorkspaceRisk
	Reason   string
	Entries  int      // Counted before budget exhaustion
	Capped   bool     // True when entry budget was reached
	TimedOut bool     // True when scan duration limit was reached
	Hot      []string // High-risk subdirectories found at top level
}

// DefaultWorkspaceEntryBudget is the maximum entry count before triggering confirmation.
// DefaultWorkspaceEntryBudget bounds the host-side entry scan. Raised
// from 60000 (2026-09-25, research-backed): a single modern JS project
// carries 50-150k files, the kernel raised inotify.max_user_watches to
// 1048576 in 2020 for the same reason, and virtiofs on 2020+ NVMe hosts
// runs real dependency installs ~4x faster than the pre-2022 stack the
// old ceiling dates from. The scan's time budget remains the hard stop.
const DefaultWorkspaceEntryBudget = 500000

const workspaceScanBudget = 3 * time.Second

// hotSubtrees are top-level directory names characteristic of host home or cache trees.
var hotSubtrees = map[string]bool{
	"Library": true, "Applications": true, "Movies": true, "Music": true,
	"Pictures": true, "Downloads": true, ".Trash": true, ".cache": true,
	".npm": true, ".cargo": true, ".rustup": true, ".gradle": true,
	".android": true, ".docker": true, ".orbstack": true,
}

// systemRoots are host root paths that must never be mounted into the microVM.
var systemRoots = map[string]bool{
	"/": true, "/Users": true, "/home": true, "/System": true,
	"/Library": true, "/Applications": true, "/Volumes": true,
	"/private": true, "/private/var": true, "/private/tmp": true, "/private/etc": true,
	"/usr": true, "/etc": true, "/opt": true, "/var": true, "/tmp": true, "/nix": true,
}

// EvaluateWorkspace inspects a candidate workspace directory before mounting.
func EvaluateWorkspace(dir string, budget int) WorkspaceVerdict {
	v := WorkspaceVerdict{Path: dir, Risk: WorkspaceRiskOK}
	if strings.TrimSpace(dir) == "" {
		return v
	}
	if budget <= 0 {
		budget = DefaultWorkspaceEntryBudget
	}

	clean := resolveWorkspaceDir(dir)
	rawClean := filepath.Clean(dir)
	v.Path = clean

	if systemRoots[clean] || systemRoots[rawClean] || clean == string(filepath.Separator) || strings.HasPrefix(clean, "/System/") {
		v.Risk = WorkspaceRiskSystem
		v.Reason = clean + " is a system directory"
		return v
	}

	if home := hostHomeDir(); home != "" {
		homeResolved := home
		if r, err := filepath.EvalSymlinks(home); err == nil {
			homeResolved = r
		}
		if clean == filepath.Clean(home) || clean == filepath.Clean(homeResolved) || rawClean == filepath.Clean(home) {
			v.Risk = WorkspaceRiskHome
			v.Reason = "the workspace is your host home directory"
			v.Hot = topLevelHotSubtrees(clean)
			return v
		}
	}

	// Git repository roots represent an explicit project boundary.
	if isGitRoot(clean) {
		return v
	}

	if hot := topLevelHotSubtrees(clean); len(hot) >= 2 {
		v.Risk = WorkspaceRiskLarge
		v.Hot = hot
		v.Reason = fmt.Sprintf("%s contains host-level user directories (%s)", clean, strings.Join(hot, ", "))
		return v
	}

	v.Entries, v.Capped, v.TimedOut = scanEntryCount(clean, budget, workspaceScanBudget)
	if v.TimedOut {
		v.Risk = WorkspaceRiskLarge
		v.Reason = fmt.Sprintf("%s scan timed out (>%d files scanned)", clean, v.Entries)
	} else if v.Capped {
		v.Risk = WorkspaceRiskLarge
		v.Reason = fmt.Sprintf("%s contains more than %d files", clean, budget)
	}
	return v
}

// ErrWorkspaceRefused indicates a workspace was rejected by guardrail policy.
var ErrWorkspaceRefused = errors.New("workspace refused")

// WorkspacePolicy specifies how workspace risks should be enforced.
type WorkspacePolicy struct {
	AllowHome   bool
	Interactive bool
	Confirm     func(prompt string) bool
}

// EnforceWorkspace checks the verdict against policy, prompting or erroring as needed.
func EnforceWorkspace(v WorkspaceVerdict, p WorkspacePolicy) error {
	switch v.Risk {
	case WorkspaceRiskOK:
		return nil

	case WorkspaceRiskSystem:
		return fmt.Errorf("%w: %s (system roots saturate virtiofs; run construct from a project directory)",
			ErrWorkspaceRefused, v.Reason)

	case WorkspaceRiskHome:
		if p.AllowHome {
			warnWorkspace(v)
			return nil
		}
		return fmt.Errorf("%w: %s (set allow_home_workspace = true in [sandbox] to override)",
			ErrWorkspaceRefused, v.Reason)

	case WorkspaceRiskLarge:
		warnWorkspace(v)
		if !p.Interactive || p.Confirm == nil {
			return fmt.Errorf("%w: %s (non-interactive mode requires smaller workspace or git repository)",
				ErrWorkspaceRefused, v.Reason)
		}
		if !p.Confirm("Export this large directory to the microVM anyway?") {
			return fmt.Errorf("%w: canceled by user", ErrWorkspaceRefused)
		}
	}
	return nil
}

// resolveWorkspaceDir resolves a workspace path the way the guard stores
// and compares it: symlink-resolved, then cleaned. Used by evaluation and
// by the acceptance lookup so both sides see the same key.
func resolveWorkspaceDir(dir string) string {
	resolved := dir
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		resolved = r
	}
	return filepath.Clean(resolved)
}

// EnforceWorkspaceRemembered evaluates the workspace and enforces the
// guard policy with per-root acceptance memory: a folder whose
// large-workspace warning the user already accepted (roots.json accepted
// map) skips the guard entirely — no scan, no warning, no confirm. A
// fresh Yes is persisted after the confirm so later runs stay silent.
// Home and system risks enforce exactly as EnforceWorkspace.
func EnforceWorkspaceRemembered(cwd string, maxEntries int, allowHome, interactive bool) error {
	if rootsStoreAccepts(resolveWorkspaceDir(cwd)) {
		return nil
	}
	verdict := EvaluateWorkspace(cwd, maxEntries)
	confirmed := false
	err := EnforceWorkspace(verdict, WorkspacePolicy{
		AllowHome:   allowHome,
		Interactive: interactive,
		Confirm: func(prompt string) bool {
			ok := workspaceConfirm(prompt)
			confirmed = ok
			return ok
		},
	})
	if err == nil && confirmed {
		if rerr := rememberLargeExport(verdict.Path); rerr != nil {
			ui.LogWarning(fmt.Sprintf("Could not remember workspace acceptance (will ask again): %v", rerr))
		}
	}
	return err
}

// workspaceConfirm is the confirm seam for tests (ui.GumConfirm is a
// plain function and cannot be stubbed).
var workspaceConfirm = ui.GumConfirm

// rootsStoreAccepts reads the roots store without the flock: a torn or
// stale read worst case is one extra confirm, never a lost acceptance.
func rootsStoreAccepts(path string) bool {
	if path == "" {
		return false
	}
	store, err := LoadRootsStore()
	if err != nil {
		return false
	}
	return store.IsAccepted(path)
}

// rememberLargeExport persists a large-workspace acceptance. Acquires the
// daemon flock for the read-modify-write (acquire-then-touch); must NOT
// be called while the flock is already held.
func rememberLargeExport(path string) error {
	if path == "" {
		return nil
	}
	release, err := acquireDaemonLock()
	if err != nil {
		return err
	}
	defer release()
	store, err := LoadRootsStore()
	if err != nil {
		return err
	}
	store.AcceptLargeExport(path, time.Now().UTC())
	return SaveRootsStore(store)
}

func warnWorkspace(v WorkspaceVerdict) {
	ui.InfoF("⚠️  Risky microVM workspace: %s\n", v.Reason)
	if len(v.Hot) > 0 {
		ui.InfoF("   Hostile subtrees: %s\n", strings.Join(v.Hot, ", "))
	}
	ui.InfoLn("   Recursive scans over virtiofs can hang the guest.")
}

func scanEntryCount(dir string, budget int, timeBudget time.Duration) (count int, capped bool, timedOut bool) {
	deadline := time.Now().Add(timeBudget)
	queue := []string{dir}
	for len(queue) > 0 {
		if time.Now().After(deadline) {
			return count, false, true
		}
		cur := queue[0]
		queue = queue[1:]
		entries, err := os.ReadDir(cur)
		if err != nil {
			continue
		}
		for _, e := range entries {
			count++
			if count >= budget {
				return count, true, false
			}
			if e.IsDir() {
				queue = append(queue, filepath.Join(cur, e.Name()))
			}
		}
	}
	return count, false, false
}

func topLevelHotSubtrees(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var found []string
	for _, e := range entries {
		if e.IsDir() && hotSubtrees[e.Name()] {
			found = append(found, e.Name())
		}
	}
	sort.Strings(found)
	return found
}

func isGitRoot(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil && (info.IsDir() || info.Mode().IsRegular())
}

func hostHomeDir() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return h
}

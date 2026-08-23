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
const DefaultWorkspaceEntryBudget = 60000

const workspaceScanBudget = 1500 * time.Millisecond

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

	resolved := dir
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		resolved = r
	}
	clean := filepath.Clean(resolved)
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

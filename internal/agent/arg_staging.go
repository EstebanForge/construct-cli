package agent

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/EstebanForge/construct-cli/internal/safefs"
	"github.com/EstebanForge/construct-cli/internal/ui"
)

// Host path-argument staging for sandboxed agent runs.
//
// Orchestrators (Paseo, IDE extensions) spawn agents with absolute HOST
// paths as flag values: pi receives --extension /var/folders/.../bridge.mjs,
// --mcp-config /var/folders/.../mcp.json, and --session ~/.pi/.../x.jsonl.
// Inside the container those paths do not exist: the sandbox home is the
// bind-mounted construct home (configPath/home -> /home/construct), and
// macOS Docker Desktop does not share /var/folders at all.
//
// Before execution, flagged values that point at existing host files under
// an allowlisted root are staged: copied to
// <construct home>/.construct-staging/<run-id>/<name> and rewritten to the
// matching container path /home/construct/.construct-staging/<run-id>/<name>.
// The construct home is mounted on every run path (daemon exec, compose run,
// msb), so staging works uniformly without docker cp or per-run binds.
// Files staged from a --session flag additionally register a copy-back so
// host tooling (session import, resume from the real host store) sees the
// session progress made inside the sandbox.

const (
	stagingDirRel       = ".construct-staging"
	stagingContainerDir = "/home/construct/.construct-staging"
	maxStageFileBytes   = 8 << 20 // orchestrator bridges and configs are small
	maxStageFiles       = 16
)

// agentPathFlags lists, per agent slug, the flags whose values are host file
// paths passed by orchestrators. v1 covers pi's harness surface; extend per
// agent as new integrations appear.
var agentPathFlags = map[string]map[string]bool{
	"pi": {"--extension": true, "--mcp-config": true, "--session": true},
	// pig 0.2.0 shares pi's harness surface minus --mcp-config (no MCP flag yet).
	"pig": {"--extension": true, "--session": true},
}

// stagedCopyBack pairs a staged container-side file with its original host
// path, for sync-back after the run.
type stagedCopyBack struct {
	staged       string
	host         string
	replacements map[string]any
}

// argStager rewrites host path arguments into container-visible staged
// copies. Zero value is not usable; call newArgStager.
type argStager struct {
	runID         string
	runRoot       string // host side root: <configPath>/home/.construct-staging
	runDir        string // host side per-run: <runRoot>/<run-id>
	callerCwd     string // resolves relative flag values (the caller's cwd)
	allowedRoots  []string
	stagedCount   int
	copyBacks     []stagedCopyBack
	copyFn        func(src, dst string) error // injectable for tests
	newStagedName func(base string, n int) string
}

// newArgStager builds a stager for the agent named by slug. configPath is
// the construct config dir; callerCwd resolves relative flag values.
func newArgStager(slug, configPath, callerCwd string) *argStager {
	flags := agentPathFlags[strings.ToLower(slug)]
	if flags == nil || configPath == "" {
		return nil
	}
	allowed := tempLikeRoots()
	if root := hostAgentConfigRoot(slug); root != "" {
		allowed = append(allowed, root)
	}
	// Relative flag values resolve against the caller's cwd (project-local
	// configs are deliberate orchestrator input), so it is an allowed root.
	if callerCwd != "" {
		allowed = append(allowed, callerCwd)
	}
	return &argStager{
		runRoot:      filepath.Join(configPath, "home", stagingDirRel),
		callerCwd:    callerCwd,
		allowedRoots: allowed,
		copyFn:       copyFileBestEffort,
	}
}

// tempLikeRoots returns the host directory trees where orchestrators place
// their ephemeral bridge files (TMPDIR and the usual temp fallbacks).
func tempLikeRoots() []string {
	roots := []string{}
	if t := os.TempDir(); t != "" {
		roots = append(roots, t)
	}
	roots = append(roots, "/tmp", "/private/tmp", "/var/folders")
	return roots
}

// hostAgentConfigRoot returns the host config root for an agent (pi:
// ~/.pi, honoring PI_CODING_AGENT_DIR's parent; pig: ~/.pig, honoring
// PIG_CODING_AGENT_DIR/PIG_HOME/XDG per pig's own resolution order).
// Empty when unresolvable.
func hostAgentConfigRoot(slug string) string {
	switch strings.ToLower(slug) {
	case "pi":
		dir := hostPiDir() // ~/.pi/agent or PI_CODING_AGENT_DIR
		if dir == "" {
			return ""
		}
		return filepath.Dir(dir)
	case "pig":
		dir := hostPigDir() // ~/.pig/agent or PIG_CODING_AGENT_DIR/PIG_HOME/XDG
		if dir == "" {
			return ""
		}
		return filepath.Dir(dir)
	default:
		return ""
	}
}

// adaptArgs rewrites flagged host path values in place. Values that are not
// existing host files, or that live outside the allowed roots, are kept
// unchanged (the agent will surface its own error, exactly as before this
// feature existed). If staging fails (e.g. copy error or sanitize error),
// the flag is dropped entirely so no raw unsanitized host path reaches the agent.
func (s *argStager) adaptArgs(args []string) []string {
	flags := agentPathFlags[strings.ToLower(firstArgSlug(args))]
	if flags == nil {
		return args
	}
	var out []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--" {
			out = append(out, args[i:]...)
			break // everything after -- belongs to the agent verbatim
		}
		name, value, glued := splitFlagArg(args, i, flags)
		if name == "" {
			out = append(out, args[i])
			continue
		}
		newValue, copyBack, staged, failed, repls := s.stageValue(name, value)
		if failed {
			// Fail-closed: drop the flag and value so no raw unsanitized host path reaches the agent
			if !glued {
				i++ // consume the separate value argument slot as well
			}
			continue
		}
		if !staged {
			out = append(out, args[i])
			continue
		}
		if glued {
			out = append(out, name+"="+newValue)
		} else {
			out = append(out, name, newValue)
			i++ // consumed args[i+1]
		}
		if copyBack {
			s.copyBacks = append(s.copyBacks, stagedCopyBack{
				staged:       filepath.Join(s.runDir, filepath.Base(newValue)),
				host:         value,
				replacements: repls,
			})
		}
	}
	return out
}

// splitFlagArg extracts a candidate (flag, value, glued) at position i when
// the flag is in the allowlist. For separate-value flags the value is
// args[i+1]; the caller must only advance i (adaptArgs re-reads args[i+1]
// after rewrite, which is safe because the rewritten slot is no longer a
// flag). Returns empty name when not an allowlisted flag.
func splitFlagArg(args []string, i int, flags map[string]bool) (name, value string, glued bool) {
	a := args[i]
	if !strings.HasPrefix(a, "--") {
		return "", "", false
	}
	if eq := strings.Index(a, "="); eq > 2 {
		n, v := a[:eq], a[eq+1:]
		if flags[n] {
			return n, v, true
		}
		return "", "", false
	}
	if flags[a] && i+1 < len(args) {
		return a, args[i+1], false
	}
	return "", "", false
}

// firstArgSlug returns the agent slug from the command args (args[0] is the
// agent binary name on construct run paths).
func firstArgSlug(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return filepath.Base(args[0])
}

// stageValue copies the host file referenced by a flagged value into the
// per-run staging dir, sanitizes credentials, and returns the container-side replacement path.
// copyBack is true only for --session (host store must learn about sandbox
// progress). staged is false when the value was left unchanged. failed is true
// when staging encountered an error (copy or sanitize failure) and the flag must be dropped.
func (s *argStager) stageValue(flag, value string) (replacement string, copyBack bool, staged bool, failed bool, replacements map[string]any) {
	if value == "" {
		return value, false, false, false, nil
	}
	hostPath := expandHome(value)
	if !filepath.IsAbs(hostPath) {
		hostPath = filepath.Join(s.callerCwd, hostPath)
	}
	resolved, err := filepath.EvalSymlinks(hostPath)
	if err != nil {
		return value, false, false, false, nil
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return value, false, false, false, nil
	}
	if info.Size() > maxStageFileBytes {
		ui.InfoF("⚠ staging %s skipped: %s exceeds %d bytes\n", flag, resolved, maxStageFileBytes)
		return value, false, false, false, nil
	}
	if !underAnyRoot(resolved, s.allowedRoots) {
		return value, false, false, false, nil
	}
	if s.stagedCount >= maxStageFiles {
		ui.InfoF("⚠ staging %s skipped: file limit reached\n", flag)
		return value, false, false, false, nil
	}
	if err := s.ensureRunDir(); err != nil {
		ui.InfoF("⚠ staging %s skipped: %v\n", flag, err)
		return value, false, false, false, nil
	}
	s.stagedCount++
	namer := s.newStagedName
	if namer == nil {
		namer = defaultStagedName
	}
	stagedName := namer(filepath.Base(resolved), s.stagedCount)
	dst := filepath.Join(s.runDir, stagedName)
	if err := s.copyFn(resolved, dst); err != nil {
		s.stagedCount--
		ui.InfoF("⚠ staging %s failed: %v\n", flag, err)
		return "", false, false, true, nil
	}

	// Sanitize credential JSON and session files. Real keys never enter the guest.
	lower := strings.ToLower(dst)
	if strings.HasSuffix(lower, ".json") || strings.HasSuffix(lower, ".jsonl") {
		data, rErr := os.ReadFile(dst)
		if rErr != nil {
			_ = os.Remove(dst) //nolint:errcheck
			s.stagedCount--
			ui.InfoF("⚠ staging %s failed: read error: %v\n", flag, rErr)
			return "", false, false, true, nil
		}
		sanitized, repls, sErr := sanitizeCredentialDataWithReplacements(data)
		if sErr != nil {
			_ = os.Remove(dst) //nolint:errcheck
			s.stagedCount--
			ui.InfoF("⚠ staging %s failed: credential sanitize error: %v\n", flag, sErr)
			return "", false, false, true, nil
		}
		if wErr := os.WriteFile(dst, sanitized, 0o600); wErr != nil {
			_ = os.Remove(dst) //nolint:errcheck
			s.stagedCount--
			ui.InfoF("⚠ staging %s failed: write error: %v\n", flag, wErr)
			return "", false, false, true, nil
		}
		replacements = repls
	}

	container := stagingContainerDir + "/" + s.runID + "/" + stagedName
	return container, flag == "--session", true, false, replacements
}

// ensureRunDir lazily creates the per-run staging directory (0700).
func (s *argStager) ensureRunDir() error {
	if s.runDir != "" {
		return nil
	}
	id, err := randomID()
	if err != nil {
		return err
	}
	s.runID = id
	s.runDir = filepath.Join(s.runRoot, id)
	return os.MkdirAll(s.runDir, 0o700)
}

// defaultStagedName avoids collisions between repeated flags with the same
// basename while preserving the extension (pi dispatches on it).
func defaultStagedName(base string, n int) string {
	if n <= 1 {
		return base
	}
	ext := filepath.Ext(base)
	return fmt.Sprintf("%s-%d%s", strings.TrimSuffix(base, ext), n, ext)
}

// underAnyRoot reports whether p is at or under one of roots. Roots are
// resolved through symlinks so the macOS /var -> /private/var aliasing
// cannot cause a prefix mismatch with an already-resolved p.
func underAnyRoot(p string, roots []string) bool {
	for _, r := range roots {
		if r == "" {
			continue
		}
		resolved, err := filepath.EvalSymlinks(r)
		if err != nil {
			resolved = r
		}
		if p == resolved || strings.HasPrefix(p, resolved+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}

// copyBackLimit caps one staged-file copy-back: session payloads are
// small text, and an unbounded guest-grown file would fill the host disk
// at teardown.
const copyBackLimit = 64 << 20

// copyFileBestEffort copies a regular file with 0644, refusing symlinks:
// the staged file lives in the guest-writable home bind, and a guest that
// swaps it for a symlink would otherwise have the host copy the target's
// bytes out of the bind (or follow a dangling link to create one).
func copyFileBestEffort(src, dst string) error {
	data, err := safefs.ReadFileRegular(src, copyBackLimit)
	if err != nil {
		return err
	}
	return safefs.WriteFile(dst, data, 0o644)
}

// syncBack copies staged files back to their original host paths, restoring
// original host values for round-tripped sentinels.
func (s *argStager) syncBack() {
	for _, cb := range s.copyBacks {
		data, err := safefs.ReadFileRegular(cb.staged, copyBackLimit)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				ui.LogDebug("staging copy-back failed %s -> %s: %v", cb.staged, cb.host, err)
				continue
			}
			ui.InfoF("⚠ staging copy-back failed %s -> %s: %v\n", cb.staged, cb.host, err)
			continue
		}

		// Restore original host values for round-tripped sentinels
		restored, err := restoreCredentialData(data, cb.replacements)
		if err != nil {
			ui.InfoF("⚠ staging copy-back restore failed %s -> %s: %v\n", cb.staged, cb.host, err)
			continue
		}
		data = restored

		// Refuse copy-back if any unresolved sentinel placeholders remain
		if bytes.Contains(data, []byte("cnstr_sentinel")) {
			ui.InfoF("⚠ staging copy-back refused for %s: contains unresolved sentinel placeholder\n", cb.host)
			continue
		}

		if err := safefs.WriteFile(cb.host, data, 0o644); err != nil {
			ui.InfoF("⚠ staging copy-back failed %s -> %s: %v\n", cb.staged, cb.host, err)
		}
	}
}

// cleanup removes the per-run staging directory.
func (s *argStager) cleanup() {
	if s.runDir != "" {
		_ = os.RemoveAll(s.runDir) //nolint:errcheck // best-effort cleanup
	}
}

func randomID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

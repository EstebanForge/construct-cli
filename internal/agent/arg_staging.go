package agent

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

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
}

// stagedCopyBack pairs a staged container-side file with its original host
// path, for sync-back after the run.
type stagedCopyBack struct {
	staged string
	host   string
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
// ~/.pi, honoring PI_CODING_AGENT_DIR's parent). Empty when unresolvable.
func hostAgentConfigRoot(slug string) string {
	switch strings.ToLower(slug) {
	case "pi":
		dir := hostPiDir() // ~/.pi/agent or PI_CODING_AGENT_DIR
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
// feature existed). All failures are warnings, never run blockers.
func (s *argStager) adaptArgs(args []string) {
	flags := agentPathFlags[strings.ToLower(firstArgSlug(args))]
	if flags == nil {
		return
	}
	for i := 0; i < len(args); i++ {
		if args[i] == "--" {
			break // everything after -- belongs to the agent verbatim
		}
		name, value, glued := splitFlagArg(args, i, flags)
		if name == "" {
			continue
		}
		newValue, copyBack, staged := s.stageValue(name, value)
		if !staged {
			continue
		}
		if glued {
			args[i] = name + "=" + newValue
		} else {
			args[i+1] = newValue
		}
		if copyBack {
			s.copyBacks = append(s.copyBacks, stagedCopyBack{
				staged: filepath.Join(s.runDir, filepath.Base(newValue)),
				host:   value,
			})
		}
	}
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
// per-run staging dir and returns the container-side replacement path.
// copyBack is true only for --session (host store must learn about sandbox
// progress). staged is false when the value was left unchanged.
func (s *argStager) stageValue(flag, value string) (replacement string, copyBack bool, staged bool) {
	if value == "" {
		return value, false, false
	}
	hostPath := expandHome(value)
	if !filepath.IsAbs(hostPath) {
		hostPath = filepath.Join(s.callerCwd, hostPath)
	}
	resolved, err := filepath.EvalSymlinks(hostPath)
	if err != nil {
		return value, false, false
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return value, false, false
	}
	if info.Size() > maxStageFileBytes {
		ui.InfoF("⚠ staging %s skipped: %s exceeds %d bytes\n", flag, resolved, maxStageFileBytes)
		return value, false, false
	}
	if !underAnyRoot(resolved, s.allowedRoots) {
		return value, false, false
	}
	if s.stagedCount >= maxStageFiles {
		ui.InfoF("⚠ staging %s skipped: file limit reached\n", flag)
		return value, false, false
	}
	if err := s.ensureRunDir(); err != nil {
		ui.InfoF("⚠ staging %s skipped: %v\n", flag, err)
		return value, false, false
	}
	s.stagedCount++
	namer := s.newStagedName
	if namer == nil {
		namer = defaultStagedName
	}
	stagedName := namer(filepath.Base(resolved), s.stagedCount)
	dst := filepath.Join(s.runDir, stagedName)
	if err := s.copyFn(resolved, dst); err != nil {
		ui.InfoF("⚠ staging %s failed: %v\n", flag, err)
		return value, false, false
	}
	container := stagingContainerDir + "/" + s.runID + "/" + stagedName
	return container, flag == "--session", true
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

// copyFileBestEffort copies a regular file with 0644.
func copyFileBestEffort(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }() //nolint:errcheck // read-only close
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close() //nolint:errcheck // best-effort close on error path
		return err
	}
	return out.Close()
}

// syncBack copies staged files back to their original host paths. Called
// from Teardown; every failure is a debug log only.
func (s *argStager) syncBack() {
	for _, cb := range s.copyBacks {
		if err := copyFileBestEffort(cb.staged, cb.host); err != nil {
			ui.LogDebug("staging copy-back failed %s -> %s: %v", cb.staged, cb.host, err)
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

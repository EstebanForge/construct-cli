package agent

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/EstebanForge/construct-cli/internal/config"
	"github.com/EstebanForge/construct-cli/internal/safefs"
	"github.com/EstebanForge/construct-cli/internal/ui"
)

// Role-based one-way config sync (host -> construct home), run explicitly via
// `construct sys sync`. Roles, not a receipt, decide overwrite behavior:
//
//   - roleConfig (settings, rules, MCP config, commands): the host copy always
//     wins on content diff, because running the sync means the host is the
//     source of truth. --dry-run shows every planned change first.
//   - roleCredential (auth/token files): pushed only when the guest copy is
//     missing or an empty stub ({}), so a guest-side token rotation is never
//     clobbered by a stale host copy. --force overrides.
//
// Guest paths live inside the guest-writable construct home, so every read of
// an existing guest file goes through safefs and every write through
// safefs.WriteFile: a prompt-injected guest must not be able to turn the
// command into a host file read (planted symlink/FIFO) or write.

type syncRole int

const (
	roleConfig syncRole = iota
	roleCredential
)

// maxSyncFileBytes caps each synced file; config trees should hold nothing
// this large, and the cap keeps a planted FIFO-like surprise bounded.
const maxSyncFileBytes = 8 << 20

type configSyncEntry struct {
	// hostPath resolves the host-side path given the host home dir.
	// Returns "" when the agent is not installed on the host.
	hostPath func(home string) string
	// guestRel is the destination relative to the construct home root,
	// mirroring the guest agent home layout.
	guestRel string
	role     syncRole
	// dir marks a directory push: host files land under guestRel, additive
	// only (guest-only files are never deleted).
	dir bool
}

type syncRow struct {
	slug    string
	entries []configSyncEntry
}

type syncResult struct {
	pushed  int
	skipped int
	err     error
	// missingHost marks a skipped entry whose host source does not exist, so
	// the report can say "not installed" instead of the misleading "up to
	// date".
	missingHost bool
}

func homePath(home string, elems ...string) string {
	return filepath.Join(append([]string{home}, elems...)...)
}

// piAgentDir resolves the host pi agent dir; PI_CODING_AGENT_DIR wins when set
// (mirrors Herdr's own resolution).
func piAgentDir(home string) string {
	if v := strings.TrimSpace(os.Getenv("PI_CODING_AGENT_DIR")); v != "" {
		return expandHome(v)
	}
	return filepath.Join(home, ".pi", "agent")
}

// pigAgentDir resolves the host pig agent dir using pig's own order:
// PIG_CODING_AGENT_DIR, then $PIG_HOME/agent, then $XDG_CONFIG_HOME/pig/agent,
// then ~/.pig/agent.
func pigAgentDir(home string) string {
	if v := strings.TrimSpace(os.Getenv("PIG_CODING_AGENT_DIR")); v != "" {
		return expandHome(v)
	}
	if v := strings.TrimSpace(os.Getenv("PIG_HOME")); v != "" {
		return filepath.Join(expandHome(v), "agent")
	}
	if v := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); v != "" {
		return filepath.Join(expandHome(v), "pig", "agent")
	}
	return filepath.Join(home, ".pig", "agent")
}

// configSyncTable is the per-agent sync registry. Entries only cover files
// with stable, known layouts; an entry whose host path is absent skips
// silently. Sessions, caches, and history are excluded by design.
var configSyncTable = []syncRow{
	{slug: "agy", entries: []configSyncEntry{
		{hostPath: func(h string) string { return homePath(h, ".antigravity", "AGENTS.md") }, guestRel: ".antigravity/AGENTS.md", role: roleConfig},
	}},
	{slug: "claude", entries: []configSyncEntry{
		{hostPath: func(h string) string { return homePath(h, ".claude", "settings.json") }, guestRel: ".claude/settings.json", role: roleConfig},
		{hostPath: func(h string) string { return homePath(h, ".claude", "keybindings.json") }, guestRel: ".claude/keybindings.json", role: roleConfig},
		{hostPath: func(h string) string { return homePath(h, ".claude", "CLAUDE.md") }, guestRel: ".claude/CLAUDE.md", role: roleConfig},
		{hostPath: func(h string) string { return homePath(h, ".claude", "commands") }, guestRel: ".claude/commands", role: roleConfig, dir: true},
		{hostPath: func(h string) string { return homePath(h, ".claude", "agents") }, guestRel: ".claude/agents", role: roleConfig, dir: true},
		{hostPath: func(h string) string { return homePath(h, ".claude.json") }, guestRel: ".claude.json", role: roleConfig},
		{hostPath: func(h string) string { return homePath(h, ".claude", ".credentials.json") }, guestRel: ".claude/.credentials.json", role: roleCredential},
	}},
	{slug: "amp", entries: []configSyncEntry{
		{hostPath: func(h string) string { return homePath(h, ".config", "amp", "settings.json") }, guestRel: ".config/amp/settings.json", role: roleConfig},
		{hostPath: func(h string) string { return homePath(h, ".config", "amp", "AGENTS.md") }, guestRel: ".config/amp/AGENTS.md", role: roleConfig},
	}},
	{slug: "qwen", entries: []configSyncEntry{
		{hostPath: func(h string) string { return homePath(h, ".qwen", "settings.json") }, guestRel: ".qwen/settings.json", role: roleConfig},
		{hostPath: func(h string) string { return homePath(h, ".qwen", "AGENTS.md") }, guestRel: ".qwen/AGENTS.md", role: roleConfig},
		{hostPath: func(h string) string { return homePath(h, ".qwen", "oauth_creds.json") }, guestRel: ".qwen/oauth_creds.json", role: roleCredential},
	}},
	{slug: "copilot", entries: []configSyncEntry{
		{hostPath: func(h string) string { return homePath(h, ".copilot", "AGENTS.md") }, guestRel: ".copilot/AGENTS.md", role: roleConfig},
	}},
	{slug: "opencode", entries: []configSyncEntry{
		{hostPath: func(h string) string { return homePath(h, ".config", "opencode", "opencode.json") }, guestRel: ".config/opencode/opencode.json", role: roleConfig},
		{hostPath: func(h string) string { return homePath(h, ".config", "opencode", "AGENTS.md") }, guestRel: ".config/opencode/AGENTS.md", role: roleConfig},
		{hostPath: func(h string) string { return homePath(h, ".local", "share", "opencode", "auth.json") }, guestRel: ".local/share/opencode/auth.json", role: roleCredential},
	}},
	{slug: "cline", entries: []configSyncEntry{
		{hostPath: func(h string) string { return homePath(h, "Documents", "Cline", "Rules") }, guestRel: "Documents/Cline/Rules", role: roleConfig, dir: true},
		{hostPath: func(h string) string { return homePath(h, "Cline", "Rules") }, guestRel: "Cline/Rules", role: roleConfig, dir: true},
	}},
	{slug: "crush", entries: []configSyncEntry{
		{hostPath: func(h string) string { return homePath(h, ".config", "crush", "crush.json") }, guestRel: ".config/crush/crush.json", role: roleConfig},
		{hostPath: func(h string) string { return homePath(h, ".config", "crush", "AGENTS.md") }, guestRel: ".config/crush/AGENTS.md", role: roleConfig},
	}},
	{slug: "codex", entries: []configSyncEntry{
		{hostPath: func(h string) string { return homePath(h, ".codex", "config.toml") }, guestRel: ".codex/config.toml", role: roleConfig},
		{hostPath: func(h string) string { return homePath(h, ".codex", "AGENTS.md") }, guestRel: ".codex/AGENTS.md", role: roleConfig},
		{hostPath: func(h string) string { return homePath(h, ".codex", "auth.json") }, guestRel: ".codex/auth.json", role: roleCredential},
	}},
	{slug: "droid", entries: []configSyncEntry{
		{hostPath: func(h string) string { return homePath(h, ".factory", "config.json") }, guestRel: ".factory/config.json", role: roleConfig},
		{hostPath: func(h string) string { return homePath(h, ".factory", "AGENTS.md") }, guestRel: ".factory/AGENTS.md", role: roleConfig},
	}},
	{slug: "goose", entries: []configSyncEntry{
		{hostPath: func(h string) string { return homePath(h, ".config", "goose", "config.yaml") }, guestRel: ".config/goose/config.yaml", role: roleConfig},
		{hostPath: func(h string) string { return homePath(h, ".config", "goose", "AGENTS.md") }, guestRel: ".config/goose/AGENTS.md", role: roleConfig},
	}},
	{slug: "kilocode", entries: []configSyncEntry{
		{hostPath: func(h string) string { return homePath(h, ".kilocode", "rules") }, guestRel: ".kilocode/rules", role: roleConfig, dir: true},
	}},
	{slug: "pi", entries: []configSyncEntry{
		{hostPath: func(h string) string { return filepath.Join(piAgentDir(h), "settings.json") }, guestRel: ".pi/agent/settings.json", role: roleConfig},
		{hostPath: func(h string) string { return filepath.Join(piAgentDir(h), "auth.json") }, guestRel: ".pi/agent/auth.json", role: roleCredential},
		{hostPath: func(h string) string { return filepath.Join(piAgentDir(h), "AGENTS.md") }, guestRel: ".pi/agent/AGENTS.md", role: roleConfig},
		{hostPath: func(h string) string { return piAgentDir(h) + string(filepath.Separator) + "extensions" }, guestRel: ".pi/agent/extensions", role: roleConfig, dir: true},
	}},
	{slug: "pig", entries: []configSyncEntry{
		{hostPath: func(h string) string { return filepath.Join(pigAgentDir(h), "settings.json") }, guestRel: ".pig/agent/settings.json", role: roleConfig},
		{hostPath: func(h string) string { return filepath.Join(pigAgentDir(h), "auth.json") }, guestRel: ".pig/agent/auth.json", role: roleCredential},
		{hostPath: func(h string) string { return filepath.Join(pigAgentDir(h), "AGENTS.md") }, guestRel: ".pig/agent/AGENTS.md", role: roleConfig},
		{hostPath: func(h string) string { return pigAgentDir(h) + string(filepath.Separator) + "extensions" }, guestRel: ".pig/agent/extensions", role: roleConfig, dir: true},
	}},
}

// isStubCredential reports whether a guest credential file counts as missing:
// empty or the entrypoint's seeded empty JSON object. A stub must never block
// the first real credential push.
func isStubCredential(data []byte) bool {
	s := strings.TrimSpace(string(data))
	return s == "" || s == "{}"
}

// readHostRegular reads a host-side source, following symlinked leaves (the
// dotfiles manager symlinks config files routinely) while refusing non-regular
// targets and capping size. The host side is user-owned and not guest-writable,
// so only the resolved target's regularity matters here, never symlink
// refusal. Destination reads use safefs.ReadFileRegular instead: that side is
// guest-writable.
func readHostRegular(path string, maxBytes int64) ([]byte, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, err
	}
	return safefs.ReadFileRegular(resolved, maxBytes)
}

func credentialShouldPush(existing []byte, hasExisting bool, force bool) bool {
	if !hasExisting {
		return true
	}
	if isStubCredential(existing) {
		return true
	}
	return force
}

// syncOneEntry syncs one table entry. dryRun reports planned pushes without
// writing. Missing host sources skip silently; a refused guest destination
// (planted symlink, non-regular file) surfaces as res.err and counts skipped.
func syncOneEntry(hostHome, guestHome string, e configSyncEntry, force, dryRun bool) syncResult {
	src := e.hostPath(hostHome)
	if src == "" {
		return syncResult{missingHost: true}
	}
	dst := filepath.Join(guestHome, filepath.FromSlash(e.guestRel))
	if e.dir {
		return syncDirEntry(src, dst, force, dryRun)
	}
	return syncFileEntry(src, dst, e.role, force, dryRun)
}

func syncFileEntry(src, dst string, role syncRole, force, dryRun bool) syncResult {
	data, err := readHostRegular(src, maxSyncFileBytes)
	if err != nil {
		if os.IsNotExist(err) {
			return syncResult{missingHost: true} // host file absent: agent not installed that way
		}
		return syncResult{skipped: 1, err: err}
	}

	existing, err := safefs.ReadFileRegular(dst, maxSyncFileBytes)
	hasExisting := err == nil
	if err != nil && !os.IsNotExist(err) {
		// Planted symlink, FIFO, or oversize file at the destination: refuse
		// rather than read or overwrite through it.
		return syncResult{skipped: 1, err: err}
	}
	if hasExisting && role == roleConfig {
		if sha256.Sum256(existing) == sha256.Sum256(data) {
			return syncResult{}
		}
	} else if hasExisting && role == roleCredential {
		if !credentialShouldPush(existing, hasExisting, force) {
			return syncResult{skipped: 1}
		}
		if sha256.Sum256(existing) == sha256.Sum256(data) {
			return syncResult{}
		}
	}

	mode := os.FileMode(0o644)
	if role == roleCredential {
		mode = 0o600
	}
	if dryRun {
		return syncResult{pushed: 1}
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return syncResult{skipped: 1, err: err}
	}
	if err := safefs.WriteFile(dst, data, mode); err != nil {
		return syncResult{skipped: 1, err: err}
	}
	return syncResult{pushed: 1}
}

// syncDirEntry pushes the host directory's regular files under dst, additive
// only: files that exist only in the guest (e.g. construct-managed extension
// state) are never deleted. Host-side file symlinks are resolved (dotfiles
// managers symlink config files routinely); symlinked or non-regular RESOLVED
// targets are refused by readHostRegular. Destination paths stay hard-refused
// via safefs in syncFileEntry.
func syncDirEntry(srcDir, dstDir string, force, dryRun bool) syncResult {
	fi, err := os.Stat(srcDir)
	if err != nil {
		if os.IsNotExist(err) {
			return syncResult{missingHost: true}
		}
		return syncResult{skipped: 1, err: err}
	}
	if !fi.IsDir() {
		return syncResult{skipped: 1, err: fmt.Errorf("%s: not a directory", srcDir)}
	}

	res := syncResult{}
	err = filepath.WalkDir(srcDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			res.skipped++
			res.err = walkErr
			return nil
		}
		if path == srcDir {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			res.skipped++
			res.err = err
			return nil
		}
		r := syncFileEntry(path, filepath.Join(dstDir, rel), roleConfig, force, dryRun)
		res.pushed += r.pushed
		res.skipped += r.skipped
		if r.err != nil && res.err == nil {
			// Keep the first failure; later ones repeat the same root cause.
			res.err = r.err
		}
		return nil
	})
	if err != nil {
		res.err = err
	}
	return res
}

// selectSyncRows resolves the --agents filter. Empty means every table row;
// duplicate slugs are collapsed.
func selectSyncRows(slugs []string) ([]syncRow, error) {
	if len(slugs) == 0 {
		return configSyncTable, nil
	}
	bySlug := make(map[string]syncRow, len(configSyncTable))
	for _, row := range configSyncTable {
		bySlug[row.slug] = row
	}
	out := make([]syncRow, 0, len(slugs))
	seen := make(map[string]bool, len(slugs))
	for _, s := range slugs {
		row, ok := bySlug[strings.ToLower(s)]
		if !ok {
			return nil, fmt.Errorf("unknown agent %q (supported: %s)", s, strings.Join(agentSlugs(), ", "))
		}
		if seen[row.slug] {
			continue
		}
		seen[row.slug] = true
		out = append(out, row)
	}
	return out, nil
}

func agentSlugs() []string {
	out := make([]string, 0, len(SupportedAgents))
	for _, a := range SupportedAgents {
		out = append(out, a.Slug)
	}
	return out
}

// acquireSyncLock takes an exclusive flock on sync.lock inside dir so two
// concurrent invocations cannot interleave pushes. Returns a release func the
// caller must defer.
func acquireSyncLock(dir string) (func(), error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return func() {}, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "sync.lock"), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return func() {}, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close() //nolint:errcheck // OS releases the flock on close
		return func() {}, err
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) //nolint:errcheck // OS releases on close
			_ = f.Close()                                   //nolint:errcheck // best-effort cleanup
		})
	}, nil
}

// syncOptions carries the parsed `construct sys push` flags.
type syncOptions struct {
	agents []string
	force  bool
	dryRun bool
	yes    bool
	help   bool
}

func parseSyncArgs(args []string) (syncOptions, error) {
	var opts syncOptions
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--force":
			opts.force = true
		case arg == "--dry-run":
			opts.dryRun = true
		case arg == "--yes" || arg == "-y":
			opts.yes = true
		case arg == "--help" || arg == "-h":
			opts.help = true
		case arg == "--agents" || arg == "-a":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--agents requires a comma-separated slug list")
			}
			i++
			opts.agents = strings.Split(args[i], ",")
		case strings.HasPrefix(arg, "--agents="):
			opts.agents = strings.Split(strings.TrimPrefix(arg, "--agents="), ",")
		default:
			return opts, fmt.Errorf("unknown flag %q", arg)
		}
	}
	// Trim, then drop empties so trailing commas and stray spaces never become
	// phantom "unknown agent" errors.
	clean := opts.agents[:0:0]
	for _, s := range opts.agents {
		if s = strings.TrimSpace(s); s != "" {
			clean = append(clean, s)
		}
	}
	opts.agents = clean
	return opts, nil
}

// pushAction decides what a bare or flagged run does. Bare runs adapt to
// context: interactive terminals get plan-then-confirm, scripts get a
// fail-closed preview plus a hint, never a silent write.
type pushAction int

const (
	pushPreview     pushAction = iota // print plan, write nothing
	pushApply                         // apply directly (--yes)
	pushConfirm                       // print plan, then confirm before applying
	pushPreviewHint                   // print plan + "use --yes" hint (scripted bare run)
)

func resolveAction(opts syncOptions, interactive bool) pushAction {
	switch {
	case opts.dryRun:
		return pushPreview
	case opts.yes:
		return pushApply
	case interactive:
		return pushConfirm
	default:
		return pushPreviewHint
	}
}

// SyncAgentConfigs is the `construct sys push` entry point. args are the
// post-namespace CLI args (--agents slug,slug --force --dry-run --yes).
func SyncAgentConfigs(args []string) error {
	opts, err := parseSyncArgs(args)
	if err != nil {
		return err
	}
	if opts.help {
		printPushUsage()
		return nil
	}

	rows, err := selectSyncRows(opts.agents)
	if err != nil {
		return err
	}

	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return fmt.Errorf("cannot resolve host home dir: %w", err)
	}
	guestHome := filepath.Join(config.GetConfigDir(), "home")

	release, err := acquireSyncLock(config.GetConfigDir())
	if err != nil {
		return fmt.Errorf("sync lock: %w", err)
	}
	defer release()

	runPass := func(dryRun bool) int {
		// Applying re-runs the classification, so a file that changed after
		// the preview is re-checked, never written from stale state.
		total := 0
		if dryRun {
			fmt.Println("Dry run - no files will be written:")
		}
		for _, row := range rows {
			perRow := 0
			missingEntries := 0
			var notes []string
			for _, e := range row.entries {
				res := syncOneEntry(home, guestHome, e, opts.force, dryRun)
				perRow += res.pushed
				if res.missingHost {
					missingEntries++
					continue
				}
				if res.skipped > 0 {
					if res.err != nil {
						notes = append(notes, fmt.Sprintf("%s skipped (%v)", e.guestRel, res.err))
					} else {
						notes = append(notes, fmt.Sprintf("%s skipped", e.guestRel))
					}
				}
			}
			total += perRow
			verb := "pushed"
			if dryRun {
				verb = "would be pushed"
			}
			switch {
			case missingEntries == len(row.entries):
				fmt.Printf("%s: not installed on host\n", row.slug)
			case perRow == 0 && len(notes) == 0:
				fmt.Printf("%s: up to date\n", row.slug)
			default:
				fmt.Printf("%s: %d file(s) %s\n", row.slug, perRow, verb)
				for _, n := range notes {
					fmt.Printf("  %s\n", n)
				}
			}
		}
		return total
	}

	interactive := ui.StdinIsTerminal() && ui.GumAvailable()
	switch action := resolveAction(opts, interactive); action {
	case pushPreview:
		runPass(true)
		fmt.Println("Dry run complete. Run with --yes to apply.")
	case pushPreviewHint:
		runPass(true)
		fmt.Println("Nothing written. Re-run with --yes to apply.")
	case pushConfirm:
		if planned := runPass(true); planned == 0 {
			return nil
		} else if ui.GumConfirm(fmt.Sprintf("Apply these %d file(s) now?", planned)) {
			runPass(false)
		} else {
			fmt.Println("Canceled, nothing written.")
		}
	case pushApply:
		runPass(false)
	}
	return nil
}

func printPushUsage() {
	fmt.Println("Usage: construct sys push [--agents slug,slug] [--force] [--dry-run] [--yes]")
	fmt.Println()
	fmt.Println("One-way push of host agent configs into the Construct sandbox:")
	fmt.Println("  settings, rules, MCP config: host copy wins on change")
	fmt.Println("  credentials (auth.json etc): pushed only when missing/stub, or with --force")
	fmt.Println()
	fmt.Println("Flags:")
	fmt.Println("  --agents slug,slug  Push only these agents (default: all)")
	fmt.Println("  --force             Overwrite existing guest credentials with host copies")
	fmt.Println("  --dry-run           Show what would be pushed without writing")
	fmt.Println("  --yes               Push without the interactive confirm")
}

package sys

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/EstebanForge/construct-cli/internal/agent"
	"github.com/EstebanForge/construct-cli/internal/ui"
)

// PATH shims for supported agents: real executable files that route an agent
// command through the Construct sandbox daemon.
//
// Shell aliases only exist inside an interactive shell. Harnesses that manage
// coding agents (Paseo, IDE extensions, CI wrappers) resolve the agent binary
// on PATH or spawn it directly with execve, so they never see aliases or shell
// functions and end up running the bare host binary. A shim file closes that
// gap: for users who always want agents inside the sandbox, `construct sys
// shims --install` makes `pi` (and every other supported agent) a real
// executable that execs `construct <slug>` with the same arguments, cwd, and
// stdio. Each agent also gets an `ns-<slug>` shim that runs the REAL host
// binary directly (non-sandboxed), replacing the ns- shell functions the old
// alias system installed.

// shimMarker must appear in the leading comment block of every shim we write.
// It lets install overwrite and uninstall remove only our own files.
const shimMarker = "# construct-cli shim"

// nsPrefix marks non-sandboxed shims: `ns-pi` runs the real host binary.
const nsPrefix = "ns-"

// defaultShimDir is the conventional per-user executable directory.
func defaultShimDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "bin"), nil
}

// shimPath returns the target file path for an agent slug.
func shimPath(dir, slug string) string {
	return filepath.Join(dir, slug)
}

// isOurShim reports whether the file at path was written by us: the marker
// comment must appear in the leading comment block (after the shebang),
// before the first code line.
func isOurShim(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			if strings.HasPrefix(line, shimMarker) {
				return true
			}
			continue
		}
		return false // first code line reached without our marker
	}
	return false
}

// resolveConstructPath returns the absolute path of the running construct
// binary, preferring the stable ~/.local/bin/ct install if present.
func resolveConstructPath() (string, error) {
	return resolveAliasConstructCommand()
}

// writeShim writes one sandboxed shim for slug, refusing to clobber a file
// that exists and is not one of our shims (unless force is set).
func writeShim(dir, slug, constructPath string, force bool) (string, error) {
	target := shimPath(dir, slug)
	if err := guardTarget(target, force); err != nil {
		return target, err
	}

	script := fmt.Sprintf(`#!/bin/sh
%s (agent: %s). Installed by `+"`construct sys shims`"+`. Do not edit.
# Routes this command through the Construct sandbox. Remove with:
#   construct sys shims --uninstall
exec %q %s "$@"
`, shimMarker, slug, constructPath, slug)

	return target, writeExecutable(target, script)
}

// writeNsShim writes the non-sandboxed counterpart (`ns-<slug>`) that execs
// the real host binary directly, outside the Construct sandbox.
func writeNsShim(dir, slug, realBin string, force bool) (string, error) {
	target := shimPath(dir, nsPrefix+slug)
	if err := guardTarget(target, force); err != nil {
		return target, err
	}

	script := fmt.Sprintf(`#!/bin/sh
%s (agent: %s, non-sandboxed). Installed by `+"`construct sys shims`"+`. Do not edit.
# Runs the host binary directly, outside the Construct sandbox. Remove with:
#   construct sys shims --uninstall
exec %q "$@"
`, shimMarker, slug, realBin)

	return target, writeExecutable(target, script)
}

// guardTarget refuses to overwrite an existing file that is not ours.
func guardTarget(target string, force bool) error {
	_, err := os.Stat(target)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !isOurShim(target) && !force {
		return fmt.Errorf("file exists and is not a construct shim (use --force to overwrite): %s", target)
	}
	return nil
}

// writeExecutable writes the script and guarantees the executable bit.
// The target is removed first: os.WriteFile follows symlinks, so a leftover
// link would otherwise redirect the write into the link's target file and
// corrupt whatever it points at (brew/npm globals are commonly symlinks).
func writeExecutable(target, script string) error {
	if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.WriteFile(target, []byte(script), 0o755); err != nil {
		return err
	}
	return os.Chmod(target, 0o755)
}

// lookPathSkippingDir resolves name on PATH, ignoring the given directory.
// Used to find the REAL agent binary when installing ns- shims: without the
// skip, resolution could return our own sandboxed shim and ns-pi would loop.
func lookPathSkippingDir(name, skipDir string) (string, error) {
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" || dir == skipDir {
			continue
		}
		candidate := filepath.Join(dir, name)
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() {
			continue
		}
		if info.Mode()&0o111 != 0 {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("%s not found on PATH (excluding %s)", name, skipDir)
}

// selectAgents filters SupportedAgents to the requested slugs (all when none).
func selectAgents(only []string) ([]agent.Agent, error) {
	if len(only) == 0 {
		return agent.SupportedAgents, nil
	}
	bySlug := map[string]agent.Agent{}
	for _, a := range agent.SupportedAgents {
		bySlug[a.Slug] = a
	}
	out := make([]agent.Agent, 0, len(only))
	for _, slug := range only {
		a, ok := bySlug[slug]
		if !ok {
			return nil, fmt.Errorf("unknown agent: %s (see `construct agents`)", slug)
		}
		out = append(out, a)
	}
	return out, nil
}

// InstallShims writes executable shims for the selected agents into dir.
// For every agent it also writes an `ns-<slug>` shim that execs the real
// host binary (non-sandboxed), replacing the ns- shell functions that the
// removed alias system used to install. Finally it removes the legacy
// managed alias block from the shell config so both layers never coexist.
func InstallShims(dir string, only []string, force bool) {
	constructPath, err := resolveConstructPath()
	if err != nil {
		ui.GumError(fmt.Sprintf("Could not resolve construct binary path: %v", err))
		os.Exit(1)
	}

	// Warn when the resolved construct path does not point at the running
	// binary. FixCtSymlink repairs a stale ct symlink, but a ct that is a
	// regular file (or a symlink it cannot replace) is passed through as-is;
	// every shim would then exec a dead or outdated construct.
	if resolved, rerr := filepath.EvalSymlinks(constructPath); rerr == nil {
		if self, serr := os.Executable(); serr == nil {
			if selfResolved, srr := filepath.EvalSymlinks(self); srr == nil && resolved != selfResolved {
				fmt.Fprintf(os.Stderr, "⚠ %s does not resolve to the running construct binary (%s); shims will exec it as-is\n", constructPath, selfResolved)
			}
		}
	}

	agents, err := selectAgents(only)
	if err != nil {
		ui.GumError(err.Error())
		os.Exit(1)
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		ui.GumError(fmt.Sprintf("Could not create shim directory %s: %v", dir, err))
		os.Exit(1)
	}

	var failed bool
	for _, a := range agents {
		target, err := writeShim(dir, a.Slug, constructPath, force)
		if err != nil {
			fmt.Fprintf(os.Stderr, "✗ %s: %v\n", a.Slug, err)
			failed = true
		} else {
			fmt.Printf("✓ %s → %s %s\n", target, constructPath, a.Slug)
		}

		// Non-sandboxed counterpart: resolve the real binary, skipping our
		// own shim dir so the sandboxed shim can never shadow the target.
		realBin, lookupErr := lookPathSkippingDir(a.Slug, dir)
		if lookupErr != nil {
			fmt.Fprintf(os.Stderr, "⚠ %s%s skipped: %v\n", nsPrefix, a.Slug, lookupErr)
			continue
		}
		nsTarget, nsErr := writeNsShim(dir, a.Slug, resolveBinaryPath(realBin), force)
		if nsErr != nil {
			fmt.Fprintf(os.Stderr, "✗ %s%s: %v\n", nsPrefix, a.Slug, nsErr)
			failed = true
			continue
		}
		fmt.Printf("✓ %s → %s (non-sandboxed)\n", nsTarget, resolveBinaryPath(realBin))
	}

	// Migrate users off the removed alias system: drop the managed rc block.
	if removed, configFile, rmErr := RemoveLegacyAliasBlock(); rmErr != nil {
		fmt.Fprintf(os.Stderr, "⚠ could not remove legacy shell alias block: %v\n", rmErr)
	} else if removed {
		fmt.Printf("✓ removed legacy shell alias block from %s (backup created)\n", configFile)
	}

	warnIfNotOnPath(dir)
	warnIfShadowed(agents, dir)

	if failed {
		os.Exit(1)
	}
}

// UninstallShims removes our shims (sandboxed and ns-) for the selected
// agents from dir.
func UninstallShims(dir string, only []string) {
	agents, err := selectAgents(only)
	if err != nil {
		ui.GumError(err.Error())
		os.Exit(1)
	}

	var failed bool
	for _, a := range agents {
		for _, name := range []string{a.Slug, nsPrefix + a.Slug} {
			target := shimPath(dir, name)
			if _, err := os.Stat(target); os.IsNotExist(err) {
				continue
			}
			if !isOurShim(target) {
				fmt.Fprintf(os.Stderr, "✗ %s: not a construct shim, left in place\n", target)
				failed = true
				continue
			}
			if err := os.Remove(target); err != nil {
				fmt.Fprintf(os.Stderr, "✗ %s: %v\n", target, err)
				failed = true
				continue
			}
			fmt.Printf("✓ removed %s\n", target)
		}
	}
	if failed {
		os.Exit(1)
	}
}

// ListShims prints the install state of every supported agent's shims.
func ListShims(dir string) {
	fmt.Printf("Shim directory: %s\n\n", dir)
	for _, a := range agent.SupportedAgents {
		target := shimPath(dir, a.Slug)
		nsTarget := shimPath(dir, nsPrefix+a.Slug)
		state := "not installed"
		if _, err := os.Stat(target); err == nil {
			if isOurShim(target) {
				state = "installed"
			} else {
				state = "foreign file (not ours)"
			}
		}
		nsState := "not installed"
		if _, err := os.Stat(nsTarget); err == nil {
			if isOurShim(nsTarget) {
				nsState = "installed"
			} else {
				nsState = "foreign file (not ours)"
			}
		}
		fmt.Printf("  %-12s %-28s sandboxed: %-24s %s: %s\n", a.Slug, a.Name, state, nsPrefix+a.Slug, nsState)
	}
}

// warnIfNotOnPath warns when dir is absent from the current PATH.
func warnIfNotOnPath(dir string) {
	for _, p := range filepath.SplitList(os.Getenv("PATH")) {
		if p == dir {
			return
		}
	}
	fmt.Fprintf(os.Stderr, "⚠ %s is not on your PATH; add it before shims take effect.\n", dir)
}

// warnIfShadowed warns when another executable with the same name wins PATH
// resolution over the freshly installed shim.
func warnIfShadowed(agents []agent.Agent, dir string) {
	for _, a := range agents {
		resolved, err := exec.LookPath(a.Slug)
		if err != nil {
			continue
		}
		if resolved != shimPath(dir, a.Slug) {
			fmt.Fprintf(os.Stderr, "⚠ %s resolves to %s, not the shim in %s\n", a.Slug, resolved, dir)
		}
	}
}

// HandleShimsCommand implements `construct sys shims`.
func HandleShimsCommand(args []string) {
	var dir string
	var only []string
	var install, uninstall, force, removeAliases bool

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--install":
			install = true
		case "--uninstall":
			uninstall = true
		case "--remove-aliases":
			removeAliases = true
		case "--list":
			// default action; explicit flag accepted
		case "--force":
			force = true
		case "--dir":
			i++
			if i >= len(args) {
				ui.GumError("--dir requires a path")
				os.Exit(1)
			}
			dir = args[i]
		case "--agent":
			i++
			if i >= len(args) {
				ui.GumError("--agent requires a slug")
				os.Exit(1)
			}
			only = append(only, args[i])
		default:
			fmt.Fprintf(os.Stderr, "Unknown shims flag: %s\n", args[i])
			printShimsUsage()
			os.Exit(1)
		}
	}

	if dir == "" {
		var err error
		dir, err = defaultShimDir()
		if err != nil {
			ui.GumError(fmt.Sprintf("Could not resolve default shim dir: %v", err))
			os.Exit(1)
		}
	}

	// Standalone cleanup path for users upgrading from the removed alias
	// system who do not want shims: remove the managed rc block and stop.
	if removeAliases {
		if install || uninstall {
			ui.GumError("--remove-aliases cannot be combined with --install or --uninstall")
			os.Exit(1)
		}
		removed, configFile, err := RemoveLegacyAliasBlock()
		if err != nil {
			ui.GumError(fmt.Sprintf("Could not remove legacy shell alias block: %v", err))
			os.Exit(1)
		}
		if removed {
			fmt.Printf("\u2713 removed legacy shell alias block from %s (backup created)\n", configFile)
		} else {
			fmt.Println("No Construct alias block found; nothing to remove.")
		}
		return
	}

	switch {
	case install && uninstall:
		ui.GumError("--install and --uninstall are mutually exclusive")
		os.Exit(1)
	case install:
		InstallShims(dir, only, force)
	case uninstall:
		UninstallShims(dir, only)
	default:
		ListShims(dir)
	}
}

func printShimsUsage() {
	usage := `Usage: construct sys shims --install|--uninstall|--list [options]

Installs real executable shims for supported agents so that tools which
spawn agent binaries directly (no shell: orchestrators, IDE extensions,
CI) run them inside the Construct sandbox.

Options:
  --install          Write shims (default dir: ~/.local/bin)
  --uninstall        Remove previously installed shims
  --remove-aliases   Only remove the legacy managed shell alias block
  --list             Show shim state (default action)
  --dir <path>       Target directory for shims
  --agent <slug>     Limit to one agent (repeatable)
  --force            Overwrite existing files that are not our shims

Per agent two shims are written: '<slug>' routes through the Construct
sandbox (exec construct <slug>, stdin/stdout pass through unchanged so RPC
modes keep streaming JSON) and ' + "ns-<slug>" + ' runs the real host binary
directly (non-sandboxed). Installing also removes the legacy managed shell
alias block that older releases wrote; hand-written shell functions are
never touched.
`
	fmt.Print(usage)
}

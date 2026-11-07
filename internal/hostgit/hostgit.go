// Package hostgit executes hardened git commands on the host.
package hostgit

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

var hookEvents = []string{
	"applypatch-msg", "pre-applypatch", "post-applypatch", "pre-commit", "pre-merge-commit",
	"prepare-commit-msg", "commit-msg", "post-commit", "pre-rebase", "post-checkout", "post-merge",
	"pre-push", "pre-receive", "update", "proc-receive", "post-receive", "post-update",
	"reference-transaction", "push-to-checkout", "pre-auto-gc", "post-rewrite", "sendemail-validate",
	"fsmonitor-watchman", "p4-changelist", "p4-prepare-changelist", "p4-post-changelist", "p4-pre-submit",
	"post-index-change",
}

func hardenedConfig() [][2]string {
	kv := make([][2]string, 0, 7+len(hookEvents))
	kv = append(kv,
		[2]string{"core.fsmonitor", "false"},
		[2]string{"core.hooksPath", os.DevNull},
		[2]string{"core.alternateRefsCommand", "true"},
		[2]string{"protocol.allow", "never"},
		[2]string{"protocol.ssh.allow", "always"},
		[2]string{"protocol.file.allow", "always"},
		[2]string{"maintenance.auto", "false"},
		[2]string{"gc.auto", "0"},
	)
	for _, h := range hookEvents {
		kv = append(kv, [2]string{"hook." + h + ".enabled", "false"})
	}
	return kv
}

// Command prepares a hardened git command on the host.
func Command(ctx context.Context, args ...string) *exec.Cmd {
	kv := hardenedConfig()
	if len(args) > 1 && args[0] == "-C" {
		kv = append(kv, repoOff(ctx, args[1])...)
	}
	return buildCommand(ctx, kv, args)
}

// Output runs git on the host with hardening and returns stdout.
func Output(ctx context.Context, args ...string) ([]byte, error) {
	return Command(ctx, args...).Output()
}

func buildCommand(ctx context.Context, kv [][2]string, args []string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git", args...)
	env := append(sanitizeEnv(os.Environ()),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_NO_LAZY_FETCH=1",
		"GIT_ALLOW_PROTOCOL=file:ssh",
		"GIT_SSH_COMMAND=false",
		"GIT_SSH_VARIANT=ssh",
		"GIT_CONFIG_COUNT="+strconv.Itoa(len(kv)),
	)
	for i, p := range kv {
		n := strconv.Itoa(i)
		env = append(env, "GIT_CONFIG_KEY_"+n+"="+p[0], "GIT_CONFIG_VALUE_"+n+"="+p[1])
	}
	cmd.Env = env
	return cmd
}

func repoOff(ctx context.Context, dir string) [][2]string {
	probeCmd := buildCommand(ctx, hardenedConfig(), []string{"-C", dir, "config", "--name-only", "--get-regexp", `^(filter|hook)\.`})
	out, err := probeCmd.Output()
	if err != nil {
		return nil
	}

	var kv [][2]string
	seen := make(map[string]bool)
	for _, key := range strings.Split(string(out), "\n") {
		key = strings.TrimSpace(key)
		section, name, ok := splitKey(key)
		if !ok || seen[section+"."+name] {
			continue
		}
		seen[section+"."+name] = true
		switch section {
		case "hook":
			kv = append(kv, [2]string{"hook." + name + ".enabled", "false"})
		case "filter":
			for _, k := range []string{"clean", "smudge", "process"} {
				kv = append(kv, [2]string{"filter." + name + "." + k, ""})
			}
			kv = append(kv, [2]string{"filter." + name + ".required", "false"})
		}
	}
	return kv
}

func splitKey(key string) (section, name string, ok bool) {
	section, rest, found := strings.Cut(key, ".")
	if !found {
		return "", "", false
	}
	i := strings.LastIndex(rest, ".")
	if i <= 0 {
		return "", "", false
	}
	return section, rest[:i], true
}

func sanitizeEnv(env []string) []string {
	var out []string
	for _, kv := range env {
		switch {
		case strings.HasPrefix(kv, "GIT_"):
			continue
		case strings.HasPrefix(kv, "PATH="):
			var abs []string
			for _, d := range filepath.SplitList(strings.TrimPrefix(kv, "PATH=")) {
				if filepath.IsAbs(d) {
					abs = append(abs, d)
				}
			}
			out = append(out, "PATH="+strings.Join(abs, string(os.PathListSeparator)))
		default:
			out = append(out, kv)
		}
	}
	return out
}

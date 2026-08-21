# Harness Path-Arg Staging

How orchestrator-driven agents (Paseo, IDE extensions, CI wrappers) run inside the sandbox even though they pass host paths on the command line. For the user-facing setup guide (shims, daemon mounts, PATH), see [HARNESSES.md](HARNESSES.md); this document covers the mechanism.

## The problem

Harnesses spawn agent binaries directly:

```
pi --mode rpc \
   --extension /var/folders/kv/.../paseo-integration.mjs \
   --mcp-config /var/folders/kv/.../mcp.json \
   --session /Users/you/.pi/agent/sessions/--workspaces-.../x.jsonl
```

All three values are absolute HOST paths. Inside the container:

- The sandbox home is the bind-mounted construct home (`~/.config/construct-cli/home` -> `/home/construct`). The user's real home is not mounted, so the `~/.pi` session path does not exist.
- macOS Docker Desktop does not share `/var/folders` at all, so the temp files cannot even be bind-mounted without changing Docker settings.

Without staging, pi dies with `Extension path does not exist` and exits 1.

## The mechanism

`internal/agent/arg_staging.go` runs in `engine.Prepare()` (next to `syncAgentIntegrations`), before any run path branches. For every agent in `agentPathFlags` (v1: pi's `--extension`, `--mcp-config`, `--session`):

1. Resolve the value: `~` expansion, relative paths against the caller's cwd, `EvalSymlinks`.
2. If it is an existing regular file under an allowed root, copy it to `<construct home>/.construct-staging/<run-id>/<name>` (0700 per-run dir; suffix `-N` disambiguates repeated basenames while preserving the extension).
3. Rewrite the argument to `/home/construct/.construct-staging/<run-id>/<name>`.
4. `--session` values additionally register a copy-back; `engine.Teardown()` syncs the staged file back to the original host path so the host store learns what the sandbox wrote.

Allowed roots: `os.TempDir()` plus the usual temp trees, the agent's host config dir (`~/.pi`, honoring `PI_CODING_AGENT_DIR`), and the caller's cwd (project-local configs are deliberate orchestrator input). Caps: 8 MB per file, 16 files per run. Values that are not existing files, or that live outside the roots, are left untouched, which preserves the previous failure mode instead of inventing a new one.

Because the construct home is mounted on every run path (daemon `docker exec`, compose `run`, and the msb backend), staging needs no `docker cp`, no per-run bind mounts, and no Docker file-sharing configuration.

## Known limits

- Resuming a session that originated on the host fails pi's stored-cwd validation inside the container (the session remembers its birth path). Sessions spawned inside the sandbox, which is what orchestrators create, resume normally.
- File CONTENTS are not rewritten: an `mcp.json` that references absolute host paths (e.g. local binaries) still sees those paths inside the container.

## Adding an agent

Extend `agentPathFlags` in `internal/agent/arg_staging.go` with the flags the harness passes as host file paths. Table tests live in `arg_staging_test.go`; cover glued (`--flag=value`), repeated, relative, tilde, and post-`--` cases.

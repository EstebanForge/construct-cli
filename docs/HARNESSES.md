# Using Construct with Agent Harnesses

How to run orchestrator-driven agents (Paseo, IDE extensions, CI wrappers, any tool that spawns agent binaries) inside the Construct sandbox.

## What this enables

Harnesses spawn agent CLIs directly — `pi --mode rpc --extension <bridge file> --session <session file>` — with no shell in between. Construct supports that flow end to end: the agent runs sandboxed, its orchestrator bridge loads, and the RPC stream stays clean.

Requires Construct 1.15.1 or later (path-argument staging).

## Quick start

Three steps, in order:

```bash
# 1. Install PATH shims (sandboxed <slug> + non-sandboxed ns-<slug>)
construct sys shims --install

# 2. Mount the directories your harness will use as workspaces
#    (edit ~/.config/construct-cli/config.toml, then restart the daemon)
[daemon]
multi_paths_enabled = true
mount_paths = ["~/your-projects-root"]

# 3. Warm up once: build the image and install the agent set
construct pi --version
```

Docker must be running. Step 3 takes minutes the first time; spend it at a terminal rather than as a mysterious stall inside your harness's first spawn.

## Why each step matters

**Shims.** Harnesses resolve the agent binary on PATH with a plain execve; shell aliases and functions are invisible to them. `construct sys shims --install` puts a real executable at `~/.local/bin/<slug>` that execs `construct <slug>`, plus `ns-<slug>` for the real host binary when one exists. See the README shims section for flags (`--list`, `--uninstall`, `--force`, `--remove-aliases`).

**Daemon mounts.** The harness spawns agents with `cwd` set to its workspace directory. The daemon only exposes directories listed in `mount_paths` (both keys default to off/empty). Without them the agent starts in the container's default directory — wrong repo, wrong context. One entry per project root is enough; subdirectories are covered.

**Warm-up.** First invocation builds the container image and installs the agent set into it.

## The PATH condition

The agent is spawned with the PATH of the harness's daemon process. If that daemon is launched from your shell, `~/.local/bin` is on it and everything works. If it autostarts headless (login item, launchd), it inherits the system PATH and will find a different `pi` or none at all. The robust fix is the harness's agent-binary override pointing at the shim's absolute path. For Paseo, in `~/.paseo/config.json`:

```json
{
  "agents": {
    "providers": {
      "pi": {
        "command": { "mode": "replace", "argv": ["/Users/you/.local/bin/pi"] }
      }
    }
  }
}
```

Any harness that lets you configure the agent binary (env var or config) accepts the same treatment: point it at the shim file.

## How it works under the hood

Orchestrators pass absolute host paths as flag values (`--extension` and `--mcp-config` point at temp bridge files; `--session` at the host agent store). Those paths do not exist inside the container, so Construct stages the referenced files into the construct home — which is mounted on every run path — and rewrites the arguments to their container paths. Session files are copied back on exit so the host store learns what the sandbox wrote. Mechanism, allowlist, and limits: [HARNESS-STAGING.md](HARNESS-STAGING.md).

## Paseo notes

- Nothing Paseo-side is required beyond the steps above when its daemon inherits your PATH.
- Sessions Paseo creates through the sandbox are "sandbox-born" and resume normally from the phone.
- Agents run with the sandbox's copy of the agent home. Verify once, interactively, that your models and credentials are present: `construct pi` and check the model list.

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `Extension path does not exist`, exit 1 | Construct older than 1.15.1 (no staging), or the harness spawns a foreign `pi` | Update construct; verify with `which pi` inside the harness's context |
| Agent works but in the wrong directory | Workspace dir not in `mount_paths` | Add the project root, restart the daemon |
| First spawn takes minutes | Image build + agent install | One-time warm-up (`construct pi --version`) |
| Harness daemon uses the host binary | Daemon PATH lacks `~/.local/bin` | Binary override pointing at the shim (see PATH condition) |
| Resuming a host-created session fails (stored cwd) | Session remembers its host birth path; known limit | Resume sessions created through the sandbox; see HARNESS-STAGING.md |
| Need the unsandboxed agent | — | `ns-<slug>` (installed when a host binary exists) |

## Adding support for another harness flag

If a harness passes a new host path flag (say, `--skills-dir`), extend `agentPathFlags` in `internal/agent/arg_staging.go` — see the end of [HARNESS-STAGING.md](HARNESS-STAGING.md).

# Host Exec Bridge

This document describes how Construct proxies selected host binaries into the sandbox: the agent invokes them from inside the container, but they actually execute on the host machine.

---

## Overview

Some workflows need a host-only CLI (e.g. `wicket`, which reads host config, secrets, and state) to be callable by the agent inside the sandbox. The Host Exec Bridge lets the user declare a set of binaries that, when invoked from inside the container, are proxied to the host and run there.

This is a **deliberate hole through the sandbox boundary**, sized by whatever binaries the user allowlists. See the [Threat model](#threat-model) section.

---

## How it works

```
Container                                 Host
┌──────────────────────────────┐         ┌─────────────────────────────────────┐
│ Agent runs: wicket foo bar   │         │ Prepare():                          │
│   ↓                          │         │  • resolve binaries via LookPath    │
│ ~/.local/bin/wicket (symlink │◄────────│    (fail closed if missing)         │
│   created host-side)         │         │  • start bridge (token, random port)│
│   → /usr/local/bin/          │         │  • reconcile ~/.local/bin symlinks  │
│      construct-host-exec     │         │    (host-side, flock-guarded)       │
│   argv0=basename="wicket"    │         │                                     │
│   POST host.docker.internal: │────────▶│ Host Exec Bridge (Go HTTP)          │
│        PORT/exec             │         │  1. verify X-Construct-Exec-Token   │
│   body: {argv, stdin}        │         │  2. argv[0] ∈ allowlist? (403)      │
│                              │         │  3. exec resolved abs path          │
│   ◀── stream JSONL chunks ───│─────────│     (Setpgid; ctx=WithTimeout)      │
│      stdout/stderr/exit      │         │  4. stream stdout/stderr JSONL      │
│   parse exit chunk, exit(N)  │         │     (mutex + Flusher)               │
└──────────────────────────────┘         │  5. audit log; ctx.Done → kill(-pgid)│
                                        └─────────────────────────────────────┘
```

The whole pipe is per-session: new token, new random port, fresh symlink reconciliation on every `construct` invocation. Nothing is persisted in the container.

---

## Configuration

```toml
[sandbox]
host_binaries = ["wicket"]
```

- **Off when empty** (default). No bridge starts.
- Requires `construct build` after first enabling, so the container shim (`/usr/local/bin/construct-host-exec`) is baked into the image.
- Per-call timeout override: set `CONSTRUCT_HOST_EXEC_TIMEOUT` (seconds) on the host; default 30 minutes.

---

## Environment variables

Injected into the container per-session (and per `docker exec`):

| Variable | Example | Purpose |
|----------|---------|---------|
| `CONSTRUCT_HOST_EXEC_URL` | `http://host.docker.internal:54247` | Bridge URL. |
| `CONSTRUCT_HOST_EXEC_TOKEN` | `a3f9...` (64-char hex) | Bearer token; required in the `X-Construct-Exec-Token` header. |
| `CONSTRUCT_HOST_BINARIES` | `wicket,foo` | Comma-joined allowlist (so the container knows which names are proxied). |

---

## Protocol

The shim POSTs JSON to `/exec`:

```json
{"argv":["wicket","--version"],"stdin":"<base64>"}
```

The response is a stream of newline-delimited JSON chunks:

```jsonl
{"type":"stdout","data":"<base64>"}
{"type":"stderr","data":"<base64>"}
{"type":"exit","code":0}
```

- Each `stdout`/`stderr` frame corresponds to **one read from the child's pipe**, base64-encoded (binary-safe; handles `\r` progress bars). Order within each stream is preserved; order between stdout and stderr is best-effort.
- `exit` carries the real exit code, or `124` if the bridge killed the child (timeout or client disconnect).
- `401` = bad/missing token. `403` = `argv[0]` not in the allowlist.

---

## Daemon reuse

Construct's daemon is long-lived and reused across invocations. The bridge stays correct without any in-container state:

- **Token/port**: regenerated each `Prepare()` and injected fresh into every `docker exec` env (`-e` flags). The daemon never caches a stale token. The shim reads its env at invocation time, so it always sees the current session's URL/token.
- **Symlinks**: reconciled **host-side** in `Prepare()` (no `docker exec`). `~/.local/bin` is bind-mounted from the host home, so adding/removing entries in `host_binaries` takes effect on the next `construct` invocation without a daemon restart or rebuild.

---

## Debugging

Audit log on the host: `~/.config/construct-cli/logs/host_exec.log` (always-on; timestamp, argv, resolved path, exit code, duration).

`construct sys doctor` warns when:
- `host_binaries` is set but the shim isn't in the image (run `construct build`).
- Manifest contains entries no longer in the config (stale symlinks).

### Common failure patterns

**Agent gets `127: command not found` for a listed binary:**
- The image is old and doesn't contain `/usr/local/bin/construct-host-exec`. Run `construct build`. (Note: this surfaces as bash's `127`, not the shim's `126`, because the shim itself never gets to run.)

**Agent gets `126`:**
- The bridge didn't start or is unreachable. Check `host_exec.log` on the host; confirm the binary resolves on your host PATH (`which wicket`).

**Output looks wrong / no colors / no progress bar:**
- There is no controlling terminal (PTY). Many CLIs degrade gracefully; pass `--no-interactive`, `--no-color`, or `--json` if available.

---

## Threat model

- **Trust boundary**: anything in the container (agent, installed packages, npm/rust deps) can call any allowlisted binary with **any argv** once listed.
- **Token**: prevents unrelated local processes / adjacent containers from calling the bridge.
- **Allowlist**: prevents the container from running arbitrary *unlisted* host commands.
- **Resolve-once**: binaries are resolved to absolute paths at `Prepare()` time, preventing PATH/argv manipulation from redirecting a listed name at request time.
- **Token-in-env implication**: any process inside the container can read `CONSTRUCT_HOST_EXEC_TOKEN`/`URL` from its own env and call the bridge directly with arbitrary argv. The shim provides no additional security boundary over a compromised dependency just hitting the HTTP endpoint. (Per-binary token scoping was considered and rejected: it doesn't help, since all env vars are readable by the same process.)
- **Execution identity**: resolved binaries run as the **host user running `construct`**, not root and not the container's `construct` user. Files they write on shared bind-mounted paths are owned by the host user; the container may need to read/edit them (same situation as `propagate_git_identity`).
- **NOT defended**: what a listed binary does with its argv. If the user lists `docker`, the agent can `docker run --privileged` and own the host. The startup banner and docs make this explicit. Accepted residual risk.

---

## Limitations (v1)

- **No interactive TTY / signal forwarding.** No controlling terminal, no Ctrl-C propagation to the host process. Pipe stdin (one-shot) works; live interactive prompts do not. (Live interactive input is also structurally incompatible with the request/response transport; a future TTY effort would need a different transport.)
- **No per-binary argv filtering.**
- **No per-binary token scoping.**
- **No Windows host support.**
- **30-minute hard cap** per call (override via `CONSTRUCT_HOST_EXEC_TIMEOUT`).
- **Linux bind is `0.0.0.0`** so the container can reach the host; the per-session token is what makes this safe.

---

## Design rationale

See git history for the design record (peer review, decision log, threat model, task breakdown).

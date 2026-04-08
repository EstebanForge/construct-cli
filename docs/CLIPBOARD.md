# Clipboard System

This document describes the complete clipboard architecture for Construct CLI — how images travel from the host clipboard into each containerized agent, every quirk encountered, and why each decision was made.

---

## Table of Contents

1. [Architecture Overview](#architecture-overview)
2. [Host Clipboard Server](#host-clipboard-server)
3. [Environment Variables Reference](#environment-variables-reference)
4. [Container Entrypoint Setup](#container-entrypoint-setup)
5. [Agent-Patch Functions](#agent-patch-functions)
6. [Clipper Shim](#clipper-shim)
7. [Per-Agent Behaviour](#per-agent-behaviour)
8. [Copilot: The Full Story](#copilot-the-full-story)
9. [Debugging](#debugging)
10. [Known Quirks & Gotchas](#known-quirks--gotchas)
11. [Data Flow Diagram](#data-flow-diagram)

---

## Architecture Overview

Agents run inside a Docker container. The container has no display server, no clipboard daemon, and no access to the host's native clipboard APIs. To bridge this gap, Construct runs a lightweight HTTP server on the host that serves clipboard content on demand.

Inside the container, two mechanisms redirect clipboard requests to that server:

- **Clipper shim** — A bash script symlinked as `xsel`, `xclip`, and `wl-paste`. Any agent or library that shells out to these tools hits the shim instead, which fetches from the host server.
- **Agent-specific patches** — JS or Python modifications applied at session start that redirect native clipboard API calls (Node NAPI addons, Ink TUI key handlers, etc.) to the host server.

Whether an agent receives the image as **raw PNG bytes** or as a **file path reference** (`@path/to/image.png`) depends on what each agent understands:

| Agent | Paste Mode | Mechanism |
|-------|-----------|-----------|
| Gemini | File path (`@path`) | Clipper shim |
| Qwen | File path (`@path`) | Clipper shim |
| Codex | File path | Python PTY wrapper |
| Claude | Raw bytes | Clipper shim → raw PNG stream |
| Pi | Raw bytes | Clipper shim → raw PNG stream |
| **Copilot** | File path (`@path`) | Python PTY wrapper (see below) |

Copilot and Codex use PTY wrappers as their primary paste path. Copilot details are in [Copilot: The Full Story](#copilot-the-full-story).

---

## Host Clipboard Server

**Source:** `internal/clipboard/server.go`

A Go HTTP server that starts on a random port when an agent session begins. It reads from the host clipboard (macOS via `osascript`, Linux via `xclip`/`wl-paste`, Windows via PowerShell) and serves the content to the container.

### Startup

```go
StartServer(host string) (*Server, error)
```

- Binds to `0.0.0.0:0` (OS-assigned random port).
- Generates a 32-byte cryptographically random token (hex-encoded, 64 chars).
- Default host: `host.docker.internal` (Docker's magic hostname for reaching the host).
- Logs to `~/.config/construct-cli/logs/clipboard_server.log` (always-on).

### HTTP Endpoint: `GET /paste`

**Authentication:** `X-Construct-Clip-Token: <TOKEN>` header required. Returns `401` on mismatch.

**Query parameters:**
- `?type=image/png` — Returns raw PNG bytes from host clipboard.
- `?type=text/plain` (default) — Returns plain text from host clipboard.

**Response codes:**
- `200` — Content served.
- `404` — Clipboard is empty or does not contain the requested type.
- `401` — Token missing or wrong.
- `500` — Internal error (e.g., host tool not available).

**Timeouts:** Read 15s, Write 15s, Idle 30s.

---

## Environment Variables Reference

All variables below are injected into the container at session start by `internal/agent/runner.go`.

| Variable | Example Value | Purpose |
|----------|--------------|---------|
| `CONSTRUCT_CLIPBOARD_URL` | `http://host.docker.internal:54247` | Full URL of host clipboard server. |
| `CONSTRUCT_CLIPBOARD_TOKEN` | `a3f9...` (64-char hex) | Auth token for `/paste` requests. |
| `CONSTRUCT_FILE_PASTE_AGENTS` | `gemini,qwen,codex` | Agents that get file-path mode in the clipper shim. |
| `CONSTRUCT_CLIPBOARD_IMAGE_PATCH` | `1` or `0` | Gates `agent-patch.sh` execution in the entrypoint. |
| `CONSTRUCT_AGENT_NAME` | `copilot`, `claude`, `gemini`… | Agent identity, used by clipper to decide paste mode. |
| `XDG_SESSION_TYPE` | `wayland` | Forces native clipboard modules (Claude, Copilot, Pi) through `wl-paste` → clipper shim. |
| `CONSTRUCT_DEBUG` | `1` | Optional. Enables verbose logging in clipper, JS bridge, entrypoint. |

`CONSTRUCT_FILE_PASTE_AGENTS` is sourced from `internal/constants/constants.go`:

```go
const FileBasedPasteAgents = "gemini,qwen,codex"
```

Note: Claude and Copilot are intentionally **absent** from this list. Claude's native clipboard module handles raw bytes. Copilot's PTY wrapper handles its own path injection independently.

---

## Container Entrypoint Setup

**Source:** `internal/templates/entrypoint.sh`

On first container start (or after entrypoint hash changes), the entrypoint:

1. **Installs clipper symlinks (as root, early boot):**
   ```bash
   ln -sf /usr/local/bin/clipper /usr/bin/xclip
   ln -sf /usr/local/bin/clipper /usr/bin/xsel
   ln -sf /usr/local/bin/clipper /usr/bin/wl-paste
   ```

2. **Runs `agent-patch.sh`** (if `CONSTRUCT_CLIPBOARD_IMAGE_PATCH != 0`):
   ```bash
   PATCH_SCRIPT="/home/construct/.config/construct-cli/container/agent-patch.sh"
   bash "$PATCH_SCRIPT"
   ```
   This is where JS patches and the PTY wrapper are installed.

3. **Optionally starts X11 bridge** (not for Codex):
   - Spawns `Xvfb :0` (virtual framebuffer) in the background.
   - Starts `clipboard-x11-sync.sh` which mirrors the host clipboard into the virtual X11 session, for agents that use X11 clipboard APIs directly.

---

## Agent-Patch Functions

**Source:** `internal/templates/agent-patch.sh`

This script runs on every container session start. Each function is idempotent (version-string guards prevent double-patching). The script is deployed to `~/.config/construct-cli/container/agent-patch.sh` inside the container and re-deployed whenever the binary is updated.

---

### `fix_clipboard_libs()`

Replaces clipboard tool binaries inside npm packages so that their bundled `xsel`/`xclip` copies also hit the clipper shim.

Some npm packages (notably `clipboardy`) bundle their own copies of `xsel` or `xclip` under `lib/node_modules/<pkg>/fallbacks/linux/`. Without this fix, those agents use their own bundled binary and never touch `/usr/bin/xsel` (our symlink).

**What it does:**
- Searches `~/.npm-global` and `/home/linuxbrew/.linuxbrew` for `clipboardy/fallbacks/linux/` directories.
- For each found: replaces `xsel` and `xclip` with symlinks to `/usr/local/bin/clipper`.
- Also does an aggressive search for any loose `xsel` binary in `~/.npm-global` and replaces it.

---

### `patch_agent_code()`

Some agents gate clipboard code behind `process.platform !== "darwin"` checks, assuming Linux means no clipboard support. This patch rewrites those checks to `false` so the code runs on Linux.

**What it does:**
- Scans all `.js` files in npm-global that contain both `process.platform` and `darwin`.
- Replaces `process.platform !== "darwin"` → `false` (both single- and double-quote variants).

---

### `patch_copilot_clipboard()`

**Version string:** `construct-copilot-clipboard-bridge-v3`

Replaces Copilot's native NAPI-RS clipboard addon (`@teddyzhu/clipboard`) with a pure-JavaScript drop-in that fetches from the host bridge.

**Why:** `@teddyzhu/clipboard` is a compiled native Node.js addon. Its `.node` binary is built for a specific platform and will not run in a headless Linux container without a display. It fails immediately on load.

**What it does:**
- Finds `$HOME/.npm-global/lib/node_modules/@teddyzhu/clipboard/index.js`.
- Replaces the entire file with a pure-JS implementation that exports an identical API (`ClipboardManager` class, `getClipboardImageData`, `getClipboardFiles`, `getClipboardText`, etc.).
- All read methods use `curl` to fetch from `CONSTRUCT_CLIPBOARD_URL` with the token header.
- All write methods (`setClipboardText`, `setClipboardImage`, etc.) are no-op stubs.
- `ClipboardListener` is a stub (no event watching).
- Logs to `/tmp/construct-copilot-clipboard.log` (visible in `sys clipboard-debug`).

**Note:** This patch is still applied and the version string is still checked, but **Copilot no longer uses this bridge for image paste**. Image paste now goes through the PTY wrapper (see below). The JS bridge remains in place as a fallback layer and for text clipboard operations.

---

### `patch_copilot_keybinding()`

**Version string:** `construct-copilot-keybinding-v1`

Patches Copilot's TUI input handler so Ctrl+V works on Linux (in addition to Meta+V).

**Why:** Copilot uses Ink (React for CLI terminals). On non-darwin platforms, its paste handler requires `fe.meta` (Meta/Alt key). On Linux this means Alt+V, which conflicts with terminal emulator shortcuts.

**What it does:**
- Finds `$HOME/.npm-global/lib/node_modules/@github/copilot/app.js`.
- Rewrites the paste key check:
  - Before: `(CLi()!=="darwin"?fe.meta:fe.ctrl)&&fe.name==="v"`
  - After: `(CLi()!=="darwin"?fe.ctrl||fe.meta:fe.ctrl)&&fe.name==="v"`

**Note:** Like the JS bridge, this patch remains applied but is not the primary paste path anymore. The PTY wrapper intercepts the keypress before it even reaches Copilot's Ink input handler.

---

### `patch_copilot_paste_wrapper()`

**Version string:** `construct-copilot-wrapper-v9`

This is the main Copilot image paste mechanism. Installs a Python 3 PTY wrapper in place of the Homebrew `copilot` binary that intercepts paste keystrokes before Copilot's process ever sees them.

See [Copilot: The Full Story](#copilot-the-full-story) for the complete explanation.

---

## Clipper Shim

**Source:** `internal/templates/clipper`

A bash script deployed to `/usr/local/bin/clipper` and symlinked as `/usr/bin/xsel`, `/usr/bin/xclip`, and `/usr/bin/wl-paste`. When any agent or library calls one of these tools, the shim runs instead.

### Mode Decision

The shim checks `CONSTRUCT_AGENT_NAME` against `CONSTRUCT_FILE_PASTE_AGENTS`:

```bash
if [[ ",$FILE_PASTE_AGENTS," == *",$AGENT_NAME,"* ]]; then
    PATH_AGENT=true   # emit @/path/to/image.png
else
    PATH_AGENT=false  # emit raw PNG bytes
fi
```

### File-path mode (`PATH_AGENT=true` — Gemini, Qwen, Codex legacy)

1. Fetch image from host: `curl -sS -H "X-Construct-Clip-Token: TOKEN" URL/paste?type=image/png`
2. Save to `.construct-clipboard/clipboard-{timestamp}.png` in the current working directory.
3. Emit `@{IMG_PATH}` to stdout (plain path for Codex in legacy clipper mode, `@path` for others).

### Raw-bytes mode (`PATH_AGENT=false` — Claude, Pi)

1. Fetch image from host.
2. Check dimensions via ImageMagick `identify`. If > 8000×8000 px, resize via `convert` before emitting.
3. Emit raw PNG bytes to stdout.

### TARGETS listing

If called with `TARGETS`, `atoms`, `-l`, or `--list-types`, emits a MIME type list:
```
TIMESTAMP
TARGETS
MULTIPLE
UTF8_STRING
STRING
TEXT
image/png
image/tiff
```

### Logging

Always logs to `$CONSTRUCT_CLIPBOARD_LOG` (default: `/tmp/construct-clipper.log`).

---

## Per-Agent Behaviour

### Gemini, Qwen

- **Paste mode:** File path (`@path`).
- **Mechanism:** Clipper shim (xsel/xclip/wl-paste replacement).
- **No special patches required** beyond the shim and `fix_clipboard_libs`.

### Claude Code

- **Paste mode:** Raw bytes.
- **Mechanism:** Clipper shim in raw-bytes mode.
- **Extra env:** `XDG_SESSION_TYPE=wayland` so Claude's native clipboard module calls `wl-paste` (our shim) instead of trying X11.
- **No PTY wrapper** — Claude's Ink TUI handles image paste natively once it gets raw bytes from `wl-paste`.

### Pi (Oh My Pi)

- **Paste mode:** Raw bytes.
- **Mechanism:** Clipper shim in raw-bytes mode.
- **Extra env:** `XDG_SESSION_TYPE=wayland` (same reasoning as Claude).

### Codex

- **Paste mode:** File path.
- **Mechanism:** Python PTY wrapper (`construct-codex-wrapper-v1`) installed by `agent-patch.sh`.
- **Behavior:** Intercepts paste keystrokes, fetches image from the host clipboard bridge, saves into `.construct-clipboard/`, and injects the file path as typed text.

### GitHub Copilot

- **Paste mode:** File path (`@path`) — injected as `@.construct-clipboard/clipboard-{ts}.png`.
- **Mechanism:** Python PTY wrapper.
- **Also patched:** `@teddyzhu/clipboard` JS bridge (fallback layer) and Ink keybinding (cosmetic).
- See full explanation below.

---

## Copilot: The Full Story

Copilot was the hardest agent to get working. Here is the complete chronology of what was tried and why, so future maintainers don't repeat the same dead ends.

### Why the JS bridge alone doesn't work

Copilot uses `@teddyzhu/clipboard` (a NAPI-RS native addon) to read the clipboard when the user pastes. In a headless Linux container the native `.node` binary fails to load. We replaced `index.js` with a pure-JS bridge that fetches from the host server. **But Copilot's paste handler never called this bridge.** The call chain requires a UI event (key press detected by Ink) to trigger the clipboard read. Ink's paste handler on Linux requires `fe.meta` (Meta+V) or `fe.ctrl` (Ctrl+V depending on platform). In practice, the event never fired because:

1. The Docker TTY chain mutes or transforms certain key sequences.
2. The clipboard library loads but the event was never dispatched in the headless context.

### Why keybinding patches alone don't work

We patched `app.js` to accept `fe.ctrl` on Linux. This should have allowed Ctrl+V to trigger paste. It didn't, for the same reason: Copilot's Ink TUI input layer never received the keypress in a way it could act on, because the PTY environment inside Docker changes how raw key bytes are delivered.

### PATH shadowing problem

Early PTY wrapper attempts installed the wrapper at `~/.local/bin/copilot`. This was silently bypassed because `/home/linuxbrew/.linuxbrew/bin` comes first in PATH (it's where Homebrew installs the `copilot` npm bin script). The wrapper was never reached.

**Fix:** Detect where `copilot` actually resolves via `command -v copilot`, and install the wrapper there.

### Symlink overwrite problem

When we detected `/home/linuxbrew/.linuxbrew/bin/copilot` and ran `cat > "$real_copilot"` to install the wrapper, we were writing **through the symlink to its target** (the npm package's bin script inside `lib/node_modules/`). This corrupted the package binary. Additionally, the `cp copilot copilot-real` backup ran from the Homebrew bin directory, so when Node later tried to `import('./index.js')` from `copilot-real`, it looked for `index.js` in `/home/linuxbrew/.linuxbrew/bin/` — not in the npm package directory where it actually lives.

**Fix:** `rm -f` the Homebrew bin path first (removes the symlink itself, not its target), then `cat >` creates a new regular file there. The real binary path (`~/.npm-global/bin/copilot`) is found separately via npm-global candidates and injected into the wrapper via `sed` at install time.

### The Kitty Keyboard Protocol discovery

With the PTY wrapper correctly installed, the wrapper ran, the logs confirmed it was active and waiting for `\x16` (traditional Ctrl+V). But pressing Ctrl+V or Cmd+V in the terminal produced no `ctrl+v detected` log entry.

Adding hex-dump logging to every `os.read()` call revealed:

```
\x1b[118;9u  ← Cmd+V  (KKP: key=118='v', modifier=9=Super)
\x1b[118;5u  ← Ctrl+V (KKP: key=118='v', modifier=5=Ctrl)
```

**Ghostty** (and other modern terminals) use the **Kitty Keyboard Protocol (KKP)**, which encodes modifier keys in CSI-u format instead of sending legacy control bytes. `\x16` is never sent.

KKP modifier encoding: `modifier = (bitmask + 1)` where Shift=1, Alt=2, Ctrl=4, Super=8.
- Modifier 5 = Ctrl (4+1)
- Modifier 9 = Super/Cmd (8+1)

**Fix:** The wrapper now intercepts all three variants:

```python
_PASTE_TRIGGERS = [b'\x16', b'\x1b[118;5u', b'\x1b[118;9u']
```

### How the PTY wrapper works (v9)

**Installation:**

1. `command -v copilot` finds the active copilot binary (e.g., `/home/linuxbrew/.linuxbrew/bin/copilot`).
2. Idempotency check: skip if `construct-copilot-wrapper-v9` already in the file.
3. Find real copilot binary via npm-global: `~/.npm-global/bin/copilot` (a symlink into the npm package dir — Node resolves relative imports from the symlink's target directory, which is why we cannot use `readlink -f` here).
4. `rm -f <homebrew-bin>` — removes symlink or file.
5. Write Python wrapper to that path.
6. `sed -i "s|__CONSTRUCT_REAL_COPILOT__|${real_target}|"` — inject the real copilot path.
7. `chmod +x`.

**Runtime operation:**

```
User terminal (Ghostty)
    │
    │  Ctrl+V or Cmd+V (arrives as \x1b[118;5u or \x1b[118;9u)
    ▼
Python wrapper (outer PTY, fd_in)
    │
    ├─ _handle_paste(data) detects trigger sequence
    │      │
    │      ├─ curl fetches PNG from host bridge (CONSTRUCT_CLIPBOARD_URL)
    │      ├─ saves to .construct-clipboard/clipboard-{timestamp}.png
    │      └─ replaces trigger with "@.construct-clipboard/clipboard-{ts}.png "
    │
    ▼ (modified data forwarded to inner PTY)
Real copilot process (inner PTY, mfd)
    │
    └─ Sees "@.construct-clipboard/..." as typed text
       Copilot recognises @file syntax and reads the image
```

The wrapper creates a PTY pair (`pty.openpty()`), spawns the real copilot on the slave side, and bridges the outer Docker stdin to the inner PTY master — intercepting keystrokes in between. Window resize signals (`SIGWINCH`) are forwarded. If stdin is not a TTY (non-interactive exec), the wrapper falls through to `os.execv(real, args)` with no interception.

**Logging:** `~/.config/construct-cli/logs/construct-copilot-wrapper.log` (always-on, host-mounted path, survives container teardown).

---

## Debugging

Run `construct sys clipboard-debug` from the host. It executes a diagnostic bash script inside the container and shows:

1. **Host server log** (last 50 lines of `~/.config/construct-cli/logs/clipboard_server.log`) — confirms the server started and whether any fetch requests arrived.
2. **Container env** — `CONSTRUCT_CLIPBOARD_URL`, `CONSTRUCT_CLIPBOARD_TOKEN` (truncated), `CONSTRUCT_FILE_PASTE_AGENTS`, `CONSTRUCT_CLIPBOARD_IMAGE_PATCH`, `XDG_SESSION_TYPE`, etc.
3. **Clipper shim state** — Are `/usr/bin/wl-paste`, `xclip`, `xsel` symlinked to `/usr/local/bin/clipper`?
4. **Clipper log** — `/tmp/construct-clipper.log` (last 40 lines). Created on first paste.
5. **Copilot wrapper** — `which copilot` resolved path, version string check (`construct-copilot-wrapper-v9`), executable bit, shebang, `_REAL` path.
6. **Codex wrapper** — `which codex` resolved path, version string check (`construct-codex-wrapper-v1`), executable bit, shebang, `_REAL` path.
6. **Copilot wrapper log** — `~/.config/construct-cli/logs/construct-copilot-wrapper.log` (last 40 lines). Created on first Copilot session.
7. **Copilot JS bridge** — Which `@teddyzhu/clipboard/index.js` files exist, whether `construct-copilot-clipboard-bridge-v3` marker is present.
8. **Temp clipboard files** — Any `.png` files left in `/tmp`.
9. **X11 clipboard sync** — Whether `Xvfb` or `clipboard-x11-sync` is running.

### Common failure patterns

**Server starts but no requests arrive:**
- The wrapper or shim is not running (check wrapper log and shim symlinks).
- `host.docker.internal` is not reachable (try `curl http://host.docker.internal:PORT/paste` inside the container).

**Server shows `serving image: 0 bytes` or `GetText error`:**
- Host clipboard is empty or does not contain a PNG image.
- Copy an actual image (screenshot, screen capture) to clipboard first.

**Wrapper log shows `ctrl+v detected` but `save_image: fetch failed`:**
- Token mismatch (server was restarted since wrapper's env vars were set).
- Bridge server not running (`sys rebuild` to restart everything).

**Wrapper log shows `not a tty — exec direct`:**
- Copilot was started in a non-interactive context. PTY interception requires a real TTY.

**Copilot shows v7/v6 after rebuild:**
- The Docker named volume (`construct-packages`) persists the old binary. `sys rebuild` should re-run `agent-patch.sh` which reinstalls the wrapper. If it still shows old version, check the setup log for errors.

**`_REAL` shows `__CONSTRUCT_REAL_COPILOT__` (placeholder not replaced):**
- `npm bin -g` failed and `~/.npm-global/bin/copilot` did not exist at patch time. Copilot was not installed when `agent-patch.sh` ran. `sys rebuild` to force reinstall.

---

## Known Quirks & Gotchas

**Ghostty / KKP terminals:**
Modern terminals using the Kitty Keyboard Protocol never send `\x16` for Ctrl+V or Cmd+V. They send `\x1b[118;5u` and `\x1b[118;9u` respectively. If adding key interception elsewhere, always check for KKP variants. The key code (118 = Unicode `v`) and modifier encoding (bitmask+1) follow the KKP spec.

**Homebrew vs npm-global PATH order:**
`/home/linuxbrew/.linuxbrew/bin` comes before `~/.npm-global/bin` in PATH. Any wrapper installed at `~/.local/bin` or `~/.npm-global/bin` will be silently bypassed by the Homebrew copy. Always install at the path that `command -v copilot` actually resolves to.

**Never `cat >` through a symlink to install a wrapper:**
If the target path is a symlink (e.g., Homebrew bin → npm package bin script), `cat > target` writes through the symlink and corrupts the original package binary. Always `rm -f` the symlink first, then create a new regular file. The original package is found independently and referenced by absolute path.

**Never use `readlink -f` to find the real binary then copy it:**
The Homebrew copilot bin script does `import('./index.js')` relative to its own directory. If you copy it to a different directory (even the same one as `copilot-real`), Node resolves `./index.js` relative to the copy's location, not the original package. Use the npm-global symlink as `_REAL` — it's a symlink into the npm package directory so Node imports resolve correctly.

**Named Docker volume survives image rebuild:**
`construct-packages` volume (holds `/home/linuxbrew/.linuxbrew`) persists across `sys rebuild`. The wrapper installed in a previous version survives. `agent-patch.sh` handles this via version-string idempotency guards — if the guard version doesn't match the current file content, it reinstalls.

**`~/.config/construct-cli/logs/` vs `/tmp/`:**
Early wrapper versions logged to `/tmp/construct-copilot-wrapper.log`. Because containers run with `--rm`, `/tmp` is destroyed on exit. Logs must go to the home dir bind-mount (`~/.config/construct-cli/home/` on the host = `/home/construct/` in the container). All persistent logs now use `~/.config/construct-cli/logs/` inside the container.

**The `CONSTRUCT_FILE_PASTE_AGENTS` constant vs clipper default:**
`constants.go` defines `FileBasedPasteAgents = "gemini,qwen,codex"`. The clipper shim uses this from the `CONSTRUCT_FILE_PASTE_AGENTS` env var. Claude and Copilot are absent — they use raw-bytes mode through the shim (Claude/Pi) or bypass the shim entirely (Copilot via PTY wrapper). Codex now uses the PTY wrapper as primary path and this list is legacy-safe for clipper compatibility.

**Clipboard server port is random per session:**
A new port is assigned each time `construct <agent>` starts. The wrapper's `CONSTRUCT_CLIPBOARD_URL` env var is only valid for that session. If you restart the agent without a fresh `ct <agent>` invocation, the URL may be stale.

---

## Data Flow Diagram

```
Host (macOS/Linux/Windows)
│
├─ Clipboard Server (Go HTTP, random port, 32-byte token)
│  └─ clipboard_server.log (always-on)
│
└─ Docker container
   │
   ├─ Entrypoint (first run or hash change)
   │  ├─ root: ln -sf clipper → xclip, xsel, wl-paste
   │  ├─ agent-patch.sh (if CLIPBOARD_IMAGE_PATCH=1)
   │  │  ├─ fix_clipboard_libs()       — shim bundled xsel/xclip in npm packages
   │  │  ├─ patch_agent_code()         — remove darwin platform guards in JS
   │  │  ├─ patch_copilot_clipboard()  — replace @teddyzhu/clipboard with pure-JS bridge
   │  │  ├─ patch_copilot_keybinding() — allow Ctrl+V on Linux in Ink TUI
   │  │  └─ patch_copilot_paste_wrapper() — install Python PTY wrapper at copilot bin path
   │  └─ X11 bridge (optional): Xvfb + clipboard-x11-sync.sh (non-Codex)
   │
   ├─ Agent session env:
   │  CONSTRUCT_CLIPBOARD_URL=http://host.docker.internal:PORT
   │  CONSTRUCT_CLIPBOARD_TOKEN=...
   │  CONSTRUCT_AGENT_NAME=<agent>
   │  CONSTRUCT_FILE_PASTE_AGENTS=gemini,qwen,codex
   │  XDG_SESSION_TYPE=wayland  (claude, copilot, pi)
   │
   ├─ Copilot session
   │  └─ Python PTY wrapper (at /home/linuxbrew/.linuxbrew/bin/copilot)
   │     ├─ Spawns real copilot (~/.npm-global/bin/copilot) on inner PTY
   │     ├─ User presses Ctrl+V or Cmd+V
   │     │  Ghostty sends: \x1b[118;5u or \x1b[118;9u (KKP)
   │     │  Legacy terminal sends: \x16
   │     ├─ _handle_paste() intercepts any of the three
   │     ├─ curl → Clipboard Server → PNG bytes
   │     ├─ Save to .construct-clipboard/clipboard-{ts}.png
   │     └─ Inject "@.construct-clipboard/clipboard-{ts}.png " as typed text
   │        → Copilot sees it as @file reference, reads and displays image
   │
   ├─ Claude / Pi session
   │  └─ Native clipboard module calls wl-paste (→ clipper shim)
   │     ├─ clipper: PATH_AGENT=false (not in FILE_PASTE_AGENTS)
   │     ├─ curl → Clipboard Server → PNG bytes
   │     └─ Emit raw PNG bytes → agent receives binary image data
   │
   └─ Gemini / Qwen session
      └─ Agent calls xsel/xclip/wl-paste (→ clipper shim)
         ├─ clipper: PATH_AGENT=true (in FILE_PASTE_AGENTS)
         ├─ curl → Clipboard Server → PNG bytes
         ├─ Save to .construct-clipboard/clipboard-{ts}.png
         └─ Emit "@.construct-clipboard/clipboard-{ts}.png"
            → Agent receives file reference, reads image from disk
```

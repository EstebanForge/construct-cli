# Clipboard Bridge

The Construct CLI provides a bridge between the host clipboard and the containerized agents. This is particularly important for image pasting in agents like Copilot CLI.

## Copilot CLI Integration

Copilot CLI uses the `@teddyzhu/clipboard` library for clipboard access. On Linux, this library often fails to find a working X11/Wayland clipboard in a headless container.

### Patching Strategy

Construct automatically patches `@teddyzhu/clipboard/index.js` inside the container during the setup/update phase (via `agent-patch.sh`).

The patch (marked with `construct-copilot-clipboard-bridge-v2`):
1. Intercepts calls to `getFiles`, `getImageData`, `getClipboardFiles`, and `getClipboardImageData`.
2. Uses `curl` to fetch the image directly from the host-side clipboard server (`CONSTRUCT_CLIPBOARD_URL`).
3. Saves the image to a temporary file in the container.
4. Returns the file path or buffer to the agent, bypassing the native clipboard requirement.

### Coverage

The patcher searches for `@teddyzhu/clipboard` in several locations to support different installation methods:
- `$HOME/.npm-global` (Standard npm global install)
- `/home/linuxbrew/.linuxbrew` (Homebrew install)
- `$HOME/.local/share/gh/extensions` (GitHub CLI extension install)

## Debugging

If image pasting is not working, use `construct sys clipboard-debug` to diagnose the issue.

### 1. Check Patch State

Run the following to see if the library was found and patched:
```bash
./bin/construct sys clipboard-debug
```
It will report:
- Whether `@teddyzhu/clipboard/index.js` was found in any of the search paths.
- Whether the `construct-copilot-clipboard-bridge-v2` marker is present.
- Recent logs from the bridge if `CONSTRUCT_DEBUG=1` was used.

### 2. Enable Debug Logging

Run Copilot with debug mode enabled to capture detailed logs in the container and host:
```bash
CONSTRUCT_DEBUG=1 ./bin/construct copilot
```

- **Container Logs:** `/tmp/construct-copilot-clipboard.log` (Shows bridge activity, fetch attempts, and curl output).
- **Host Logs:** `~/.config/construct-cli/logs/debug_clipboard_server.log` (Shows host-side clipboard server requests).

### 3. Verify Host Tools

On Linux hosts, ensure `xclip` or `wl-paste` is installed. On macOS, the bridge uses `osascript`. On Windows, it uses PowerShell.

If `construct sys clipboard-debug` shows the file is patched but no image is fetched:
- Check if `host.docker.internal` is reachable from the container.
- Verify the host clipboard actually contains a PNG image.

# Construct Sandbox Environment - Agent Instructions

You are running inside **The Construct**, a secure, isolated Linux sandbox (Debian-based).

## 1. Environment Context
- **Isolation**: You are NOT running directly on the user's host machine. You are in a container.
- **Persistence**: Your home directory (`/home/construct`) and the tools installed via Homebrew/NPM are persistent across sessions.
- **Projects**: The user's project code is typically mounted at `/workspaces/<hash>/` (in daemon mode) or `/projects/<folder_name>/`.
- **Operating System**: Linux (regardless of whether the host is macOS or Windows).

## 2. Path Mapping & File Access
- **Host vs. Sandbox Paths**: If the user provides an absolute host path (e.g., starting with `/Users/`, `/home/`, or `C:\`), you MUST translate it to the corresponding path you see in `/workspaces/` or `/projects/`.
- **Clipboard Images**: When the user pastes an image, the system fetches it from the host and saves it as a local file (e.g., `.construct-clipboard/clipboard-123.png`). You will see this path injected into your input.

## 3. Communication Protocol
- **External Info**: If you need information that is not available inside the sandbox (e.g., host system configuration, hardware details, or files outside the mounted project), you MUST ask the user to provide it or run a command to fetch it.
- **Permissions**: You have `sudo` access inside this sandbox, but it does not grant you permissions on the host machine.
- **Security**: Do not attempt to "break out" of the sandbox. If you encounter network restrictions, explain them to the user so they can adjust the `construct network` settings.

## 4. Installed Tools
The environment is pre-loaded with a modern toolchain (Go, Rust, Node.js, Python, etc.) via Homebrew. If a tool is missing, you can suggest the user add it to their `packages.toml`.

### Host-Proxied Binaries
The user may have configured certain binaries to run **on the host machine** when you invoke them, rather than inside the sandbox. These appear on your PATH like any normal tool, but a shim transparently proxies each call to a host-side bridge.

- **How to tell**: check `$CONSTRUCT_HOST_BINARIES` (comma-separated list of proxied names).
- **Behavior**: invoking any of these runs the real binary on the host, as the user's own account, with your argv passed through verbatim. Output streams back normally; exit codes are preserved.
- **Non-interactive only**: there is no controlling terminal/PTY. Pipe stdin (one-shot) works; interactive prompts do not. Many CLIs offer a `--no-interactive` / `--json` / `--yes` flag for scripted use — pass it.
- **Security note for the user (not you)**: each proxied binary runs on the host with container-controlled argv. The allowlist is configured in `[sandbox] host_binaries` in `config.toml`. See `docs/HOST-EXEC.md`.
- **If a proxied binary exits 126**: the host bridge was unreachable or misconfigured. Tell the user to run `construct build` (the shim must be baked into the image) and check `~/.config/construct-cli/logs/host_exec.log` on the host.

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

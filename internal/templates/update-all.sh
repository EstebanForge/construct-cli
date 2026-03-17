#!/usr/bin/env bash
set -e

if [ "$(id -u)" = "0" ]; then
    exec gosu construct "$0" "$@"
fi

echo "Updating all agents, packages & tools..."
echo ""

echo "=== Construct update diagnostics ==="
echo "Timestamp (UTC): $(date -u +%Y-%m-%dT%H:%M:%SZ)"
echo "User IDs: uid=$(id -u) gid=$(id -g)"
echo "User names: user=$(id -un 2>/dev/null || echo unknown) group=$(id -gn 2>/dev/null || echo unknown)"
echo "HOME: $HOME"
echo "SHELL: ${SHELL:-unknown}"
echo "PATH: $PATH"
if [ -d /home/linuxbrew/.linuxbrew ]; then
    if [ -w /home/linuxbrew/.linuxbrew ]; then
        echo "Homebrew dir writable: /home/linuxbrew/.linuxbrew"
    else
        echo "⚠️  Homebrew dir not writable by current user: /home/linuxbrew/.linuxbrew"
        ls -ld /home/linuxbrew/.linuxbrew 2>/dev/null || true
    fi
fi
if command -v brew &> /dev/null; then
    echo "brew: $(command -v brew)"
    brew --version | head -1 || true
else
    echo "brew: not found"
fi
if command -v npm &> /dev/null; then
    echo "npm: $(command -v npm)"
    npm --version || true
else
    echo "npm: not found"
fi
if command -v topgrade &> /dev/null; then
    echo "topgrade: $(command -v topgrade)"
else
    echo "topgrade: not found"
fi
echo "==================================="
echo ""

eval "$(/home/linuxbrew/.linuxbrew/bin/brew shellenv)" || true

# Sudo detection: use empty string if root, test if sudo works, otherwise skip
if [ "$(id -u)" = "0" ]; then
    SUDO=""
elif sudo -n true 2>/dev/null; then
    SUDO="sudo"
else
    SUDO=""
    echo "⚠️  sudo not available - skipping system package updates"
fi

TOPGRADE_CONFIG="$HOME/.config/topgrade.toml"

if command -v topgrade &> /dev/null; then
    if [ -f "$TOPGRADE_CONFIG" ]; then
        topgrade --config "$TOPGRADE_CONFIG"
    else
        topgrade -y --disable system
    fi
else
    echo "topgrade not found, falling back to manual updates..."

    if [ -n "$SUDO" ] || [ "$(id -u)" = "0" ]; then
        echo "Updating system packages (apt)..."
        $SUDO apt-get update -qq && $SUDO apt-get -y -qq dist-upgrade && $SUDO apt-get -y -qq autoremove && $SUDO apt-get -y -qq autoclean || true
    fi

    echo "Updating claude-code..."
    claude update || true

    echo "Updating Homebrew packages..."
    brew update && brew upgrade --greedy && brew cleanup && brew autoremove || true

    echo "Updating npm global packages..."
    if command -v npm &> /dev/null; then
        mkdir -p "$HOME/.npm-global"
        npm config set prefix "$HOME/.npm-global" || true
        export PATH="$HOME/.npm-global/bin:$PATH"
        echo "npm global prefix: $(npm config get prefix 2>/dev/null || echo unknown)"
        npm update -g || true
    fi
fi

PATCH_SCRIPT="$HOME/.config/construct-cli/container/agent-patch.sh"
PATCH_ENABLED="${CONSTRUCT_CLIPBOARD_IMAGE_PATCH:-1}"
if [ "$PATCH_ENABLED" = "0" ] || [ "$PATCH_ENABLED" = "false" ]; then
    echo ""
    echo "ℹ️  Clipboard image patch disabled; skipping agent patching"
elif [ -f "$PATCH_SCRIPT" ]; then
    echo ""
    echo "🔧 Patching agent integrations..."
    bash "$PATCH_SCRIPT" || echo "⚠️  Agent patching encountered errors"
else
    echo "⚠️  Agent patch script not found at $PATCH_SCRIPT; skipping patching"
fi

ENTRYPOINT_SCRIPT="/usr/local/bin/entrypoint.sh"
HASH_FILE="$HOME/.local/.entrypoint_hash"
USER_INSTALL_SCRIPT="$HOME/.config/construct-cli/container/install_user_packages.sh"
HASH_UTILS="$HOME/.config/construct-cli/container/entrypoint-hash.sh"
if [ -f "$HASH_UTILS" ]; then
    # shellcheck source=/dev/null
    . "$HASH_UTILS"
fi

if command -v write_entrypoint_hash >/dev/null 2>&1; then
    write_entrypoint_hash "$HASH_FILE" "$ENTRYPOINT_SCRIPT" "$USER_INSTALL_SCRIPT"
elif [ -f "$ENTRYPOINT_SCRIPT" ]; then
    CURRENT_HASH=$(sha256sum "$ENTRYPOINT_SCRIPT" | awk '{print $1}')
    if [ -f "$USER_INSTALL_SCRIPT" ]; then
        INSTALL_HASH=$(sha256sum "$USER_INSTALL_SCRIPT" | awk '{print $1}')
        CURRENT_HASH="${CURRENT_HASH}-${INSTALL_HASH}"
    fi
    mkdir -p "$HOME/.local"
    echo "$CURRENT_HASH" > "$HASH_FILE"
fi

echo ""
echo "Post-update command verification..."
missing_cmds=""
for cmd in claude amp copilot opencode qwen cline codex goose gemini kilocode pi; do
    if command -v "$cmd" &> /dev/null; then
        cmd_path=$(command -v "$cmd")
        echo "  ✓ $cmd -> $cmd_path"
    else
        echo "  - $cmd not found in PATH"
        missing_cmds="$missing_cmds $cmd"
    fi
done
if [ -n "$missing_cmds" ]; then
    echo "⚠️  Missing commands after update:$missing_cmds"
    echo "    If these agents are expected, run: construct sys packages --install"
fi

echo ""
echo "All agents, packages & tools updated!"

#!/usr/bin/env bash
# entrypoint.sh - Install agents and packages on first run only

export DEBIAN_FRONTEND=noninteractive

# 0. Root-level permission fixing (if running as root)
if [ "$(id -u)" = "0" ]; then
    # CRITICAL: Ensure Homebrew volume is owned by construct user
    # brew/npm MUST run as construct, not as root
    # This fixes permission errors after updates
    if [ -d /home/linuxbrew/.linuxbrew ]; then
        chown -R construct:construct /home/linuxbrew/.linuxbrew 2>/dev/null || true
    fi

    # Fix home directory permissions (in case volume mounts overrode them)
    chown -R construct:construct /home/construct/.local /home/construct/.config 2>/dev/null || true
    chown construct:construct /home/construct || true

    # Fix SSH Agent permissions (critical for macOS/Docker Desktop)
    if [ -n "$SSH_AUTH_SOCK" ]; then
        # Check if the socket file exists (it might be a bind mount)
        if [ -e "$SSH_AUTH_SOCK" ]; then
            chown construct:construct "$SSH_AUTH_SOCK" || true
            chmod 666 "$SSH_AUTH_SOCK" || true
        fi
    fi

    # SSH Agent Forwarding
    if [ -n "$CONSTRUCT_SSH_BRIDGE_PORT" ] && command -v socat >/dev/null; then
        # TCP-to-Unix Bridge (macOS/OrbStack/Docker Desktop reliability)
        PROXY_SOCK="/home/construct/.ssh/agent.sock"
        mkdir -p /home/construct/.ssh
        chown construct:construct /home/construct/.ssh
        rm -f "$PROXY_SOCK"

        # Bridge local Unix socket -> Host TCP listener
        socat UNIX-LISTEN:"$PROXY_SOCK",fork,mode=600,user=construct,group=construct TCP:host.docker.internal:"$CONSTRUCT_SSH_BRIDGE_PORT" >/tmp/socat.log 2>&1 &

        export SSH_AUTH_SOCK="$PROXY_SOCK"
        echo "✓ Started SSH Agent proxy"
    elif [ -n "$SSH_AUTH_SOCK" ]; then
        # Standard Linux path: just fix permissions
        if [ -e "$SSH_AUTH_SOCK" ]; then
            chown construct:construct "$SSH_AUTH_SOCK" || true
            chmod 666 "$SSH_AUTH_SOCK" || true
        fi
    fi

    # Ensure xclip/xsel always point to clipper (packages may overwrite binaries).
    if [ -f /usr/bin/xclip ] && [ ! -L /usr/bin/xclip ]; then
        mv /usr/bin/xclip /usr/bin/xclip-real 2>/dev/null || true
    fi
    ln -sf /usr/local/bin/clipper /usr/bin/xclip

    if [ -f /usr/bin/xsel ] && [ ! -L /usr/bin/xsel ]; then
        mv /usr/bin/xsel /usr/bin/xsel-real 2>/dev/null || true
    fi
    ln -sf /usr/local/bin/clipper /usr/bin/xsel

    if [ -f /usr/bin/wl-paste ] && [ ! -L /usr/bin/wl-paste ]; then
        mv /usr/bin/wl-paste /usr/bin/wl-paste-real 2>/dev/null || true
        ln -sf /usr/local/bin/clipper /usr/bin/wl-paste
    fi

    # Setup WSL-like paths for Codex fallback
    mkdir -p /mnt/c
    ln -sf /tmp /mnt/c/tmp
    ln -sf /projects /mnt/c/projects
    chown -R construct:construct /mnt/c 2>/dev/null || true

    # Drop privileges and run the rest of the script as 'construct'
    exec gosu construct "$0" "$@"
fi

# Ensure all required paths are in PATH
export PATH="/home/linuxbrew/.linuxbrew/bin:$HOME/.local/bin:$HOME/.npm-global/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

# Ensure SSH config prioritizes standard key names unless user provided one.
ensure_ssh_config() {
    local ssh_dir="$HOME/.ssh"
    local ssh_config="$ssh_dir/config"

    mkdir -p "$ssh_dir"
    chmod 700 "$ssh_dir"

    # Check if config exists
    if [ -f "$ssh_config" ]; then
        # Check if user explicitly disabled auto-management
        if grep -q "^# construct-managed: false" "$ssh_config"; then
            # User has opted out of auto-updates
            return
        fi

        # Backup existing config before updating
        cp "$ssh_config" "${ssh_config}.backup" 2>/dev/null || true
    fi

    # Write new config
    cat > "$ssh_config" <<'EOF'
# construct-managed: true
# Set to 'false' above to prevent Construct from updating this file.
# If you customize this file, set construct-managed to false to preserve your changes.

Host *
  IdentityAgent ~/.ssh/agent.sock
  PubkeyAcceptedAlgorithms +ssh-rsa
  IdentityFile ~/.ssh/default
  IdentityFile ~/.ssh/personal
  # IdentitiesOnly not set - will try all keys in agent then fall back to physical keys
EOF
    chmod 600 "$ssh_config"
}
ensure_ssh_config

# Check if we need to run installation (First run or Script update)
# We use the script hash plus install script hash to determine if a re-run is needed.
USER_INSTALL_SCRIPT="/home/construct/.config/construct-cli/container/install_user_packages.sh"
CURRENT_HASH=$(sha256sum "$0" | awk '{print $1}')
if [ -f "$USER_INSTALL_SCRIPT" ]; then
    INSTALL_HASH=$(sha256sum "$USER_INSTALL_SCRIPT" | awk '{print $1}')
    CURRENT_HASH="${CURRENT_HASH}-${INSTALL_HASH}"
fi
HASH_FILE="/home/construct/.local/.entrypoint_hash"
FORCE_FILE="/home/construct/.local/.force_entrypoint"
PREVIOUS_HASH=""
if [ -f "$HASH_FILE" ]; then
    PREVIOUS_HASH=$(cat "$HASH_FILE")
fi
if [ -f "$FORCE_FILE" ]; then
    PREVIOUS_HASH=""
    rm -f "$FORCE_FILE"
fi

if [ "$CURRENT_HASH" != "$PREVIOUS_HASH" ]; then
    echo "🔧 Setup detected (First run or Update) - installing tools..."
    echo "   This might take a few minutes..."
    echo ""

    # Create necessary directories
    mkdir -p ~/.local/bin ~/.local/lib/node_modules ~/.npm-global

    # User-defined packages (install_user_packages.sh)
    # This script is generated by the CLI based on packages.toml
    if [ -f "$USER_INSTALL_SCRIPT" ]; then
        echo "📦 Installing system and user-defined packages..."
        bash "$USER_INSTALL_SCRIPT" || echo "⚠️ Package installation encountered errors"
    fi

    # Configure npm prefix for any subsequent manual installs
    if command -v npm &> /dev/null; then
        npm config set prefix "$HOME/.npm-global"
    fi

    # Update hash file
    echo "$CURRENT_HASH" > "$HASH_FILE"
    echo ""
    echo "✅ Setup complete! Environment ready."
fi

# Configure shell environment (aliases, prompt, etc.)
setup_shell_environment() {
    local alias_file="$HOME/.bash_aliases"

    # 1. Generate/Overwrite standard aliases (Auto-updated on every run)
    cat > "$alias_file" <<EOF
# --- Standard Aliases ---
alias ls='ls --color=auto'
alias ll='ls -alF'
alias la='ls -A'
alias l='ls -CF'
alias grep='grep --color=auto'
alias fgrep='fgrep --color=auto'
alias egrep='egrep --color=auto'

# --- Navigation ---
alias ..='cd ..'
alias ...='cd ../..'
alias ....='cd ../../../..'

# --- Construct Provider Aliases ---
EOF

    # 2. Ensure .bashrc sources .bash_aliases
    if ! grep -q "test -f ~/.bash_aliases && . ~/.bash_aliases" "$HOME/.bashrc"; then
        {
            echo ""
            echo "# Alias support"
            echo 'test -f ~/.bash_aliases && . ~/.bash_aliases'
        } >> "$HOME/.bashrc"
    fi
}
setup_shell_environment

# Start a headless X11 clipboard bridge when no DISPLAY is available.
if [ -n "$CONSTRUCT_CLIPBOARD_URL" ] && [ -z "$DISPLAY" ]; then
    if command -v Xvfb >/dev/null; then
        DISPLAY="${CONSTRUCT_X11_DISPLAY:-:0}"
        export DISPLAY
        if ! pgrep -x Xvfb >/dev/null 2>&1; then
            Xvfb "$DISPLAY" -screen 0 1024x768x24 -nolisten tcp >/tmp/xvfb.log 2>&1 &
            if [ "$CONSTRUCT_DEBUG" = "1" ]; then
                echo "✓ Started Xvfb for headless clipboard"
            fi
        fi
        # Wait briefly for the X11 socket to appear.
        XSOCK="/tmp/.X11-unix/X${DISPLAY#:}"
        for _ in $(seq 1 20); do
            if [ -S "$XSOCK" ]; then
                break
            fi
            sleep 0.1
        done
        /usr/local/bin/clipboard-x11-sync.sh >/tmp/clipboard-x11-sync.log 2>&1 &
    else
        if [ "$CONSTRUCT_DEBUG" = "1" ]; then
            echo "⚠️  Xvfb not found; headless clipboard bridge disabled"
        fi
    fi
fi

# Configure network filtering (if in strict mode)
if [ -f "/usr/local/bin/network-filter.sh" ]; then
    /usr/local/bin/network-filter.sh || true
fi

# Fix clipboard libs for Node.js apps (Gemini CLI, etc.)
# This replaces bundled 'xsel' in node_modules with our bridge
fix_clipboard_libs() {
    # Strategy: Find the directory where clipboardy expects xsel to be.
    # The path usually ends in .../clipboardy/fallbacks/linux
    # We find directories matching this pattern.
    # We search both linuxbrew (for gemini-cli) and npm-global (for qwen, etc.)

    # 1. Standard clipboardy structure
    find -L /home/linuxbrew/.linuxbrew "$HOME/.npm-global" -type d -path "*/clipboardy/fallbacks/linux" 2>/dev/null | while read -r dir; do
        # Shim xsel
        local xsel_bin="$dir/xsel"
        if [ -L "$xsel_bin" ] && [[ "$(readlink "$xsel_bin")" == *"/clipper"* ]]; then
             : # Already shimmed
        else
             rm -f "$xsel_bin" 2>/dev/null
             ln -sf /usr/local/bin/clipper "$xsel_bin"
        fi

        # Shim xclip
        local xclip_bin="$dir/xclip"
        rm -f "$xclip_bin" 2>/dev/null
        ln -sf /usr/local/bin/clipper "$xclip_bin"
    done

    # 2. Aggressive search for ANY rogue xsel in npm-global (for Qwen or others with weird structures)
    find -L "$HOME/.npm-global" -name "xsel" -type f 2>/dev/null | while read -r binary; do
        # Ignore if it's already our shim (which is a symlink, but -type f might catch it if following links? no, find -L does)
        # Actually -type f with -L matches symlinks to files.
        if [[ "$(readlink -f "$binary")" == *"/clipper"* ]]; then
            continue
        fi

        rm -f "$binary"
        ln -sf /usr/local/bin/clipper "$binary"
    done
}
fix_clipboard_libs


# Patch agent code to bypass macOS-only checks for clipboard images
patch_agent_code() {
    # Find all JS files that might contain the platform check
    # We look for files containing 'process.platform' and 'darwin'
    find -L /home/linuxbrew/.linuxbrew "$HOME/.npm-global" -type f -name "*.js" 2>/dev/null | xargs grep -l "process.platform" 2>/dev/null | xargs grep -l "darwin" 2>/dev/null | while read -r js_file; do
        if grep -q "process.platform !== \"darwin\"" "$js_file"; then
            # Replace platform check with a dummy 'false' to allow the code to run on Linux
            sed -i 's/process.platform !== \"darwin\"/false/g' "$js_file"
        elif grep -q "process.platform !== 'darwin'" "$js_file"; then
            sed -i "s/process.platform !== 'darwin'/false/g" "$js_file"
        fi
    done
}
patch_agent_code

# Forward localhost login callbacks to the container when requested.
if [ "$CONSTRUCT_LOGIN_FORWARD" = "1" ] && command -v socat >/dev/null; then
    LOGIN_PORTS="${CONSTRUCT_LOGIN_FORWARD_PORTS:-1455}"
    LISTEN_OFFSET="${CONSTRUCT_LOGIN_FORWARD_LISTEN_OFFSET:-10000}"
    IFS=', ' read -r -a LOGIN_PORT_LIST <<< "$LOGIN_PORTS"
    for LOGIN_PORT in "${LOGIN_PORT_LIST[@]}"; do
        if [ -z "$LOGIN_PORT" ]; then
            continue
        fi
        LISTEN_PORT=$((LOGIN_PORT + LISTEN_OFFSET))
        socat "TCP-LISTEN:${LISTEN_PORT},fork,bind=0.0.0.0" "TCP:127.0.0.1:${LOGIN_PORT}" >/tmp/login-forward.log 2>&1 &
        echo "✓ Started login callback forwarder on port ${LISTEN_PORT} -> ${LOGIN_PORT}"
    done
fi

# Debug: Check if command exists before exec
if [ $# -gt 0 ]; then
    if ! command -v "$1" &> /dev/null; then
        echo "❌ Command not found: $1"
        echo "🔍 Current PATH: $PATH"
        echo "🔍 Searching for $1 in expected locations..."
        find /home/construct/.local/bin -name "$1*" 2>/dev/null | head -5
        find /home/linuxbrew/.linuxbrew/bin -name "$1*" 2>/dev/null | head -5
        exit 1
    fi
fi

# Execute the command passed to docker run
exec "$@"

#!/usr/bin/env bash
# entrypoint.sh - Install agents and packages on first run only
# Designed to work with both Docker (may run as root) and Podman rootless (runs as user)

export DEBIAN_FRONTEND=noninteractive

RUN_AS_USER="construct"
RUN_AS_CHOWN="construct:construct"
SKIP_HOME_CHOWN=""
if [ -n "$CONSTRUCT_HOST_UID" ] && [ -n "$CONSTRUCT_HOST_GID" ]; then
    RUN_AS_USER="${CONSTRUCT_HOST_UID}:${CONSTRUCT_HOST_GID}"
    RUN_AS_CHOWN="${CONSTRUCT_HOST_UID}:${CONSTRUCT_HOST_GID}"
    SKIP_HOME_CHOWN="1"
fi

# Root-level operations (only if actually running as root - typically Docker, not Podman)
if [ "$(id -u)" = "0" ]; then
    # Fix Homebrew volume ownership
    if [ -d /home/linuxbrew/.linuxbrew ]; then
        chown -R "$RUN_AS_CHOWN" /home/linuxbrew/.linuxbrew 2>/dev/null || true
    fi

    # Fix home directory permissions
    if [ -z "$SKIP_HOME_CHOWN" ]; then
        chown -R "$RUN_AS_CHOWN" /home/construct 2>/dev/null || true
    fi

    # Fix SSH socket permissions
    if [ -n "$SSH_AUTH_SOCK" ] && [ -e "$SSH_AUTH_SOCK" ]; then
        chown "$RUN_AS_CHOWN" "$SSH_AUTH_SOCK" 2>/dev/null || true
        chmod 666 "$SSH_AUTH_SOCK" 2>/dev/null || true
    fi

    # Clipboard tool symlinks (these are set up in Dockerfile but may need refresh)
    ln -sf /usr/local/bin/clipper /usr/bin/xclip 2>/dev/null || true
    ln -sf /usr/local/bin/clipper /usr/bin/xsel 2>/dev/null || true
    ln -sf /usr/local/bin/clipper /usr/bin/wl-paste 2>/dev/null || true

    # WSL-like paths for Codex fallback
    mkdir -p /mnt/c 2>/dev/null || true
    ln -sf /tmp /mnt/c/tmp 2>/dev/null || true
    ln -sf /projects /mnt/c/projects 2>/dev/null || true

    # Patch /etc/profile to preserve PATH
    if ! grep -q "# Construct: PATH management disabled" /etc/profile 2>/dev/null; then
        sed -i '/^if \[ "$(id -u)" -eq 0 \]; then$/,/^export PATH$/ {
            i# Construct: PATH management disabled - PATH is set by docker-compose.yml and entrypoint.sh
            s/^/# /
        }' /etc/profile 2>/dev/null || true
    fi

    # Drop privileges and re-run as construct user
    exec gosu "$RUN_AS_USER" "$0" "$@"
else
    # Non-root mode (Podman rootless) - fix permissions using sudo if available
    # This handles the case where files were created by root during image build
    # but we're now running as a non-root user
    if command -v sudo >/dev/null 2>&1; then
        # Fix home directory permissions
        sudo chown -R "$(id -u):$(id -g)" /home/construct 2>/dev/null || true

        # Fix Homebrew volume ownership
        if [ -d /home/linuxbrew/.linuxbrew ]; then
            sudo chown -R "$(id -u):$(id -g)" /home/linuxbrew/.linuxbrew 2>/dev/null || true
        fi

        # Fix SSH socket permissions
        if [ -n "$SSH_AUTH_SOCK" ] && [ -e "$SSH_AUTH_SOCK" ]; then
            sudo chown "$(id -u):$(id -g)" "$SSH_AUTH_SOCK" 2>/dev/null || true
            chmod 666 "$SSH_AUTH_SOCK" 2>/dev/null || true
        fi
    fi
fi

# SSH Agent Bridge (works as non-root user too)
if [ -n "$CONSTRUCT_SSH_BRIDGE_PORT" ] && command -v socat >/dev/null; then
    PROXY_SOCK="$HOME/.ssh/agent.sock"
    mkdir -p "$HOME/.ssh" 2>/dev/null || true
    chmod 700 "$HOME/.ssh" 2>/dev/null || true
    rm -f "$PROXY_SOCK"
    socat UNIX-LISTEN:"$PROXY_SOCK",fork,mode=600 TCP:host.docker.internal:"$CONSTRUCT_SSH_BRIDGE_PORT" >/tmp/socat.log 2>&1 &
    export SSH_AUTH_SOCK="$PROXY_SOCK"
    echo "✓ Started SSH Agent proxy"
fi

# Ensure all required paths are in PATH
# NOTE: This PATH definition must be kept in sync with internal/env/env.go PathComponents
# Any changes here should be reflected in env.go and vice versa
ORIGINAL_PATH="$PATH"
PATH=""
add_path() {
    local dir="$1"
    if [ -d "$dir" ]; then
        case ":$PATH:" in
            *":$dir:"*) return ;;
        esac
        if [ -z "$PATH" ]; then
            PATH="$dir"
        else
            PATH="$PATH:$dir"
        fi
    fi
}

add_path "/home/linuxbrew/.linuxbrew/bin"
add_path "/home/linuxbrew/.linuxbrew/sbin"
add_path "$HOME/.local/bin"
add_path "$HOME/.npm-global/bin"
add_path "$HOME/.cargo/bin"
add_path "$HOME/.bun/bin"
add_path "$HOME/.asdf/bin"
add_path "$HOME/.asdf/shims"
add_path "$HOME/.volta/bin"
add_path "$HOME/.nix-profile/bin"
add_path "/nix/var/nix/profiles/default/bin"
add_path "$HOME/.phpbrew/bin"
add_path "$HOME/.local/share/mise/bin"
add_path "$HOME/.local/share/mise/shims"
add_path "/usr/local/sbin"
add_path "/usr/local/bin"
add_path "/usr/sbin"
add_path "/usr/bin"
add_path "/sbin"
add_path "/bin"

if [ -d "$HOME/.nvm/versions/node" ]; then
    for dir in "$HOME"/.nvm/versions/node/*/bin; do
        add_path "$dir"
    done
fi

if [ -n "$ORIGINAL_PATH" ]; then
    IFS=':' read -r -a original_parts <<< "$ORIGINAL_PATH"
    for dir in "${original_parts[@]}"; do
        add_path "$dir"
    done
fi

export PATH
export NVM_DIR="$HOME/.nvm"
# Ensure library path includes Homebrew (for libgit2, etc.)
export LD_LIBRARY_PATH="/home/linuxbrew/.linuxbrew/lib:$LD_LIBRARY_PATH"
# Suppress Node.js deprecation warnings (punycode in Node 21+, etc.)
export NODE_NO_WARNINGS=1

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
HASH_FILE="/home/construct/.local/.entrypoint_hash"
HASH_UTILS="/home/construct/.config/construct-cli/container/entrypoint-hash.sh"
if [ -f "$HASH_UTILS" ]; then
    # shellcheck source=/dev/null
    . "$HASH_UTILS"
fi
if command -v compute_entrypoint_hash >/dev/null 2>&1; then
    CURRENT_HASH=$(compute_entrypoint_hash "$0" "$USER_INSTALL_SCRIPT")
else
    CURRENT_HASH=$(sha256sum "$0" | awk '{print $1}')
    if [ -f "$USER_INSTALL_SCRIPT" ]; then
        INSTALL_HASH=$(sha256sum "$USER_INSTALL_SCRIPT" | awk '{print $1}')
        CURRENT_HASH="${CURRENT_HASH}-${INSTALL_HASH}"
    fi
fi
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

    # Create Pi agent config directories with empty auth.json
    mkdir -p ~/.pi/agent
    if [ ! -f ~/.pi/agent/auth.json ]; then
        echo '{}' > ~/.pi/agent/auth.json
    fi

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

    # Patch agent integrations (clipboard fixes, cross-platform checks)
    PATCH_SCRIPT="/home/construct/.config/construct-cli/container/agent-patch.sh"
    if [ -f "$PATCH_SCRIPT" ]; then
        bash "$PATCH_SCRIPT" || echo "⚠️ Agent patching encountered errors"
    else
        echo "⚠️  Agent patch script not found; skipping patching"
    fi

    # Update hash file
    if command -v write_entrypoint_hash >/dev/null 2>&1; then
        write_entrypoint_hash "$HASH_FILE" "$0" "$USER_INSTALL_SCRIPT"
    else
        echo "$CURRENT_HASH" > "$HASH_FILE"
    fi
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

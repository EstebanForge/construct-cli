#!/usr/bin/env bash
# entrypoint.sh - Install agents and packages on first run only

# 0. Root-level permission fixing (if running as root)
if [ "$(id -u)" = "0" ]; then
    # Fix SSH Agent permissions (critical for macOS/Docker Desktop)
    if [ -n "$SSH_AUTH_SOCK" ]; then
        # Check if the socket file exists (it might be a bind mount)
        if [ -e "$SSH_AUTH_SOCK" ]; then
            chown construct:construct "$SSH_AUTH_SOCK" || true
            chmod 666 "$SSH_AUTH_SOCK" || true
        fi
    fi

    # Fix home directory permissions (in case volume mounts overrode them)
    # We avoid -R on the whole home if it's huge, but .local and .config are critical
    # chown -R construct:construct /home/construct/.local /home/construct/.config 2>/dev/null || true
    # Actually, for first run stability, we ensure construct owns its home
    chown construct:construct /home/construct || true

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
        echo "✓ Started SSH Agent proxy (TCP Bridge)"
    elif [ -n "$SSH_AUTH_SOCK" ]; then
        # Standard Linux path: just fix permissions
        if [ -e "$SSH_AUTH_SOCK" ]; then
            chown construct:construct "$SSH_AUTH_SOCK" || true
            chmod 666 "$SSH_AUTH_SOCK" || true
        fi
    fi

    # Drop privileges and run the rest of the script as 'construct'
    exec gosu construct "$0" "$@"
fi

# Ensure all required paths are in PATH
export PATH="/home/linuxbrew/.linuxbrew/bin:$HOME/.local/bin:$HOME/.npm-global/bin:/usr/local/bin:$PATH"

MARKER_FILE="/home/construct/.local/.agents-installed"

if [ ! -f "$MARKER_FILE" ]; then
    echo "🔧 First run detected - installing AI agents..."
    echo "This will take 5-10 minutes. Subsequent runs will be instant."
    echo ""

    # Create necessary directories
    mkdir -p ~/.local/bin ~/.local/lib/node_modules ~/.npm-global

    # Install claude-code using official installer
    echo "Installing claude-code..."
    curl -fsSL https://claude.ai/install.sh | bash || true


    # Install Homebrew CLI tools and development utilities
    echo "Installing Homebrew CLI tools..."
    eval "$(/home/linuxbrew/.linuxbrew/bin/brew shellenv)"
    brew install ast-grep yq sd fzf eza zoxide ripgrep \
        gh git-delta git-cliff procs python-setuptools httpie \
        yarn composer wget tree neovim gulp-cli unzip \
        ffmpeg php php-cs-fixer wp-cli tailwindcss uv prettier \
        go openjdk typescript rust kotlin lua ruby dart-sdk swift perl zig erlang gnucobol \
        ninja gradle \
        fastmod shellcheck yamllint terraform awscli \
        node@24 python@3 oven-sh/bun/bun jq \
        vite webpack tlrc || true

    # Install AI agents via Homebrew
    echo "Installing gemini-cli..."
    brew install gemini-cli || npm install -g @google/gemini-cli || true

    echo "Installing opencode..."
    brew install opencode || true

    # Now that Node.js is installed (from gemini-cli), configure npm and install npm-based agents
    if command -v npm &> /dev/null; then
        echo "Configuring npm..."
        npm config set prefix "$HOME/.npm-global"

        # npm-based agents (to .local/lib/node_modules)
        echo "Installing qwen-code..."
        npm install -g @qwen-code/qwen-code@latest || true

        # GitHub Copilot CLI (npm only)
        echo "Installing copilot-cli..."
        npm install -g @github/copilot || true

        # Cline CLI
        echo "Installing cline..."
        npm install -g cline || true

        # OpenAI Codex CLI
        echo "Installing codex-cli..."
        npm install -g @openai/codex || true
    else
        echo "⚠️  npm not available, skipping npm-based agent installations"
    fi

    # MCP CLI
    echo "Installing mcp-cli-ent..."
    curl -fsSL https://raw.githubusercontent.com/EstebanForge/mcp-cli-ent/main/scripts/install.sh | bash || true

    # Mark as installed
    touch "$MARKER_FILE"
    echo ""
    echo "✅ Installation complete! Agents installed to persistent volumes."
    echo "   Next launch will be instant."
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

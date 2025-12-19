#!/usr/bin/env bash
# entrypoint.sh - Install agents and packages on first run only

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
    brew install ast-grep yq sd fzf eza zoxide \
        gh git-delta git-cliff procs python-setuptools httpie \
        yarn composer wget tree neovim gulp-cli unzip \
        ffmpeg php php-cs-fixer wp-cli tailwindcss uv go prettier \
        fastmod shellcheck yamllint terraform awscli \
        node@24 python@3 oven-sh/bun/bun \
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

    # Create aliases
    {
        echo 'alias zai="claude --api-base https://api.z.ai/api/coding/paas/v4"'
        echo 'alias glm="zai"'
        echo 'alias minimax="claude --api-base <minimax-endpoint>"'
    } >> ~/.bashrc

    # Mark as installed
    touch "$MARKER_FILE"
    echo ""
    echo "✅ Installation complete! Agents installed to persistent volumes."
    echo "   Next launch will be instant."
fi

# Configure network filtering (if in strict mode)
if [ -f "/usr/local/bin/network-filter.sh" ]; then
    /usr/local/bin/network-filter.sh || true
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

#!/usr/bin/env bash
set -e

if [ "$(id -u)" = "0" ]; then
    exec gosu construct "$0" "$@"
fi

echo "Updating all agents, packages & tools..."
echo ""

eval "$(/home/linuxbrew/.linuxbrew/bin/brew shellenv)" || true

TOPGRADE_CONFIG="$HOME/.config/topgrade.toml"

if command -v topgrade &> /dev/null; then
    if [ -f "$TOPGRADE_CONFIG" ]; then
        topgrade --config "$TOPGRADE_CONFIG"
    else
        topgrade -y --disable system
    fi
else
    echo "topgrade not found, falling back to manual updates..."
    
    echo "Updating system packages (apt)..."
    sudo apt-get update -qq && sudo apt-get -y -qq dist-upgrade && sudo apt-get -y -qq autoremove && sudo apt-get -y -qq autoclean || true

    echo "Updating claude-code..."
    curl -fsSL https://claude.ai/install.sh | bash || true

    echo "Updating mcp-cli-ent..."
    curl -fsSL https://raw.githubusercontent.com/EstebanForge/mcp-cli-ent/main/scripts/install.sh | bash || true

    echo "Updating Homebrew packages..."
    brew update && brew upgrade --greedy && brew cleanup && brew autoremove || true

    echo "Updating npm global packages..."
    if command -v npm &> /dev/null; then
        npm update -g || true
    fi
fi

echo ""
echo "All agents, packages & tools updated!"

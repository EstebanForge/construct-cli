#!/usr/bin/env bash
set -e

if [ "$(id -u)" = "0" ]; then
    exec gosu construct "$0" "$@"
fi

echo "Updating all agents, packages & tools..."
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
        npm update -g || true
    fi
fi

echo ""
echo "All agents, packages & tools updated!"

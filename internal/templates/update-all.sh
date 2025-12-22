#!/usr/bin/env bash
# update-all.sh - Update all installed agents and packages

echo "🔄 Updating all AI agents, tools, and packages..."
echo ""

# Update system packages (Debian apt)
echo "Updating system packages (apt)..."
sudo apt-get update -qq && sudo apt-get upgrade -y -qq || true

# Update claude-code using official installer
echo "Updating claude-code..."
curl -fsSL https://claude.ai/install.sh | bash || true

# Update MCP CLI-Ent (installed via script)
echo "Updating mcp-cli-ent..."
curl -fsSL https://raw.githubusercontent.com/EstebanForge/mcp-cli-ent/main/scripts/install.sh | bash || true

# Update Homebrew (all packages)
echo "Updating Homebrew formulas and all installed packages..."
eval "$(/home/linuxbrew/.linuxbrew/bin/brew shellenv)" || true
brew update || true
brew upgrade || true
brew cleanup || true

# Update npm global packages (all)
echo "Updating all npm global packages..."
# Clean up corrupted npm packages (invalid names with dots after scope)
if command -v jq >/dev/null 2>&1; then
    corrupted=$(npm list -g --json 2>/dev/null | jq -r '.dependencies | keys[] | select(startswith("@") and (split("/")[1] // "" | startswith(".")))' 2>/dev/null || echo "")
    if [ -n "$corrupted" ]; then
        echo "Cleaning up corrupted npm packages..."
        echo "$corrupted" | while read -r pkg; do
            [ -n "$pkg" ] && npm uninstall -g "$pkg" 2>/dev/null || true
        done
    fi
fi
npm update -g 2>&1 | grep -v "Invalid package name" || true

echo ""
echo "✅ All agents & packages updated!"

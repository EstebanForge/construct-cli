#!/usr/bin/env bash
# update-all.sh - Update all installed agents and packages

echo "🔄 Updating all agents, packages & tools..."
echo ""

# Ensure we run as 'construct' for brew/npm (use sudo where needed)
if [ "$(id -u)" = "0" ]; then
    exec gosu construct "$0" "$@"
fi

# Update system packages (Debian apt-get)
echo "Updating system packages (apt)..."
sudo apt-get update -qq && sudo apt-get -y -qq dist-upgrade && sudo apt-get -y -qq autoremove && sudo apt-get -y -qq autoclean || true

# Update claude-code using official installer
echo "Updating claude-code..."
curl -fsSL https://claude.ai/install.sh | bash || true

# Update MCP CLI-Ent (installed via script)
echo "Updating mcp-cli-ent..."
curl -fsSL https://raw.githubusercontent.com/EstebanForge/mcp-cli-ent/main/scripts/install.sh | bash || true

# Update Homebrew (all packages)
echo "Updating Homebrew formulas and all installed packages..."
eval "$(/home/linuxbrew/.linuxbrew/bin/brew shellenv)" || true
brew_prefix="/home/linuxbrew/.linuxbrew"
if [ -d "$brew_prefix" ]; then
    if [ ! -d "$brew_prefix/.git" ]; then
        echo "Repairing Homebrew git metadata..."
        git -C "$brew_prefix" init || true
        git -C "$brew_prefix" remote add origin https://github.com/Homebrew/brew || true
        git -C "$brew_prefix" fetch origin master --depth=1 2>/dev/null || git -C "$brew_prefix" fetch origin main --depth=1 || true
        if git -C "$brew_prefix" show-ref --verify --quiet refs/remotes/origin/master; then
            git -C "$brew_prefix" reset --hard origin/master || true
        elif git -C "$brew_prefix" show-ref --verify --quiet refs/remotes/origin/main; then
            git -C "$brew_prefix" reset --hard origin/main || true
        fi
    elif ! git -C "$brew_prefix" remote get-url origin >/dev/null 2>&1; then
        git -C "$brew_prefix" remote add origin https://github.com/Homebrew/brew || true
    fi

    taps_dir="$brew_prefix/Library/Taps"
    if [ -d "$taps_dir" ]; then
        find "$taps_dir" -mindepth 2 -maxdepth 2 -type d 2>/dev/null | while read -r tap; do
            if [ ! -d "$tap/.git" ]; then
                owner=$(basename "$(dirname "$tap")")
                repo=$(basename "$tap")
                echo "Repairing tap git metadata: ${owner}/${repo}"
                git -C "$tap" init || true
                git -C "$tap" remote add origin "https://github.com/${owner}/${repo}" || true
                git -C "$tap" fetch origin master --depth=1 2>/dev/null || git -C "$tap" fetch origin main --depth=1 || true
                if git -C "$tap" show-ref --verify --quiet refs/remotes/origin/master; then
                    git -C "$tap" reset --hard origin/master || true
                elif git -C "$tap" show-ref --verify --quiet refs/remotes/origin/main; then
                    git -C "$tap" reset --hard origin/main || true
                fi
            elif ! git -C "$tap" remote get-url origin >/dev/null 2>&1; then
                owner=$(basename "$(dirname "$tap")")
                repo=$(basename "$tap")
                git -C "$tap" remote add origin "https://github.com/${owner}/${repo}" || true
            fi
        done
    fi
fi
brew update && brew upgrade --greedy && brew cleanup || true

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
if command -v npm >/dev/null 2>&1; then
    npm config set prefix "$HOME/.npm-global" >/dev/null 2>&1 || true
    npm config set fund false >/dev/null 2>&1 || true
    global_packages=$(npm list -g --depth=0 --json 2>/dev/null | jq -r '.dependencies | keys[]' 2>/dev/null | xargs)
    if [ -n "$global_packages" ]; then
        for pkg in $global_packages; do
            pkg_dir="$HOME/.npm-global/lib/node_modules"
            scope_dir=""
            name_dir=""
            if [[ "$pkg" == @*/* ]]; then
                scope_dir="$pkg_dir/$(echo "$pkg" | cut -d/ -f1)"
                name_dir="$(echo "$pkg" | cut -d/ -f2)"
                find "$scope_dir" -maxdepth 1 -type d -name ".${name_dir}-*" 2>/dev/null | xargs rm -rf || true
            else
                find "$pkg_dir" -maxdepth 1 -type d -name ".${pkg}-*" 2>/dev/null | xargs rm -rf || true
            fi

            NPM_CONFIG_LOGLEVEL=error NPM_CONFIG_FUND=false npm install -g --no-fund "$pkg" || NPM_CONFIG_LOGLEVEL=error NPM_CONFIG_FUND=false npm install -g --no-fund --force "$pkg" || true
        done
    fi
fi

echo ""
echo "✅ All agents, packages & tools updated!"

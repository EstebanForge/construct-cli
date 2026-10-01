#!/usr/bin/env bash
set -e

if [ "$(id -u)" = "0" ]; then
    exec gosu construct "$0" "$@"
fi

echo "Updating all agents, packages & tools..."
echo ""

# Clear patching marker to ensure re-patching after updates
rm -f "$HOME/.construct_patched"

echo "=== Construct update diagnostics ==="
echo "Timestamp (UTC): $(date -u +%Y-%m-%dT%H:%M:%SZ)"
echo "User IDs: uid=$(id -u) gid=$(id -g)"
echo "User names: user=$(id -un 2>/dev/null || echo unknown) group=$(id -gn 2>/dev/null || echo unknown)"
echo "HOME: $HOME"
echo "SHELL: ${SHELL:-unknown}"
echo "PATH: $PATH"
if command -v mise &> /dev/null; then
    echo "mise: $(mise --version 2>/dev/null | head -1)"
else
    echo "mise: not found"
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

# mise self-update prompts before replacing its binary; topgrade runs it
# without a TTY, so an unanswered prompt aborts the step (topgrade marks it
# IGNORED and mise never updates). MISE_YES=1 answers every mise
# confirmation, including the manual fallback path below.
export MISE_YES=1

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
        topgrade -y --disable system,claude_code,pi
    fi
else
    echo "topgrade not found, falling back to manual updates..."
    # NOTE: baked agents (claude/codex/agy/pi/opencode) update on the image
    # lane — never in-guest. This fallback only refreshes root-disk state.

    if [ -n "$SUDO" ] || [ "$(id -u)" = "0" ]; then
        echo "Updating system packages (apt)..."
        $SUDO apt-get update -qq && $SUDO apt-get -y -qq dist-upgrade && $SUDO apt-get -y -qq autoremove && $SUDO apt-get -y -qq autoclean || true
    fi

    if command -v mise &> /dev/null; then
        echo "Updating mise tools..."
        mise upgrade --yes || true
    fi
fi

# Upgrade npm global packages to latest (npm update -g doesn't cross semver boundaries)
if command -v npm &> /dev/null; then
    mkdir -p "$HOME/.npm-global"
    npm config set prefix "$HOME/.npm-global" || true
    export PATH="$HOME/.npm-global/bin:$PATH"
    echo ""
    echo "Upgrading npm global packages to latest..."
    # Sweep stale npm temp entries before reinstalling. An interrupted npm
    # run leaves dot-prefixed temp dirs under the global node_modules
    # (.<pkg>-XXXXXX for plain packages, .@scope/ holding the temp for
    # scoped ones, and newer npm nests the temp inside the real scope
    # dir: @scope/.<pkg>-XXXXXX) and later installs fail with ENOTEMPTY
    # when npm renames onto the occupied destination. npm never cleans
    # these up itself; .bin is the only legitimate dot-entry here.
    npm_global_lib="$HOME/.npm-global/lib/node_modules"
    if [ -d "$npm_global_lib" ]; then
        for entry in "$npm_global_lib"/.[!.]* "$npm_global_lib"/@*/.[!.]*; do
            # -L keeps broken symlinks in the sweep: [ -e ] alone would
            # stop the loop early on the first dangling link.
            [ -e "$entry" ] || [ -L "$entry" ] || continue
            [ "$(basename "$entry")" = ".bin" ] && continue
            rm -rf "$entry"
        done
    fi
    # Reinstall only packages with a newer version on the registry. The
    # old path reinstalled every global package (~35 serial npm runs,
    # minutes of work plus mise reshim churn) even when nothing moved:
    # npm install pkg@latest never skips an up-to-date package, which is
    # why the explicit reinstall stays instead of npm update -g (that
    # one does not cross semver ranges). npm outdated does one registry
    # sweep. Validate the payload before trusting it: on registry or
    # network failure npm prints {"error":{...}} on STDOUT (rc=1), and
    # blindly extracting keys would run `npm install -g error@latest`
    # against the unrelated public package. Empty stdout (nothing to
    # check) also lands in the warn branch rather than a fake success.
    npm_outdated=$(npm outdated -g --json 2>/dev/null || true)
    if [ -n "$npm_outdated" ] && echo "$npm_outdated" | jq -e 'type == "object" and (has("error") | not)' > /dev/null 2>&1; then
        npm_pkgs=$(echo "$npm_outdated" | jq -r 'to_entries[] | select(.key != "npm" and (.value | type == "object") and .value.current) | .key')
        if [ -n "$npm_pkgs" ]; then
            for pkg in $npm_pkgs; do
                npm install -g --force "$pkg@latest" || echo "⚠️  Failed to upgrade $pkg"
            done
        else
            echo "  No outdated npm global packages"
        fi
    else
        echo "⚠️  npm outdated check failed; skipping npm global upgrades"
    fi
fi

# pi user-tier resources (extensions, model catalogs) live in ~/.pi and
# always update in-guest. The baked /usr/local/bin/pi is construct-owned
# (the image chowns the agent tier), so self-update writes its install
# prefix in place; on older images where it is still root-owned, only
# extensions and models update and self-update stays on the image lane.
# --no-approve keeps the run non-interactive; project-local files are
# never trusted from a routine.
if command -v pi &> /dev/null; then
    echo ""
    echo "Updating pi (extensions, models, self)..."
    if [ -w "$(command -v pi)" ]; then
        pi update --no-approve --all || echo "⚠️  pi update --all failed"
    else
        echo "  pi binary is baked root-owned; self-update stays on the image lane"
        pi update --no-approve --extensions || echo "⚠️  pi extension update failed"
    fi
    pi update --no-approve --models || echo "⚠️  pi model catalog refresh failed"
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
# Guest hash gate lives on the root fs (resets on recreate); the bind copy
# mirrors it for the host CLI. Gate write is best-effort: old images lack
# /var/lib/construct-cli ownership, the mirror is what the host reads.
GATE_DIR="/var/lib/construct-cli"
mkdir -p "$GATE_DIR" 2>/dev/null || true
if [ -w "$GATE_DIR" ]; then
    HASH_FILE="$GATE_DIR/.entrypoint_hash"
else
    HASH_FILE="$HOME/.local/.entrypoint_hash"
fi
MIRROR_FILE="$HOME/.local/.entrypoint_hash"
USER_INSTALL_SCRIPT="$HOME/.config/construct-cli/container/install_user_packages.sh"
HASH_UTILS="$HOME/.config/construct-cli/container/entrypoint-hash.sh"
if [ -f "$HASH_UTILS" ]; then
    # shellcheck source=/dev/null
    . "$HASH_UTILS"
fi

if command -v write_entrypoint_hash >/dev/null 2>&1; then
    write_entrypoint_hash "$HASH_FILE" "$ENTRYPOINT_SCRIPT" "$USER_INSTALL_SCRIPT"
    write_entrypoint_hash "$MIRROR_FILE" "$ENTRYPOINT_SCRIPT" "$USER_INSTALL_SCRIPT" 2>/dev/null || true
elif [ -f "$ENTRYPOINT_SCRIPT" ]; then
    CURRENT_HASH=$(sha256sum "$ENTRYPOINT_SCRIPT" | awk '{print $1}')
    if [ -f "$USER_INSTALL_SCRIPT" ]; then
        INSTALL_HASH=$(sha256sum "$USER_INSTALL_SCRIPT" | awk '{print $1}')
        CURRENT_HASH="${CURRENT_HASH}-${INSTALL_HASH}"
    fi
    mkdir -p "$HOME/.local"
    echo "$CURRENT_HASH" > "$HASH_FILE"
    echo "$CURRENT_HASH" > "$MIRROR_FILE" 2>/dev/null || true
fi

echo ""
echo "Post-update command verification..."
missing_cmds=""
for cmd in claude amp copilot opencode qwen cline crush codex goose agy kilocode pi pig; do
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

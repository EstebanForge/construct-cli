#!/usr/bin/env bash
# reset-environment.sh - Complete reset of Construct CLI environment
# Usage: ./scripts/reset-environment.sh [--all]
#   --all    Also delete config directory (~/.config/construct-cli/)

# set -e  # Disabled to prevent script from exiting on cleanup failures

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo -e "${YELLOW}🔄 Construct CLI Environment Reset${NC}"
echo ""

# Parse arguments
DELETE_CONFIG=false
if [ "$1" == "--all" ]; then
    DELETE_CONFIG=true
    echo -e "${YELLOW}⚠️  Will also delete config directory${NC}"
    echo ""
fi

# Detect container runtime
RUNTIME=""
if command -v docker &> /dev/null; then
    RUNTIME="docker"
elif command -v podman &> /dev/null; then
    RUNTIME="podman"
else
    echo -e "${RED}Error: No container runtime found (docker or podman)${NC}"
    exit 1
fi

echo "Using runtime: $RUNTIME"
echo ""

# Function to run command and ignore errors
run_cleanup() {
    local cmd="$1"
    local desc="$2"

    echo -n "  $desc... "
    if eval "$cmd" &> /dev/null; then
        echo -e "${GREEN}✓${NC}"
        return 0
    else
        echo -e "${YELLOW}(none found)${NC}"
        return 1
    fi
}

# 1. Stop running construct containers
echo "1. Stopping running containers:"
run_cleanup "$RUNTIME stop construct-cli construct-session construct-cli-daemon" "Stopping containers" || true

# 2. Remove construct containers
echo ""
echo "2. Removing containers:"
run_cleanup "$RUNTIME rm -f construct-cli construct-session construct-cli-daemon" "Removing containers"

# 2.1. Force remove ANY remaining construct containers (crucial for releasing volumes)
echo ""
echo "2.1. Ensuring all construct containers are gone:"
for container in $($RUNTIME ps -a -q --filter "name=construct" || true); do
    echo -n "  Force removing container $container... "
    $RUNTIME rm -f "$container" &> /dev/null && echo -e "${GREEN}✓${NC}" || echo -e "${YELLOW}skipped${NC}"
done

# 3. Remove construct image
echo ""
echo "3. Removing images:"
run_cleanup "$RUNTIME rmi -f construct-box:latest" "Removing construct-box:latest"

# 4. Remove construct volumes
echo ""
echo "4. Removing volumes:"
run_cleanup "$RUNTIME volume rm construct-agents" "Removing construct-agents"
run_cleanup "$RUNTIME volume rm construct-packages" "Removing construct-packages"
run_cleanup "$RUNTIME volume rm construct-home" "Removing construct-home"
run_cleanup "$RUNTIME volume rm construct-config" "Removing construct-config"

# Also remove any orphaned volumes that might be stuck
echo ""
echo "4.1. Removing any orphaned construct volumes:"
for vol in $($RUNTIME volume ls -q | grep -i construct || true); do
    echo -n "  Removing volume $vol... "
    $RUNTIME volume rm "$vol" &> /dev/null && echo -e "${GREEN}✓${NC}" || echo -e "${YELLOW}skipped${NC}"
done

# 5. Remove construct network
echo ""
echo "5. Removing network:"
run_cleanup "$RUNTIME network rm construct-net construct-cli" "Removing networks"

# 6. Clean up additional Docker resources
echo ""
echo "6. Removing dangling construct images:"
for img in $($RUNTIME images --filter "dangling=true" -q | xargs $RUNTIME inspect --format='{{.Id}}' 2>/dev/null | grep -i construct || true); do
    echo -n "  Removing dangling image $img... "
    $RUNTIME rmi -f "$img" &> /dev/null && echo -e "${GREEN}✓${NC}" || echo -e "${YELLOW}skipped${NC}"
done

# 7. Clean build cache
echo ""
echo "7. Cleaning build cache:"
run_cleanup "$RUNTIME builder prune -f" "Pruning build cache"

# 9. Delete config directory (if --all flag)
if [ "$DELETE_CONFIG" = true ]; then
    echo ""
    echo "9. Removing config directory:"
    CONFIG_DIR="$HOME/.config/construct-cli"
    if [ -d "$CONFIG_DIR" ]; then
        echo -n "  Deleting $CONFIG_DIR... "
        rm -rf "$CONFIG_DIR"
        echo -e "${GREEN}✓${NC}"
    else
        echo -e "  ${YELLOW}(not found)${NC}"
    fi

    # Also remove local directories that might have permission issues
    echo ""
    echo "9.1. Removing local directories:"

    # Remove local bin directory
    LOCAL_BIN="$HOME/.local/bin"
    if [ -d "$LOCAL_BIN" ]; then
        echo -n "  Checking $LOCAL_BIN for construct tools... "
        if ls "$LOCAL_BIN"/*construct* &>/dev/null || ls "$LOCAL_BIN"/*cursor* &>/dev/null; then
            rm -f "$LOCAL_BIN"/*construct* "$LOCAL_BIN"/*cursor* 2>/dev/null || true
            echo -e "${GREEN}✓ cleaned${NC}"
        else
            echo -e "${YELLOW}no construct tools found${NC}"
        fi
    fi

    # Remove agents installation marker
    AGENTS_MARKER="$HOME/.local/.agents-installed"
    if [ -f "$AGENTS_MARKER" ]; then
        echo -n "  Removing agents installation marker... "
        rm -f "$AGENTS_MARKER"
        echo -e "${GREEN}✓${NC}"
    fi
fi

# 10. Final force cleanup
echo ""
echo "10. Final force cleanup of remaining construct resources:"

# Force remove all construct volumes
for vol in $($RUNTIME volume ls -q | grep -i construct || true); do
    echo -n "  Force removing volume $vol... "
    $RUNTIME volume rm -f "$vol" 2>/dev/null && echo -e "${GREEN}✓${NC}" || echo -e "${YELLOW}failed${NC}"
done

# Force remove all construct networks
for net in $($RUNTIME network ls -q | grep -i construct || true); do
    echo -n "  Force removing network $net... "
    $RUNTIME network rm -f "$net" 2>/dev/null && echo -e "${GREEN}✓${NC}" || echo -e "${YELLOW}failed${NC}"
done

# Kill any remaining running construct containers
for container in $($RUNTIME ps -q --filter "name=construct" || true); do
    echo -n "  Force killing container $container... "
    $RUNTIME kill "$container" 2>/dev/null && echo -e "${GREEN}✓${NC}" || echo -e "${YELLOW}failed${NC}"
done

# Force remove any remaining construct containers (including running ones)
for container in $($RUNTIME ps -a -q --filter "name=construct" || true); do
    echo -n "  Force removing container $container... "
    $RUNTIME rm -f "$container" 2>/dev/null && echo -e "${GREEN}✓${NC}" || echo -e "${YELLOW}failed${NC}"
done

echo ""
echo -e "${GREEN}✅ Cleanup complete!${NC}"
echo ""
echo "Next steps:"
echo "  1. Rebuild: make build"
echo "  2. Test: ./bin/construct shell"
echo ""

# Show remaining construct-related resources (if any)
echo "Remaining construct-related resources:"
echo ""

echo "Containers:"
$RUNTIME ps -a | grep -i construct || echo "  (none)"
echo ""

echo "Images:"
$RUNTIME images | grep -i construct || echo "  (none)"
echo ""

echo "Volumes:"
$RUNTIME volume ls | grep -i construct || echo "  (none)"
echo ""

echo "Networks:"
$RUNTIME network ls | grep -i construct || echo "  (none)"

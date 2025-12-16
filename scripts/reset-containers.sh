#!/usr/bin/env bash
# reset-containers.sh - Reset container environment while preserving home directory and config

set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

CONFIG_DIR="$HOME/.config/construct-cli"
CONTAINER_DIR="$CONFIG_DIR/container"
HOME_DIR="$CONFIG_DIR/home"
CONFIG_FILE="$CONFIG_DIR/config.toml"

echo -e "${BLUE}🔄 Construct CLI Container Reset${NC}"
echo -e "${YELLOW}Preserving home directory and config.toml${NC}"
echo ""

# Detect available runtime
detect_runtime() {
    if command -v container >/dev/null 2>&1; then
        echo "container"
    elif command -v podman >/dev/null 2>&1; then
        echo "podman"
    elif command -v docker >/dev/null 2>&1; then
        echo "docker"
    else
        echo "none"
    fi
}

RUNTIME=$(detect_runtime)
echo "Using runtime: $RUNTIME"
echo ""

if [ "$RUNTIME" = "none" ]; then
    echo -e "${RED}Error: No container runtime found${NC}"
    exit 1
fi

# Function to execute docker/podman/container commands
run_cmd() {
    if [ "$RUNTIME" = "container" ]; then
        container "$@"
    else
        "$RUNTIME" "$@"
    fi
}

# 1. Stop running containers
echo "1. Stopping running containers:"
mapfile -t running_containers < <(run_cmd ps -q --filter "name=construct" 2>/dev/null || true)
if [ ${#running_containers[@]} -gt 0 ]; then
    echo -n "  Stopping containers... "
    run_cmd stop "${running_containers[@]}" 2>/dev/null || true
    echo -e "${GREEN}✓${NC}"
else
    echo -e "  ${YELLOW}No running containers found${NC}"
fi
echo ""

# 2. Remove containers
echo "2. Removing containers:"
mapfile -t all_containers < <(run_cmd ps -aq --filter "name=construct" 2>/dev/null || true)
if [ ${#all_containers[@]} -gt 0 ]; then
    echo -n "  Removing containers... "
    run_cmd rm "${all_containers[@]}" 2>/dev/null || true
    echo -e "${GREEN}✓${NC}"
else
    echo -e "  ${YELLOW}No containers found${NC}"
fi
echo ""

# 3. Remove images
echo "3. Removing images:"
if run_cmd images -q construct-box:latest 2>/dev/null | grep -q .; then
    echo -n "  Removing construct-box:latest... "
    run_cmd rmi construct-box:latest 2>/dev/null || true
    echo -e "${GREEN}✓${NC}"
else
    echo -e "  ${YELLOW}No construct-box image found${NC}"
fi
echo ""

# 4. Remove volumes (EXCEPT home directory)
echo "4. Removing volumes:"
VOLUMES=("construct-packages" "construct-agents" "construct-config")
for volume in "${VOLUMES[@]}"; do
    if run_cmd volume ls -q --filter "name=$volume" 2>/dev/null | grep -q .; then
        echo -n "  Removing $volume... "
        run_cmd volume rm "$volume" 2>/dev/null || true
        echo -e "${GREEN}✓${NC}"
    else
        echo -e "  ${YELLOW}$volume not found${NC}"
    fi
done

# 4.1 Remove any other construct volumes (but preserve home directory)
echo ""
echo "4.1. Removing other construct volumes:"
if run_cmd volume ls -q --filter "name=construct" 2>/dev/null | grep -q .; then
    for volume in $(run_cmd volume ls -q --filter "name=construct" 2>/dev/null); do
        # Skip volumes that might contain home data
        if [[ "$volume" != *"home"* ]] && [[ "$volume" != *"config"* ]]; then
            echo -n "  Removing volume $volume... "
            run_cmd volume rm "$volume" 2>/dev/null || echo -e "${YELLOW}skipped${NC}"
        fi
    done
fi
echo ""

# 5. Remove networks
echo "5. Removing networks:"
mapfile -t construct_networks < <(run_cmd network ls -q --filter "name=construct" 2>/dev/null || true)
if [ ${#construct_networks[@]} -gt 0 ]; then
    echo -n "  Removing networks... "
    run_cmd network rm "${construct_networks[@]}" 2>/dev/null || true
    echo -e "${GREEN}✓${NC}"
else
    echo -e "  ${YELLOW}No construct networks found${NC}"
fi
echo ""

# 6. Clean build cache
echo "6. Cleaning build cache:"
if command -v docker >/dev/null 2>&1; then
    echo -n "  Pruning build cache... "
    docker builder prune -f >/dev/null 2>&1 || true
    echo -e "${GREEN}✓${NC}"
elif command -v podman >/dev/null 2>&1; then
    echo -n "  Pruning build cache... "
    podman system prune -f --volumes >/dev/null 2>&1 || true
    echo -e "${GREEN}✓${NC}"
fi
echo ""

# 7. Remove container directory (rebuilds templates)
echo "7. Removing container directory:"
if [ -d "$CONTAINER_DIR" ]; then
    echo -n "  Removing $CONTAINER_DIR... "
    rm -rf "$CONTAINER_DIR"
    echo -e "${GREEN}✓${NC}"
else
    echo -e "  ${YELLOW}Container directory not found${NC}"
fi
echo ""

# 8. Remove agent installation marker to force reinstallation
echo "8. Resetting agent installation marker:"
if [ -f "$HOME_DIR/.local/.agents-installed" ]; then
    echo -n "  Removing agents-installed marker... "
    rm -f "$HOME_DIR/.local/.agents-installed"
    echo -e "${GREEN}✓${NC}"
else
    echo -e "  ${YELLOW}No agents-installed marker found${NC}"
fi
echo ""

# 9. Show preserved items
echo "9. Preserved items:"
echo -e "  ${GREEN}✓${NC} Home directory: $HOME_DIR"
echo -e "  ${GREEN}✓${NC} Config file: $CONFIG_FILE"
echo ""

# 10. Summary
echo -e "${GREEN}✅ Container reset complete!${NC}"
echo ""
echo "Next steps:"
echo "  1. Rebuild: ./construct sys init"
echo "  2. Test: ./construct shell"
echo ""
echo -e "${BLUE}Home directory and config.toml preserved.${NC}"
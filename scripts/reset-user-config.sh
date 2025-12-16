#!/usr/bin/env bash
# reset-user-config.sh - Clean user config directory for testing
# Usage: ./scripts/reset-user-config.sh

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo -e "${YELLOW}🧹 Cleaning Construct CLI User Config${NC}"
echo ""

# 1. Clean user config directory
echo "1. Removing config directory:"
CONFIG_DIR="$HOME/.config/construct-cli"
if [ -d "$CONFIG_DIR" ]; then
    echo -n "  Deleting $CONFIG_DIR... "
    rm -rf "$CONFIG_DIR"
    echo -e "${GREEN}✓${NC}"
else
    echo -e "  ${YELLOW}(not found)${NC}"
fi

# 2. Clean local installations
echo ""
echo "2. Removing local installations:"

# Remove local bin directory entries
LOCAL_BIN="$HOME/.local/bin"
if [ -d "$LOCAL_BIN" ]; then
    echo -n "  Cleaning $LOCAL_BIN... "
    if ls "$LOCAL_BIN"/*construct* &>/dev/null || ls "$LOCAL_BIN"/*cursor* &>/dev/null || ls "$LOCAL_BIN"/*claude* &>/dev/null; then
        rm -f "$LOCAL_BIN"/*construct* "$LOCAL_BIN"/*cursor* "$LOCAL_BIN"/*claude* 2>/dev/null || true
        echo -e "${GREEN}✓ cleaned${NC}"
    else
        echo -e "${YELLOW}no construct tools found${NC}"
    fi
fi

# 3. Clean temp directories
echo ""
echo "3. Cleaning temp directories:"
TEMP_DIRS="/tmp/construct-* /tmp/construct-*-*"
for dir in $TEMP_DIRS; do
    if [ -d "$dir" ]; then
        echo -n "  Removing $dir... "
        rm -rf "$dir"
        echo -e "${GREEN}✓${NC}"
    fi
done

echo ""
echo -e "${GREEN}✅ User config cleanup complete!${NC}"
echo ""
echo "Next steps:"
echo "  1. Rebuild: make build"
echo "  2. Test: ./construct shell"
echo ""
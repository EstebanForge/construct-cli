#!/usr/bin/env bash
# reset-all.sh - Complete reset of Construct CLI (both user config and environment)
# Usage: ./scripts/reset-all.sh [--all]
#   --all    Also delete config directory (passed through to reset-environment.sh)

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo -e "${YELLOW}🔄 Complete Construct CLI Reset${NC}"
echo ""

# Get script directory
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo "This will run:"
echo "  1. reset-user-config.sh  - Clean local configuration and caches"
echo "  2. reset-environment.sh - Clean Docker containers, images, and volumes"
echo ""

# Check if scripts exist
if [ ! -f "$SCRIPT_DIR/reset-user-config.sh" ]; then
    echo -e "${RED}Error: reset-user-config.sh not found in $SCRIPT_DIR${NC}"
    exit 1
fi

if [ ! -f "$SCRIPT_DIR/reset-environment.sh" ]; then
    echo -e "${RED}Error: reset-environment.sh not found in $SCRIPT_DIR${NC}"
    exit 1
fi

# Run user config reset first
echo -e "${YELLOW}1. Cleaning user configuration...${NC}"
if "$SCRIPT_DIR/reset-user-config.sh"; then
    echo -e "${GREEN}✓ User config reset complete${NC}"
else
    echo -e "${YELLOW}⚠ User config reset completed with warnings${NC}"
fi

echo ""

# Run environment reset
echo -e "${YELLOW}2. Cleaning Docker environment...${NC}"
if "$SCRIPT_DIR/reset-environment.sh" "$@"; then
    echo -e "${GREEN}✓ Environment reset complete${NC}"
else
    echo -e "${YELLOW}⚠ Environment reset completed with warnings${NC}"
fi

echo ""
echo -e "${GREEN}✅ Complete reset finished!${NC}"
echo ""
echo "Next steps:"
echo "  1. Rebuild: make build"
echo "  2. Test: ./bin/construct shell"
echo ""
echo -e "${YELLOW}Note: All your API keys and local configurations have been removed.${NC}"
echo -e "${YELLOW}      You'll need to reconfigure them in ~/.config/construct-cli/config.toml${NC}"

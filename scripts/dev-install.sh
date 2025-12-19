#!/usr/bin/env bash
# Quick dev install - build and install in one command for rapid iteration
# No confirmations, no backups, just fast installation

set -euo pipefail

# Detect script directory
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

# Default to user local bin (no sudo needed)
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"
BINARY_NAME="construct"

echo "🔨 Building..."
cd "$PROJECT_ROOT"
go build -o "$BINARY_NAME" ./cmd/construct

# Ad-hoc code sign on macOS (required for Gatekeeper)
if [[ "$(uname)" == "Darwin" ]]; then
    codesign -s - -f "$BINARY_NAME" 2>/dev/null || true
fi

echo "📦 Installing to $INSTALL_DIR/$BINARY_NAME..."
mkdir -p "$INSTALL_DIR"
cp "$BINARY_NAME" "$INSTALL_DIR/"
chmod +x "$INSTALL_DIR/$BINARY_NAME"

# Sign installed binary too
if [[ "$(uname)" == "Darwin" ]]; then
    codesign -s - -f "$INSTALL_DIR/$BINARY_NAME" 2>/dev/null || true
fi

echo "✓ Done! Version:"
"$INSTALL_DIR/$BINARY_NAME" --version || echo "(version check failed)"

echo ""
echo "Quick test:"
echo "  construct --version"
echo "  construct sys doctor"

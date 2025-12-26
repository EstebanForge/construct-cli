#!/usr/bin/env bash
# Quick dev install - build and install in one command for rapid iteration
# Wrapper around install-local.sh

set -e

# Detect script directory
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Run install-local.sh with dev options
echo "🚀 Running dev install..."
exec "$SCRIPT_DIR/install-local.sh" --force --no-backup "$@"

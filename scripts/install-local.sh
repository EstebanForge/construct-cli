#!/usr/bin/env bash
# Install locally compiled construct binary for testing and debugging
# This script compiles and installs the binary to your local system

set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Detect script directory (project root)
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

# Installation locations
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"
BINARY_NAME="construct"
BACKUP_SUFFIX=".backup.$(date +%Y%m%d_%H%M%S)"

# Build options
BUILD_FLAGS="-v"
OUTPUT_BINARY="${PROJECT_ROOT}/${BINARY_NAME}"

# Function to print colored messages
print_info() {
    echo -e "${BLUE}ℹ${NC}  $*"
}

print_success() {
    echo -e "${GREEN}✓${NC}  $*"
}

print_warning() {
    echo -e "${YELLOW}⚠${NC}  $*"
}

print_error() {
    echo -e "${RED}✗${NC}  $*"
}

# Function to check if running with sudo when needed
check_sudo() {
    if [[ "$INSTALL_DIR" == /usr/* ]] || [[ "$INSTALL_DIR" == /opt/* ]]; then
        if [[ $EUID -ne 0 ]] && [[ -z "${SUDO_USER:-}" ]]; then
            print_error "Installation to $INSTALL_DIR requires sudo privileges"
            echo ""
            echo "Run with sudo:"
            echo "  sudo INSTALL_DIR=$INSTALL_DIR $0"
            echo ""
            echo "Or use the default user directory:"
            echo "  $0"
            exit 1
        fi
    fi
}

# Function to build the binary
build_binary() {
    print_info "Building construct binary..."
    cd "$PROJECT_ROOT"

    if ! command -v go &> /dev/null; then
        print_error "Go is not installed or not in PATH"
        exit 1
    fi

    # Clean previous build
    rm -f "$OUTPUT_BINARY"

    # Build
    if go build $BUILD_FLAGS -o "$OUTPUT_BINARY" ./cmd/construct; then
        print_success "Build successful: $OUTPUT_BINARY"
    else
        print_error "Build failed"
        exit 1
    fi

    # Show binary info
    echo ""
    print_info "Binary information:"
    echo "  Size: $(du -h "$OUTPUT_BINARY" | cut -f1)"
    echo "  SHA256: $(shasum -a 256 "$OUTPUT_BINARY" | cut -d' ' -f1)"
    if [[ -x "$OUTPUT_BINARY" ]]; then
        echo "  Version: $("$OUTPUT_BINARY" --version 2>/dev/null || echo 'N/A')"
    fi
    echo ""
}

# Function to backup existing binary
backup_existing() {
    local target="$INSTALL_DIR/$BINARY_NAME"

    if [[ -f "$target" ]]; then
        local backup_path="${target}${BACKUP_SUFFIX}"
        print_warning "Existing binary found, creating backup..."

        if cp "$target" "$backup_path"; then
            print_success "Backup created: $backup_path"
        else
            print_error "Failed to create backup"
            return 1
        fi

        # Show difference if it's a construct binary
        if "$target" --version &>/dev/null; then
            echo "  Current version: $("$target" --version | head -1)"
        fi
    fi
}

# Function to install the binary
install_binary() {
    local source="$OUTPUT_BINARY"
    local target="$INSTALL_DIR/$BINARY_NAME"

    print_info "Installing to $target..."

    # Ensure install directory exists
    if [[ ! -d "$INSTALL_DIR" ]]; then
        print_info "Creating directory: $INSTALL_DIR"
        mkdir -p "$INSTALL_DIR"
    fi

    # Copy binary
    if cp "$source" "$target"; then
        chmod +x "$target"

        # Ad-hoc code sign on macOS (required for Gatekeeper)
        if [[ "$(uname)" == "Darwin" ]]; then
            if command -v codesign &>/dev/null; then
                codesign -s - -f "$target" 2>/dev/null || true
            fi
        fi

        print_success "Installed: $target"
    else
        print_error "Failed to install binary"
        exit 1
    fi
}

# Function to verify installation
verify_installation() {
    local target="$INSTALL_DIR/$BINARY_NAME"

    print_info "Verifying installation..."

    # Check if in PATH
    if command -v "$BINARY_NAME" &> /dev/null; then
        local which_path
        which_path=$(command -v "$BINARY_NAME")

        if [[ "$which_path" == "$target" ]]; then
            print_success "Binary is in PATH: $which_path"
        else
            print_warning "Binary installed but another version found in PATH: $which_path"
            print_warning "Installed version: $target"
        fi
    else
        print_warning "Binary not found in PATH"
        print_info "You may need to add $INSTALL_DIR to your PATH"
        echo ""
        echo "Add to your ~/.bashrc or ~/.zshrc:"
        echo "  export PATH=\"$INSTALL_DIR:\$PATH\""
    fi

    # Test execution
    if "$target" --version &>/dev/null; then
        echo ""
        print_success "Installation verified successfully!"
        echo ""
        "$target" --version
    else
        print_warning "Binary installed but --version check failed"
    fi
}

# Function to show usage
show_usage() {
    cat <<EOF
${GREEN}Construct Local Install Script${NC}

Builds and installs the construct binary locally for testing and debugging.

${BLUE}Usage:${NC}
  $0 [options]

${BLUE}Options:${NC}
  --clean         Clean build artifacts before building
  --skip-build    Skip build step (use existing binary)
  --no-backup     Don't backup existing binary
  --force         Skip all confirmations
  -h, --help      Show this help message

${BLUE}Environment Variables:${NC}
  INSTALL_DIR     Installation directory (default: ~/.local/bin)
                  Example: INSTALL_DIR=/usr/local/bin $0

${BLUE}Examples:${NC}
  # Standard installation (no sudo needed)
  $0

  # Install to system directory (requires sudo)
  sudo INSTALL_DIR=/usr/local/bin $0

  # Quick rebuild and install
  $0 --no-backup

  # Install without rebuilding
  $0 --skip-build

  # Clean build
  $0 --clean

${BLUE}After Installation:${NC}
  # Test the installed binary
  construct --version

  # Compare with production
  construct sys version

  # Run in debug mode
  construct --ct-debug sys shell
EOF
}

# Parse command line arguments
CLEAN=false
SKIP_BUILD=false
NO_BACKUP=false
FORCE=false

while [[ $# -gt 0 ]]; do
    case $1 in
        --clean)
            CLEAN=true
            shift
            ;;
        --skip-build)
            SKIP_BUILD=true
            shift
            ;;
        --no-backup)
            NO_BACKUP=true
            shift
            ;;
        --force)
            FORCE=true
            shift
            ;;
        -h|--help)
            show_usage
            exit 0
            ;;
        *)
            print_error "Unknown option: $1"
            echo ""
            show_usage
            exit 1
            ;;
    esac
done

# Main installation flow
main() {
    echo ""
    echo -e "${GREEN}═══════════════════════════════════════════════════${NC}"
    echo -e "${GREEN}  The Construct CLI - Local Installation${NC}"
    echo -e "${GREEN}═══════════════════════════════════════════════════${NC}"
    echo ""

    print_info "Project root: $PROJECT_ROOT"
    print_info "Install location: $INSTALL_DIR/$BINARY_NAME"
    echo ""

    # Check sudo if needed
    check_sudo

    # Clean if requested
    if [[ "$CLEAN" == true ]]; then
        print_info "Cleaning build artifacts..."
        cd "$PROJECT_ROOT"
        go clean
        rm -f "$OUTPUT_BINARY"
        print_success "Clean complete"
        echo ""
    fi

    # Build binary
    if [[ "$SKIP_BUILD" == false ]]; then
        build_binary
    elif [[ ! -f "$OUTPUT_BINARY" ]]; then
        print_error "No binary found at $OUTPUT_BINARY"
        print_info "Run without --skip-build to build first"
        exit 1
    else
        print_info "Using existing binary: $OUTPUT_BINARY"
        echo ""
    fi

    # Confirm installation if not forced
    if [[ "$FORCE" == false ]]; then
        echo -e "${YELLOW}Ready to install:${NC}"
        echo "  From: $OUTPUT_BINARY"
        echo "  To:   $INSTALL_DIR/$BINARY_NAME"
        echo ""
        read -p "Continue? [y/N] " -n 1 -r
        echo ""
        if [[ ! $REPLY =~ ^[Yy]$ ]]; then
            print_info "Installation cancelled"
            exit 0
        fi
        echo ""
    fi

    # Backup existing binary
    if [[ "$NO_BACKUP" == false ]]; then
        backup_existing
        echo ""
    fi

    # Install
    install_binary
    echo ""

    # Verify
    verify_installation

    echo ""
    echo -e "${GREEN}═══════════════════════════════════════════════════${NC}"
    print_success "Installation complete!"
    echo -e "${GREEN}═══════════════════════════════════════════════════${NC}"
    echo ""

    # Show quick test commands
    print_info "Quick test commands:"
    echo "  construct --version"
    echo "  construct sys doctor"
    echo "  construct --ct-debug sys shell"
    echo ""
}

# Run main
main

#!/usr/bin/env bash
# Uninstall locally installed construct binary and restore backups

set -euo pipefail

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"
BINARY_NAME="construct"
TARGET="$INSTALL_DIR/$BINARY_NAME"

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

check_sudo() {
    if [[ "$INSTALL_DIR" == /usr/* ]] || [[ "$INSTALL_DIR" == /opt/* ]]; then
        if [[ $EUID -ne 0 ]] && [[ -z "${SUDO_USER:-}" ]]; then
            print_error "Uninstallation from $INSTALL_DIR requires sudo"
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

show_usage() {
    cat <<EOF
${GREEN}Construct Uninstall Script${NC}

Removes locally installed construct binary.

${BLUE}Usage:${NC}
  $0 [options]

${BLUE}Options:${NC}
  --restore       Restore most recent backup
  --list-backups  List available backups
  --force         Skip confirmation
  -h, --help      Show this help

${BLUE}Environment Variables:${NC}
  INSTALL_DIR     Installation directory (default: ~/.local/bin)

${BLUE}Examples:${NC}
  # Remove installed binary (no sudo needed)
  $0

  # Remove and restore backup
  $0 --restore

  # List available backups
  $0 --list-backups

  # Remove from system directory (requires sudo)
  sudo INSTALL_DIR=/usr/local/bin $0
EOF
}

list_backups() {
    print_info "Looking for backups in $INSTALL_DIR..."
    echo ""

    local backups=("$INSTALL_DIR"/"$BINARY_NAME".backup.*)
    local found=false

    for backup in "${backups[@]}"; do
        if [[ -f "$backup" ]]; then
            found=true
            echo "  $(basename "$backup")"
            if [[ -x "$backup" ]]; then
                local version=$("$backup" --version 2>/dev/null | head -1 || echo "unknown version")
                echo "    Version: $version"
            fi
            echo "    Size: $(du -h "$backup" | cut -f1)"
            echo "    Date: $(stat -f "%Sm" -t "%Y-%m-%d %H:%M:%S" "$backup" 2>/dev/null || stat -c "%y" "$backup" 2>/dev/null | cut -d. -f1)"
            echo ""
        fi
    done

    if [[ "$found" == false ]]; then
        print_warning "No backups found"
    fi
}

restore_backup() {
    print_info "Looking for most recent backup..."

    # Find most recent backup
    local latest_backup
    latest_backup=$(ls -t "$INSTALL_DIR"/"$BINARY_NAME".backup.* 2>/dev/null | head -1 || true)

    if [[ -z "$latest_backup" ]]; then
        print_error "No backup found to restore"
        exit 1
    fi

    print_info "Found backup: $(basename "$latest_backup")"

    if [[ -x "$latest_backup" ]]; then
        local version=$("$latest_backup" --version 2>/dev/null | head -1 || echo "unknown version")
        echo "  Version: $version"
    fi

    echo ""
    read -p "Restore this backup? [y/N] " -n 1 -r
    echo ""

    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        print_info "Restore cancelled"
        exit 0
    fi

    if cp "$latest_backup" "$TARGET"; then
        chmod +x "$TARGET"
        print_success "Backup restored to $TARGET"
        rm "$latest_backup"
        print_success "Backup file removed"
    else
        print_error "Failed to restore backup"
        exit 1
    fi
}

uninstall() {
    if [[ ! -f "$TARGET" ]]; then
        print_warning "Binary not found at $TARGET"
        exit 0
    fi

    echo ""
    echo -e "${GREEN}═══════════════════════════════════════════════════${NC}"
    echo -e "${GREEN}  Construct CLI - Uninstall${NC}"
    echo -e "${GREEN}═══════════════════════════════════════════════════${NC}"
    echo ""

    print_info "Target: $TARGET"

    if [[ -x "$TARGET" ]]; then
        echo "  Version: $("$TARGET" --version 2>/dev/null | head -1 || echo 'unknown')"
    fi

    echo ""

    if [[ "$FORCE" != true ]]; then
        read -p "Remove this binary? [y/N] " -n 1 -r
        echo ""
        if [[ ! $REPLY =~ ^[Yy]$ ]]; then
            print_info "Uninstall cancelled"
            exit 0
        fi
    fi

    if rm "$TARGET"; then
        print_success "Removed: $TARGET"
    else
        print_error "Failed to remove binary"
        exit 1
    fi

    # Check for ct symlink/alias
    if [[ -L "$INSTALL_DIR/ct" ]] && [[ "$(readlink "$INSTALL_DIR/ct")" == "$TARGET" ]]; then
        print_info "Removing ct symlink..."
        rm "$INSTALL_DIR/ct"
        print_success "Removed: $INSTALL_DIR/ct"
    fi

    echo ""
    print_success "Uninstall complete"
    echo ""

    # Show backups if any exist
    local backup_count
    backup_count=$(ls "$INSTALL_DIR"/"$BINARY_NAME".backup.* 2>/dev/null | wc -l || echo 0)
    if [[ $backup_count -gt 0 ]]; then
        print_info "Found $backup_count backup(s)"
        echo "  List backups: $0 --list-backups"
        echo "  Restore: $0 --restore"
        echo ""
        echo "  Or manually restore:"
        echo "    sudo cp $INSTALL_DIR/$BINARY_NAME.backup.YYYYMMDD_HHMMSS $TARGET"
    fi
}

# Parse arguments
RESTORE=false
LIST_BACKUPS=false
FORCE=false

while [[ $# -gt 0 ]]; do
    case $1 in
        --restore)
            RESTORE=true
            shift
            ;;
        --list-backups)
            LIST_BACKUPS=true
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
            show_usage
            exit 1
            ;;
    esac
done

# Main
if [[ "$LIST_BACKUPS" == true ]]; then
    list_backups
elif [[ "$RESTORE" == true ]]; then
    check_sudo
    restore_backup
else
    check_sudo
    uninstall
fi

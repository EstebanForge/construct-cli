#!/usr/bin/env bash
#
# Construct CLI installer (curl | bash friendly)
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/EstebanForge/construct-cli/main/scripts/install.sh | bash
#
# Flags via env:
#   INSTALL_DIR=/path # override install dir (default: /usr/local/bin or ~/.local/bin)
#   SKIP_SYMLINK=1    # skip creating ct symlink

set -euo pipefail

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

info() { echo -e "${BLUE}ℹ  $*${NC}"; }
success() { echo -e "${GREEN}✓ $*${NC}"; }
warn() { echo -e "${YELLOW}⚠  $*${NC}"; }
error() { echo -e "${RED}✗ $*${NC}"; exit 1; }

REPO="EstebanForge/construct-cli"
BINARY="construct"
ALIAS="ct"
VERSION="latest"
INSTALL_DIR="${INSTALL_DIR:-}"

require_cmd() {
    command -v "$1" >/dev/null 2>&1 || error "Missing required command: $1"
}

detect_platform() {
    local os arch
    os="$(uname -s | tr '[:upper:]' '[:lower:]')"
    arch="$(uname -m)"

    case "$os" in
        darwin) os="darwin" ;;
        linux) os="linux" ;;
        *) error "Unsupported OS: $os" ;;
    esac

    case "$arch" in
        x86_64|amd64) arch="amd64" ;;
        arm64|aarch64) arch="arm64" ;;
        *) error "Unsupported architecture: $arch" ;;
    esac

    echo "${os}-${arch}"
}

latest_version() {
    local api="https://api.github.com/repos/${REPO}/releases/latest"
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL "$api" | grep '"tag_name":' | head -1 | cut -d'"' -f4
    elif command -v wget >/dev/null 2>&1; then
        wget -qO- "$api" | grep '"tag_name":' | head -1 | cut -d'"' -f4
    else
        error "curl or wget required to resolve latest version"
    fi
}

pick_install_dir() {
    if [[ -n "$INSTALL_DIR" ]]; then
        echo "$INSTALL_DIR"
        return
    fi

    local default="/usr/local/bin"
    if [[ -w "$default" ]]; then
        echo "$default"
    else
        echo "${HOME}/.local/bin"
    fi
}

ensure_path_hint() {
    local dir="$1"
    case ":$PATH:" in
        *":$dir:"*) return ;;
        *) warn "Add $dir to PATH to use ${BINARY} globally" ;;
    esac
}

install_binary() {
    local src="$1" dest_dir="$2"
    mkdir -p "$dest_dir"
    local dest="${dest_dir}/${BINARY}"

    if install -m 0755 "$src" "$dest" 2>/dev/null; then
        :
    else
        info "Escalating to sudo for install into $dest_dir"
        sudo install -m 0755 "$src" "$dest"
    fi

    success "Installed: $dest"

    if [[ "${SKIP_SYMLINK:-0}" != "1" ]]; then
        local alias_path="${dest_dir}/${ALIAS}"
        if ln -sf "$dest" "$alias_path" 2>/dev/null; then
            success "Alias created: $alias_path → $BINARY"
        else
            info "Escalating to sudo for alias in $dest_dir"
            sudo ln -sf "$dest" "$alias_path"
            success "Alias created: $alias_path → $BINARY"
        fi
    fi
}

download_binary() {
    local url="$1"
    local tmp
    tmp="$(mktemp)"

    if command -v curl >/dev/null 2>&1; then
        if ! curl -fsL "$url" -o "$tmp"; then
            rm -f "$tmp"
            error "Download failed: $url"
        fi
    else
        if ! wget -qO "$tmp" "$url"; then
            rm -f "$tmp"
            error "Download failed: $url"
        fi
    fi

    if [[ ! -s "$tmp" ]]; then
        rm -f "$tmp"
        error "Download failed: Empty file received from $url"
    fi

    echo "$tmp"
}

main() {
    require_cmd uname
    require_cmd grep

    local platform
    platform="$(detect_platform)"

    if [[ "$VERSION" == "latest" || -z "$VERSION" ]]; then
        info "Resolving latest version..."
        VERSION="$(latest_version)"
    fi

    local asset="construct-${platform}"
    local url="https://github.com/${REPO}/releases/download/${VERSION}/${asset}"
    info "Platform: ${platform}"
    info "Version: ${VERSION}"

    local tmp
    tmp="$(download_binary "$url")"
    chmod +x "$tmp"

    local dest_dir
    dest_dir="$(pick_install_dir)"
    info "Install dir: ${dest_dir}"

    install_binary "$tmp" "$dest_dir"
    rm -f "$tmp"

    ensure_path_hint "$dest_dir"

    echo
    success "Construct installed."
    echo "Verify with: ${BINARY} --version"
    echo "Then run:    ${BINARY} sys init"
}

main "$@"

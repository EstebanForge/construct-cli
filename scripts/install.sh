#!/usr/bin/env bash
#
# Construct CLI installer (curl | bash friendly)
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/EstebanForge/construct-cli/main/scripts/install.sh | bash
#
# Flags via env:
#   INSTALL_DIR=/path # override install dir (default: /usr/local/bin or ~/.local/bin)
#   SKIP_SYMLINK=1    # skip creating ct symlink
#   FORCE=1           # skip version check, always reinstall
#   CHANNEL=stable|beta # release channel when VERSION=latest (default: stable)

set -euo pipefail

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

info() { echo -e "${BLUE}ℹ  $*${NC}" >&2; }
success() { echo -e "${GREEN}✓ $*${NC}" >&2; }
warn() { echo -e "${YELLOW}⚠  $*${NC}" >&2; }
error() { echo -e "${RED}✗ $*${NC}" >&2; exit 1; }

REPO="EstebanForge/construct-cli"
BINARY="construct"
ALIAS="ct"
VERSION="latest"
CHANNEL="${CHANNEL:-stable}"
INSTALL_DIR="${INSTALL_DIR:-}"
FORCE="${FORCE:-0}"

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
    local version_file="VERSION"
    case "${CHANNEL}" in
        stable) version_file="VERSION" ;;
        beta) version_file="VERSION-BETA" ;;
        *) error "Invalid CHANNEL: ${CHANNEL} (expected stable or beta)" ;;
    esac

    local url="https://raw.githubusercontent.com/${REPO}/main/${version_file}"
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL "$url" | tr -d '[:space:]'
    elif command -v wget >/dev/null 2>&1; then
        wget -qO- "$url" | tr -d '[:space:]'
    else
        error "curl or wget required to resolve latest version"
    fi
}

get_installed_version() {
    local dest_dir="$1"
    local version_file="${HOME}/.config/construct-cli/.version"
    local binary_path="${dest_dir}/${BINARY}"

    # Check .version file first (faster, more reliable)
    if [[ -f "$version_file" ]]; then
        cat "$version_file" | tr -d '[:space:]'
        return
    fi

    # Fallback: query binary if .version file missing
    if [[ -x "$binary_path" ]]; then
        "$binary_path" version 2>/dev/null | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -1 || echo ""
    else
        echo ""
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
    require_cmd tar

    local platform
    platform="$(detect_platform)"

    local dest_dir
    dest_dir="$(pick_install_dir)"

    if [[ "$VERSION" == "latest" || -z "$VERSION" ]]; then
        info "Resolving latest version (channel: ${CHANNEL})..."
        VERSION="$(latest_version)"
    fi

    # Check if same version is already installed
    if [[ "$FORCE" != "1" ]]; then
        local installed_version
        installed_version="$(get_installed_version "$dest_dir")"

        if [[ -n "$installed_version" && "$installed_version" == "$VERSION" ]]; then
            success "Already on latest version: ${VERSION}"
            echo -n "Do you want to reinstall? [y/N]: " >&2
            response=""
            if ! read -r response; then
                response=""
            fi
            response=$(echo "$response" | tr '[:upper:]' '[:lower:]')
            if [[ "$response" != "y" && "$response" != "yes" ]]; then
                info "Installation cancelled."
                exit 0
            fi
        elif [[ -n "$installed_version" ]]; then
            info "Upgrading: ${installed_version} → ${VERSION}"
        fi
    fi

    local asset="construct-${platform}-${VERSION}.tar.gz"
    local url="https://github.com/${REPO}/releases/download/${VERSION}/${asset}"
    info "Platform: ${platform}"
    info "Version: ${VERSION}"

    info "Downloading ${asset}..."
    local tmp_tar
    tmp_tar="$(download_binary "$url")"

    local tmp_dir
    tmp_dir="$(mktemp -d)"
    tar -xzf "$tmp_tar" -C "$tmp_dir"

    local binary_name="construct-${platform}"
    local tmp_bin="${tmp_dir}/${binary_name}"

    if [[ ! -f "$tmp_bin" ]]; then
        # Fallback: maybe it's just named 'construct' inside?
        if [[ -f "${tmp_dir}/construct" ]]; then
            tmp_bin="${tmp_dir}/construct"
        else
            error "Binary ${binary_name} not found in archive"
        fi
    fi

    chmod +x "$tmp_bin"

    info "Install dir: ${dest_dir}"

    install_binary "$tmp_bin" "$dest_dir"

    rm -rf "$tmp_dir" "$tmp_tar"

    ensure_path_hint "$dest_dir"

    echo
    success "The Construct installed."
    echo "Verify with: ${BINARY} --version"
    echo "Then run:    ${BINARY} sys init"
}

main "$@"

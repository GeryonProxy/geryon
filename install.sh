#!/bin/bash
# Geryon Installation Script
# Usage: curl -sSL https://raw.githubusercontent.com/GeryonProxy/geryon/main/install.sh | bash

set -e

REPO="GeryonProxy/geryon"
INSTALL_DIR="/usr/local/bin"
BINARY_NAME="geryon"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Print functions
print_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

print_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Detect OS and architecture
detect_platform() {
    OS=$(uname -s | tr '[:upper:]' '[:lower:]')
    ARCH=$(uname -m)

    case $OS in
        linux|darwin|freebsd)
            ;;
        *)
            print_error "Unsupported operating system: $OS"
            exit 1
            ;;
    esac

    case $ARCH in
        x86_64|amd64)
            ARCH="amd64"
            ;;
        arm64|aarch64)
            ARCH="arm64"
            ;;
        *)
            print_error "Unsupported architecture: $ARCH"
            exit 1
            ;;
    esac

    PLATFORM="${OS}-${ARCH}"
}

# Get latest release version
get_latest_version() {
    if command -v curl &> /dev/null; then
        VERSION=$(curl -s "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')
    elif command -v wget &> /dev/null; then
        VERSION=$(wget -qO- "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')
    else
        print_error "curl or wget is required"
        exit 1
    fi

    if [ -z "$VERSION" ]; then
        print_error "Could not determine latest version"
        exit 1
    fi

    echo "$VERSION"
}

# Download binary
download_binary() {
    local version=$1
    local platform=$2
    local temp_dir=$3

    local url="https://github.com/${REPO}/releases/download/${version}/geryon-${platform}"
    local output="${temp_dir}/geryon"

    print_info "Downloading Geryon ${version} for ${platform}..."

    if command -v curl &> /dev/null; then
        curl -fsSL -o "$output" "$url" || {
            print_error "Failed to download binary"
            exit 1
        }
    elif command -v wget &> /dev/null; then
        wget -qO "$output" "$url" || {
            print_error "Failed to download binary"
            exit 1
        }
    fi

    chmod +x "$output"
}

# Verify checksum (if sha256sum is available)
verify_checksum() {
    local version=$1
    local platform=$2
    local temp_dir=$3

    if ! command -v sha256sum &> /dev/null && ! command -v shasum &> /dev/null; then
        print_warn "Checksum verification skipped (sha256sum not available)"
        return 0
    fi

    local checksum_url="https://github.com/${REPO}/releases/download/${version}/geryon-${platform}.sha256"
    local checksum_file="${temp_dir}/geryon.sha256"

    print_info "Verifying checksum..."

    if command -v curl &> /dev/null; then
        curl -fsSL -o "$checksum_file" "$checksum_url" 2>/dev/null || return 0
    elif command -v wget &> /dev/null; then
        wget -qO "$checksum_file" "$checksum_url" 2>/dev/null || return 0
    fi

    if [ -f "$checksum_file" ]; then
        cd "$temp_dir"
        if command -v sha256sum &> /dev/null; then
            sha256sum -c "$checksum_file" || {
                print_error "Checksum verification failed"
                exit 1
            }
        else
            shasum -a 256 -c "$checksum_file" || {
                print_error "Checksum verification failed"
                exit 1
            }
        fi
        print_info "Checksum verified"
    fi
}

# Install binary
install_binary() {
    local temp_dir=$1
    local install_dir=$2

    if [ -w "$install_dir" ]; then
        mv "${temp_dir}/geryon" "${install_dir}/${BINARY_NAME}"
    else
        print_info "Requesting sudo access to install to ${install_dir}..."
        sudo mv "${temp_dir}/geryon" "${install_dir}/${BINARY_NAME}"
    fi

    print_info "Geryon installed to ${install_dir}/${BINARY_NAME}"
}

# Create config directory
setup_config() {
    local config_dir="$HOME/.config/geryon"

    if [ ! -d "$config_dir" ]; then
        mkdir -p "$config_dir"
        print_info "Created config directory: ${config_dir}"
    fi

    # Generate example config if it doesn't exist
    if [ ! -f "${config_dir}/geryon.yaml" ]; then
        ${INSTALL_DIR}/${BINARY_NAME} --generate-config > "${config_dir}/geryon.yaml.example" 2>/dev/null || true
        print_info "Generated example config: ${config_dir}/geryon.yaml.example"
    fi
}

# Main installation
main() {
    print_info "Geryon Installer"
    print_info "================"

    # Check for version argument
    VERSION="${1:-latest}"
    if [ "$VERSION" = "latest" ]; then
        VERSION=$(get_latest_version)
    fi

    # Remove 'v' prefix if present
    VERSION="${VERSION#v}"
    VERSION="v${VERSION}"

    detect_platform

    print_info "Platform: ${PLATFORM}"
    print_info "Version: ${VERSION}"

    # Create temp directory
    TEMP_DIR=$(mktemp -d)
    trap 'rm -rf "$TEMP_DIR"' EXIT

    # Download and install
    download_binary "$VERSION" "$PLATFORM" "$TEMP_DIR"
    verify_checksum "$VERSION" "$PLATFORM" "$TEMP_DIR"
    install_binary "$TEMP_DIR" "$INSTALL_DIR"
    setup_config

    # Verify installation
    if command -v geryon &> /dev/null; then
        print_info "Geryon installed successfully!"
        echo ""
        geryon --version 2>/dev/null || true
        echo ""
        print_info "Quick start:"
        echo "  1. Generate a config: geryon --generate-config > ~/.config/geryon/geryon.yaml"
        echo "  2. Edit the config with your database settings"
        echo "  3. Start Geryon: geryon --config ~/.config/geryon/geryon.yaml"
        echo ""
        print_info "Documentation: https://github.com/${REPO}"
    else
        print_error "Installation failed. Please check your PATH."
        exit 1
    fi
}

# Run main function
main "$@"

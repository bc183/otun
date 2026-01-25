#!/bin/sh
set -e

# otun installer script
# Usage: curl -fsSL https://raw.githubusercontent.com/bc183/otun/main/install.sh | sh

REPO="bc183/otun"
INSTALL_DIR="/usr/local/bin"
BINARY_NAME="otun"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Print colored output
info() { printf "${BLUE}==>${NC} %s\n" "$1"; }
success() { printf "${GREEN}==>${NC} %s\n" "$1"; }
warn() { printf "${YELLOW}==>${NC} %s\n" "$1"; }
error() { printf "${RED}==>${NC} %s\n" "$1"; }

# Ask yes/no question with default
# Usage: ask_yes_no "Question?" "y"  (default yes)
#        ask_yes_no "Question?" "n"  (default no)
ask_yes_no() {
    _question="$1"
    _default="$2"

    if [ "$_default" = "y" ]; then
        printf "%s [Y/n]: " "$_question"
    else
        printf "%s [y/N]: " "$_question"
    fi

    # Read from /dev/tty to handle curl | sh
    if read -r _answer < /dev/tty 2>/dev/null; then
        :
    else
        read -r _answer
    fi

    # Use default if empty
    if [ -z "$_answer" ]; then
        _answer="$_default"
    fi

    case "$_answer" in
        [Yy]|[Yy][Ee][Ss]) return 0 ;;
        *) return 1 ;;
    esac
}


echo ""
echo "  ╔═══════════════════════════════════╗"
echo "  ║         otun installer            ║"
echo "  ╚═══════════════════════════════════╝"
echo ""

# Detect OS
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$OS" in
    darwin) OS="darwin" ;;
    linux) OS="linux" ;;
    mingw*|msys*|cygwin*) OS="windows" ;;
    *) error "Unsupported OS: $OS"; exit 1 ;;
esac

# Detect architecture
ARCH=$(uname -m)
case "$ARCH" in
    x86_64|amd64) ARCH="amd64" ;;
    arm64|aarch64) ARCH="arm64" ;;
    *) error "Unsupported architecture: $ARCH"; exit 1 ;;
esac

info "Detected platform: $OS/$ARCH"

# Check for existing installation
EXISTING_PATH=$(command -v "$BINARY_NAME" 2>/dev/null || true)
if [ -n "$EXISTING_PATH" ]; then
    EXISTING_VERSION=$("$EXISTING_PATH" version 2>/dev/null | head -1 || echo "unknown")
    warn "Existing installation found: $EXISTING_PATH"
    warn "Current version: $EXISTING_VERSION"
    echo ""
    if ! ask_yes_no "Do you want to replace it?" "y"; then
        echo ""
        info "Installation cancelled."
        exit 0
    fi
    echo ""
fi

# Get available versions
info "Fetching available versions..."
LATEST_VERSION=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" 2>/dev/null | grep '"tag_name"' | sed -E 's/.*"([^"]+)".*/\1/')
if [ -z "$LATEST_VERSION" ]; then
    error "Failed to get latest version from GitHub"
    exit 1
fi

# Ask which version to install
echo ""
info "Latest version: $LATEST_VERSION"
printf "Version to install (press Enter for latest): "
if read -r VERSION < /dev/tty 2>/dev/null; then
    :
else
    read -r VERSION
fi

# Use latest if empty
if [ -z "$VERSION" ]; then
    VERSION="$LATEST_VERSION"
fi

# Ensure version starts with 'v' if it's a number
case "$VERSION" in
    [0-9]*) VERSION="v$VERSION" ;;
esac

echo ""
info "Installing otun $VERSION ($OS/$ARCH)..."

# Build download URL
if [ "$OS" = "windows" ]; then
    FILENAME="otun_${OS}_${ARCH}.zip"
else
    FILENAME="otun_${OS}_${ARCH}.tar.gz"
fi
URL="https://github.com/$REPO/releases/download/$VERSION/$FILENAME"

# Create temp directory
TMP_DIR=$(mktemp -d)
trap "rm -rf $TMP_DIR" EXIT

# Download
info "Downloading $URL..."
if ! curl -fsSL "$URL" -o "$TMP_DIR/$FILENAME" 2>/dev/null; then
    error "Failed to download $URL"
    error "Check that version '$VERSION' exists"
    exit 1
fi

# Extract
cd "$TMP_DIR"
if [ "$OS" = "windows" ]; then
    unzip -q "$FILENAME"
else
    tar xzf "$FILENAME"
fi

# Install
if [ -w "$INSTALL_DIR" ]; then
    mv "$BINARY_NAME" "$INSTALL_DIR/"
else
    info "Installing to $INSTALL_DIR (requires sudo)..."
    sudo mv "$BINARY_NAME" "$INSTALL_DIR/"
fi

echo ""
success "otun $VERSION installed to $INSTALL_DIR/$BINARY_NAME"
echo ""
echo "Get started:"
echo "  otun http 3000"
echo ""

#!/bin/sh
set -e

# otun installer script
# Usage: curl -fsSL https://raw.githubusercontent.com/bc183/otun/main/install.sh | sh

REPO="bc183/otun"
INSTALL_DIR="/usr/local/bin"
BINARY_NAME="otun"

# Detect OS
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$OS" in
    darwin) OS="darwin" ;;
    linux) OS="linux" ;;
    mingw*|msys*|cygwin*) OS="windows" ;;
    *) echo "Unsupported OS: $OS"; exit 1 ;;
esac

# Detect architecture
ARCH=$(uname -m)
case "$ARCH" in
    x86_64|amd64) ARCH="amd64" ;;
    arm64|aarch64) ARCH="arm64" ;;
    *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

# Get latest version
VERSION=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" | grep '"tag_name"' | sed -E 's/.*"([^"]+)".*/\1/')
if [ -z "$VERSION" ]; then
    echo "Failed to get latest version"
    exit 1
fi

echo "Installing otun $VERSION ($OS/$ARCH)..."

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

# Download and extract
echo "Downloading $URL..."
curl -fsSL "$URL" -o "$TMP_DIR/$FILENAME"

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
    echo "Installing to $INSTALL_DIR (requires sudo)..."
    sudo mv "$BINARY_NAME" "$INSTALL_DIR/"
fi

echo "✓ otun installed to $INSTALL_DIR/$BINARY_NAME"
echo ""
echo "Get started:"
echo "  otun http 3000"

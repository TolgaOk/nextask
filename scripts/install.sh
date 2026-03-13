#!/bin/sh
set -eu

REPO="TolgaOk/nextask"
BINARY="nextask"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"

# Detect OS
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$OS" in
  linux)  OS="linux" ;;
  darwin) OS="darwin" ;;
  *) echo "Unsupported OS: $OS" >&2; exit 1 ;;
esac

# Detect architecture
ARCH=$(uname -m)
case "$ARCH" in
  x86_64|amd64)  ARCH="amd64" ;;
  aarch64|arm64)  ARCH="arm64" ;;
  *) echo "Unsupported architecture: $ARCH" >&2; exit 1 ;;
esac

# Get latest version
VERSION=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | cut -d'"' -f4)
if [ -z "$VERSION" ]; then
  echo "Failed to fetch latest version" >&2
  exit 1
fi

# Download
URL="https://github.com/${REPO}/releases/download/${VERSION}/${BINARY}_${OS}_${ARCH}.tar.gz"
TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT

echo "Downloading ${BINARY} ${VERSION} (${OS}/${ARCH})..."
curl -fsSL "$URL" -o "$TMPDIR/${BINARY}.tar.gz"
tar -xzf "$TMPDIR/${BINARY}.tar.gz" -C "$TMPDIR"

# Install
mkdir -p "$INSTALL_DIR"
mv "$TMPDIR/${BINARY}" "$INSTALL_DIR/${BINARY}"
chmod +x "$INSTALL_DIR/${BINARY}"

echo "Installed ${BINARY} to ${INSTALL_DIR}/${BINARY}"

# Check PATH
case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *) echo "Add to your PATH: export PATH=\"${INSTALL_DIR}:\$PATH\"" ;;
esac

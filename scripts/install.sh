#!/bin/sh
# GitGate Installer Script
# Usage: curl -fsSL https://raw.githubusercontent.com/jvrsantacruz/gitgate/main/scripts/install.sh | sh
set -e

REPO="jvrsantacruz/gitgate"
INSTALL_DIR="${GITGATE_INSTALL_DIR:-$HOME/.gitgate/bin}"
LINK_DIR="${GITGATE_LINK_DIR:-$HOME/.local/bin}"
BINARY="gitgate"

# Detect OS and architecture
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"

case "$ARCH" in
    x86_64|amd64) ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    *)
        echo "❌ Unsupported architecture: $ARCH"
        exit 1
        ;;
esac

case "$OS" in
    linux|darwin) ;;
    mingw*|msys*|cygwin*) OS="windows" ;;
    *)
        echo "❌ Unsupported OS: $OS"
        exit 1
        ;;
esac

EXT=""
if [ "$OS" = "windows" ]; then EXT=".exe"; fi

# Get latest version from GitHub
echo "🔍 Fetching latest release..."
LATEST=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" | \
    grep '"tag_name"' | sed -E 's/.*"([^"]+)".*/\1/')

if [ -z "$LATEST" ]; then
    echo "❌ Could not determine latest version"
    exit 1
fi

echo "📦 Installing gitgate $LATEST for $OS/$ARCH..."

BINARY_NAME="${BINARY}-${OS}-${ARCH}${EXT}"
DOWNLOAD_URL="https://github.com/$REPO/releases/download/$LATEST/$BINARY_NAME"
CHECKSUM_URL="https://github.com/$REPO/releases/download/$LATEST/checksums.txt"

# Create install directory
mkdir -p "$INSTALL_DIR"

# Download binary
TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT

echo "⬇️  Downloading $BINARY_NAME..."
curl -fsSL -o "$TMPDIR/$BINARY_NAME" "$DOWNLOAD_URL"

# Verify checksum
echo "🔒 Verifying SHA-256 checksum..."
curl -fsSL -o "$TMPDIR/checksums.txt" "$CHECKSUM_URL"

if command -v sha256sum > /dev/null 2>&1; then
    (cd "$TMPDIR" && grep "$BINARY_NAME" checksums.txt | sha256sum -c -)
elif command -v shasum > /dev/null 2>&1; then
    (cd "$TMPDIR" && grep "$BINARY_NAME" checksums.txt | shasum -a 256 -c -)
else
    echo "⚠️  Cannot verify checksum: sha256sum/shasum not found"
fi

# Install
chmod +x "$TMPDIR/$BINARY_NAME"
mv "$TMPDIR/$BINARY_NAME" "$INSTALL_DIR/${BINARY}${EXT}"

echo "✅ Installed to $INSTALL_DIR/${BINARY}${EXT}"

# Create symlink in PATH
if [ -n "$LINK_DIR" ]; then
    mkdir -p "$LINK_DIR"
    ln -sf "$INSTALL_DIR/${BINARY}${EXT}" "$LINK_DIR/${BINARY}"
    echo "🔗 Symlinked to $LINK_DIR/$BINARY"
fi

# Verify installation
if "$INSTALL_DIR/${BINARY}${EXT}" --version > /dev/null 2>&1; then
    echo ""
    echo "🎉 GitGate installed successfully!"
    echo "   Run 'gitgate doctor' to verify your setup."
    echo ""
    echo "   If '$BINARY' is not found, add this to your shell profile:"
    echo "   export PATH=\"\$HOME/.local/bin:\$PATH\""
else
    echo "⚠️  Installation succeeded but binary test failed"
fi

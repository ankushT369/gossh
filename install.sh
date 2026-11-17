
#!/usr/bin/env bash

set -e

REPO="ankushT369/gossh"
BINARY="gossh"
INSTALL_DIR="$HOME/.local/bin"

OS="$(uname -s)"
ARCH="$(uname -m)"

case "$OS:$ARCH" in
    Linux:x86_64)
        ASSET="gossh-linux-amd64"
        ;;
    Darwin:arm64)
        ASSET="gossh-darwin-arm64"
        ;;
    *)
        echo "Error: unsupported platform: $OS $ARCH"
        exit 1
        ;;
esac

URL="https://github.com/$REPO/releases/latest/download/$ASSET"

mkdir -p "$INSTALL_DIR"

TMP_FILE="$(mktemp)"
trap 'rm -f "$TMP_FILE"' EXIT

echo "[-] Installing gossh"

# Download using curl or wget
if command -v curl >/dev/null 2>&1; then
    curl -fL "$URL" -o "$TMP_FILE"
elif command -v wget >/dev/null 2>&1; then
    wget -O "$TMP_FILE" "$URL"
else
    echo "Error: curl or wget is required."
    exit 1
fi

chmod +x "$TMP_FILE"
mv "$TMP_FILE" "$INSTALL_DIR/$BINARY"

echo "[✓] Installed gossh"

if [[ ":$PATH:" != *":$INSTALL_DIR:"* ]]; then
    echo "Add this to your shell configuration:"
    echo 'export PATH="$HOME/.local/bin:$PATH"'
fi

echo "[✓] Run: gossh version"


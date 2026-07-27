#!/usr/bin/env sh
# One-line installer for nodered-mcp (Linux/macOS).
#
#   curl -fsSL https://raw.githubusercontent.com/fgjcarlos/nodered-mcp/main/scripts/install.sh | sh
#
# Downloads the right prebuilt binary from GitHub Releases, drops it in
# ~/.local/bin, then points you at `nodered-mcp init --write`.
#
# NOTE: requires the repo to be published with a Release (see issues/301).
# Until then there is nothing to download and this script will 404.
set -eu

REPO="fgjcarlos/nodered-mcp"
DEST="${HOME}/.local/bin"

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case "$ARCH" in
	x86_64 | amd64) ARCH=amd64 ;;
	aarch64 | arm64) ARCH=arm64 ;;
	*) echo "unsupported architecture: $ARCH" >&2; exit 1 ;;
esac

ASSET="nodered-mcp_${OS}_${ARCH}.tar.gz"
URL="https://github.com/${REPO}/releases/latest/download/${ASSET}"

mkdir -p "$DEST"
echo "downloading ${URL}"
curl -fsSL "$URL" | tar -xz -C "$DEST" nodered-mcp
chmod +x "${DEST}/nodered-mcp"

echo "installed: ${DEST}/nodered-mcp"
case ":${PATH}:" in
	*":${DEST}:"*) ;;
	*) echo "note: add ${DEST} to your PATH" ;;
esac
echo "next: nodered-mcp init --write"

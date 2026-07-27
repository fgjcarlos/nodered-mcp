#!/usr/bin/env bash
# Build a Claude Desktop .mcpb bundle for one platform.
#
# Usage:
#   scripts/build-mcpb.sh                       # host platform
#   GOOS=windows GOARCH=amd64 scripts/build-mcpb.sh
#   GOOS=darwin  GOARCH=arm64 scripts/build-mcpb.sh
#
# Requires: go, npx (for @anthropic-ai/mcpb). Output: nodered-mcp-<os>-<arch>.mcpb
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

VERSION="${VERSION:-dev}"
GOOS="${GOOS:-$(go env GOOS)}"
GOARCH="${GOARCH:-$(go env GOARCH)}"

BIN="nodered-mcp"
[ "$GOOS" = "windows" ] && BIN="nodered-mcp.exe"

rm -rf mcpb/server
mkdir -p mcpb/server

echo "building $GOOS/$GOARCH -> mcpb/server/$BIN"
CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" \
	go build -ldflags "-s -w -X main.version=${VERSION}" -o "mcpb/server/$BIN" ./cmd/nodered-mcp

OUT="nodered-mcp-${GOOS}-${GOARCH}.mcpb"
echo "packing -> $OUT"
npx --yes @anthropic-ai/mcpb pack mcpb "$OUT"

echo "done: $OUT"

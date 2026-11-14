#!/usr/bin/env bash
# Build the fusion-console binary. Usage: ./scripts/build.sh [target-os-target-arch]
# With no argument, builds for the host. Example: ./scripts/build.sh darwin-arm64
set -euo pipefail
cd "$(dirname "$0")/.."

if [ ! -f internal/server/webdist/index.html ]; then
    echo "webdist missing; run scripts/build-web.sh first" >&2
    exit 1
fi

TARGET="${1:-}"
OUT=bin
mkdir -p "$OUT"
if [ -z "$TARGET" ]; then
    go build -trimpath -o "$OUT/fusion-console" ./cmd/console
else
    OS="${TARGET%-*}"
    ARCH="${TARGET#*-}"
    EXT=""
    [ "$OS" = "windows" ] && EXT=".exe"
    GOOS="$OS" GOARCH="$ARCH" go build -trimpath -o "$OUT/fusion-console-$TARGET$EXT" ./cmd/console
fi
echo "built: $OUT"

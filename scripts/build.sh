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
revision="$(git rev-parse --verify HEAD 2>/dev/null || true)"
# Only CI for this exact checkout, or an unmodified local tree, may report
# an upstream revision as eligible for unattended replacement.
if [[ -n "$revision" && ( "${GITHUB_ACTIONS:-}" != true || "${GITHUB_SHA:-}" != "$revision" ) ]]; then
    if [[ -n "$(git status --porcelain)" ]]; then revision=""; fi
fi
version_flags="-X github.com/dahai80/fusion-console/internal/server.upstreamRevision=$revision"
if [ -z "$TARGET" ]; then
    go build -trimpath -ldflags "$version_flags" -o "$OUT/fusion-console" ./cmd/console
else
    OS="${TARGET%-*}"
    ARCH="${TARGET#*-}"
    EXT=""
    [ "$OS" = "windows" ] && EXT=".exe"
    GOOS="$OS" GOARCH="$ARCH" go build -trimpath -ldflags "$version_flags" -o "$OUT/fusion-console-$TARGET$EXT" ./cmd/console
fi
echo "built: $OUT"

#!/usr/bin/env bash
# Package fusion-console releases. Each package bundles the matching
# fusion-gateway binary so a single download is enough.
# Usage: ./scripts/package.sh <version>          (all platforms)
#        ./scripts/package.sh <version> darwin-arm64 [more targets...]
set -euo pipefail
cd "$(dirname "$0")/.."

VERSION="${1:?usage: package.sh <version> [targets...]}"
shift || true
TARGETS=("$@")
if [ ${#TARGETS[@]} -eq 0 ]; then
    TARGETS=(darwin-arm64 darwin-amd64 linux-amd64 linux-arm64 windows-amd64)
fi

if [ ! -f internal/server/webdist/index.html ]; then
    echo "webdist missing; run scripts/build-web.sh first" >&2
    exit 1
fi

DIST=dist
GATEWAY_SRC="${FUSION_GATEWAY_SRC:-$(cd .. && pwd)/fusion-gateway}"
rm -rf "$DIST"
mkdir -p "$DIST/cache"

# fetch_gateway <os> <arch> <outfile>
# Bundling at packaging time only — the two code repos stay separate.
# Source of truth is the local ../fusion-gateway checkout (release assets
# on GitHub only cover darwin-arm64). Falls back to a release asset when
# the source tree is absent.
fetch_gateway() {
    local os="$1" arch="$2" out="$3" asset
    out="$(cd "$(dirname "$out")" && pwd)/$(basename "$out")"
    if [ "$os" = "windows" ]; then
        asset="fusion-gateway-${os}-${arch}.zip"
    else
        asset="fusion-gateway-${os}-${arch}.tar.gz"
    fi
    if [ -d "$GATEWAY_SRC" ]; then
        echo "gateway: cross-build $os/$arch from $GATEWAY_SRC" >&2
        (cd "$GATEWAY_SRC" && CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
            go build -trimpath -o "$out" ./cmd/gateway)
        chmod 0755 "$out" 2>/dev/null || true
        return 0
    fi
    if [ -f "$DIST/cache/$asset" ]; then
        echo "gateway: using cached ${asset}" >&2
    elif [ -n "${GATEWAY_ALLOW_DOWNLOAD:-}" ]; then
        # Release assets only cover darwin-arm64 today; kept as fallback.
        curl -fsSL --max-time 600 \
            "https://github.com/dahai80/fusion-gateway/releases/latest/download/${asset}" \
            -o "$DIST/cache/$asset" ||
            curl -fsSL --max-time 600 "https://gh-proxy.com/https://github.com/dahai80/fusion-gateway/releases/latest/download/${asset}" \
                -o "$DIST/cache/$asset"
    else
        echo "no gateway source at $GATEWAY_SRC and no cached asset; packaging console alone for $os/$arch" >&2
        return 1
    fi
    local tmp found
    tmp=$(mktemp -d)
    cp "$DIST/cache/$asset" "$tmp/$asset"
    if [ "$os" = "windows" ]; then
        unzip -p "$tmp/$asset" "*/fusion-gateway.exe" > "$out" 2>/dev/null ||
            unzip -p "$tmp/$asset" "fusion-gateway.exe" > "$out"
    else
        tar -xzf "$tmp/$asset" -C "$tmp" 2>/dev/null || true
        found=$(find "$tmp" -name fusion-gateway -type f | head -1)
        [ -n "$found" ] || { echo "gateway binary not found in $asset" >&2; exit 1; }
        cp "$found" "$out"
        chmod 0755 "$out"
    fi
    rm -rf "$tmp"
}

stage_dir() { # stage_dir <name> <console-bin> <gateway-bin|"-">
    local d="$DIST/$1"
    mkdir -p "$d"
    cp "$2" "$d/fusion-console"
    if [ "$3" != "-" ]; then
        cp "$3" "$d/fusion-gateway"
    fi
    chmod 0755 "$d"/fusion-* 2>/dev/null || true
    cp README.md LICENSE "$d/"
}

for t in "${TARGETS[@]}"; do
    os="${t%-*}"; arch="${t#*-}"
    ./scripts/build.sh "$t"
    gwtmp=$(mktemp -d)
    if [ "$os" = "windows" ]; then
        GW="$gwtmp/fusion-gateway.exe"
        fetch_gateway "$os" "$arch" "$GW" || GW="-"
    else
        GW="$gwtmp/fusion-gateway"
        fetch_gateway "$os" "$arch" "$GW" || GW="-"
    fi

    if [ "$os" = "windows" ]; then
        d="$DIST/fusion-console-$VERSION-$t"
        mkdir -p "$d"
        cp "bin/fusion-console-$t" "$d/fusion-console.exe"
        [ "$GW" != "-" ] && cp "$GW" "$d/fusion-gateway.exe"
        cp README.md LICENSE "$d/"
        (cd "$DIST" && zip -qr "fusion-console-$VERSION-$t.zip" "$(basename "$d")")
        rm -rf "$d"
    else
        stage_dir "fusion-console-$VERSION-$t" "bin/fusion-console-$t" "$GW"
        (cd "$DIST" && tar -czf "fusion-console-$VERSION-$t.tar.gz" "fusion-console-$VERSION-$t")
        rm -rf "$DIST/fusion-console-$VERSION-$t"
    fi
    rm -rf "$gwtmp"
    echo "packaged: $t"
done

# macOS universal DMG (console + gateway universal binaries side by side).
if [ -f bin/fusion-console-darwin-arm64 ] && [ -f bin/fusion-console-darwin-amd64 ]; then
    ./scripts/build.sh darwin-arm64 >/dev/null
    ./scripts/build.sh darwin-amd64 >/dev/null
    fetch_gateway darwin arm64 "$DIST/gw-arm64"
    fetch_gateway darwin amd64 "$DIST/gw-amd64"
    lipo -create -output "$DIST/fusion-console-universal" bin/fusion-console-darwin-arm64 bin/fusion-console-darwin-amd64
    lipo -create -output "$DIST/fusion-gateway-universal" "$DIST/gw-arm64" "$DIST/gw-amd64"
    stage_dir "fusion-console-$VERSION-darwin-universal" "$DIST/fusion-console-universal" "$DIST/fusion-gateway-universal"
    hdiutil create -volname "fusion-console" -srcfolder "$DIST/fusion-console-$VERSION-darwin-universal" \
        -ov -format UDZO "$DIST/fusion-console-$VERSION-darwin-universal.dmg" >/dev/null
    rm -rf "$DIST/fusion-console-$VERSION-darwin-universal" "$DIST/fusion-console-universal" \
        "$DIST/fusion-gateway-universal" "$DIST/gw-arm64" "$DIST/gw-amd64"
    echo "packaged: darwin-universal.dmg"
fi

echo "done:"; ls -la "$DIST"

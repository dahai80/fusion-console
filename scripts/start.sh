#!/usr/bin/env bash
# Start fusion-console (and the bundled fusion-gateway when present), then
# open the default browser with one-time auto-login. macOS + Linux.
# Usage: ./start.sh [path-to-gateway-config.yaml]
set -euo pipefail
cd "$(dirname "$0")"

HERE="$(pwd)"
CONSOLE_BIN="$HERE/fusion-console"
GATEWAY_BIN="$HERE/fusion-gateway"
[ -x "$CONSOLE_BIN" ] || CONSOLE_BIN="$HOME/.fusion-console/bin/fusion-console"
[ -x "$GATEWAY_BIN" ] || GATEWAY_BIN="$HOME/.fusion-console/bin/fusion-gateway"

port_open() { # port_open <port> -> 0 if something listens
    if command -v lsof >/dev/null 2>&1; then
        lsof -iTCP:"$1" -sTCP:LISTEN >/dev/null 2>&1
    else
        (exec 3<>"/dev/tcp/127.0.0.1/$1") 2>/dev/null
    fi
}

mkdir -p "$HOME/.fusion-console/bin" "$HOME/.fusion-console/log"

# 1. Gateway: start only when a binary and a config exist and :11432 is free.
GW_CFG="${1:-${FUSION_GATEWAY_CONFIG:-}}"
[ -z "$GW_CFG" ] && [ -f "$HERE/config.yaml" ] && GW_CFG="$HERE/config.yaml"
[ -z "$GW_CFG" ] && [ -f "$HOME/.fusion-gateway/config.yaml" ] && GW_CFG="$HOME/.fusion-gateway/config.yaml"
if [ -x "$GATEWAY_BIN" ] && [ -n "$GW_CFG" ] && [ -f "$GW_CFG" ]; then
    if port_open 11432; then
        echo "gateway: already running on :11432"
    else
        nohup "$GATEWAY_BIN" -config "$GW_CFG" > "$HOME/.fusion-console/log/gateway.log" 2>&1 &
        echo "gateway: started (pid $!, config $GW_CFG, log ~/.fusion-console/log/gateway.log)"
    fi
else
    echo "gateway: skipped (binary or config.yaml not found — start it manually or use the Gateway menu)"
fi

# 2. Console: start if :11480 is free; --open launches the default browser
#    with a one-time auto-login token.
if port_open 11480; then
    echo "console: already running on :11480 — open http://127.0.0.1:11480"
else
    nohup "$CONSOLE_BIN" --open > "$HOME/.fusion-console/log/console.log" 2>&1 &
    echo "console: started (pid $!, log ~/.fusion-console/log/console.log)"
    echo "console: browser opening at http://127.0.0.1:11480 (auto-login)"
fi

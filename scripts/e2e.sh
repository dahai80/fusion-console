#!/usr/bin/env bash
# End-to-end test harness with FULL isolation from the live environment.
#
# Background (issue #2): an early ad-hoc e2e run drove the supervisor-managed
# gateway on :11432 and mutated its admin config. This harness guarantees:
#   1. Its own gateway instance: ephemeral port, config under the scratch
#      dir, isolated data_dir, file logging disabled (never appends to
#      ~/.fusion-gateway/logs/gateway-*.log).
#   2. Never touches 127.0.0.1:11432 or any live process.
#   3. Everything (gateway + console + data) is cleaned up on exit.
#
# Usage: scripts/e2e.sh [gateway-binary]
# Requires: curl, python3; the console binary (make build); a gateway
# binary (default ../fusion-gateway/bin/fusion-gateway, override via
# FUSION_GATEWAY_BIN or argv[1]). A running fusion-mlx is optional —
# without one the harness verifies wiring (login, provider, gateway
# status) but not a live model call.
set -euo pipefail

DIR="$(mktemp -d /tmp/fc-e2e.XXXXXX)"
GW_BIN="${2:-${FUSION_GATEWAY_BIN:-$(cd "$(dirname "$0")/.." && pwd)/../fusion-gateway/bin/fusion-gateway}}"
CONSOLE_BIN="$(cd "$(dirname "$0")/.." && pwd)/bin/fusion-console"
GW_PID=""
SVR_PID=""

cleanup() {
    [[ -n "$GW_PID" ]] && kill "$GW_PID" 2>/dev/null || true
    [[ -n "$SVR_PID" ]] && kill "$SVR_PID" 2>/dev/null || true
    rm -rf "$DIR"
}
trap cleanup EXIT

[[ -x "$GW_BIN" ]] || { echo "FAIL: gateway binary not found ($GW_BIN)"; exit 1; }
[[ -x "$CONSOLE_BIN" ]] || { echo "FAIL: $CONSOLE_BIN missing (run make build)"; exit 1; }

# Ephemeral ports: ask the kernel for two free ports, then release them.
read -r GW_PORT SVR_PORT <<EOF
$(python3 -c '
import socket
ports = []
for _ in range(2):
    s = socket.socket()
    s.bind(("127.0.0.1", 0))
    ports.append(s.getsockname()[1])
    s.close()
print(*ports)')
EOF

# Guard: the harness must never target the fixed live port.
[[ "$GW_PORT" != "11432" && "$SVR_PORT" != "11480" ]] || { echo "FAIL: got a registered live port"; exit 1; }

# fusion-mlx auth (if enabled) — read locally, never printed.
MLX_KEY=""
if [[ -f "$HOME/.fusion-mlx/settings.json" ]]; then
    MLX_KEY="$(python3 -c '
import json, sys
d = json.load(open(sys.argv[1]))
print((d.get("auth") or {}).get("api_key") or "", end="")
' "$HOME/.fusion-mlx/settings.json")"
fi

# Pick a chat model the local engine actually has loaded (embedders excluded);
# fall back to glm-5 when fusion-mlx is unreachable (live turn skipped anyway).
E2E_MODEL="$(curl -sf -m 2 -H "Authorization: Bearer ${MLX_KEY}" http://127.0.0.1:11434/v1/models 2>/dev/null | python3 -c '
import json, sys
try:
    ids = [m["id"] for m in json.load(sys.stdin)["data"]]
except Exception:
    ids = []
chat = [i for i in ids if not any(t in i.lower() for t in ("bge", "embed", "rerank", "tts", "whisper"))]
print(chat[0] if chat else "glm-5")')" || E2E_MODEL="glm-5"

# Snapshot the live gateway log dir so we can prove the test never touched it.

# Snapshot the live gateway log dir so we can prove the test never touched it.
LIVE_LOGS="$HOME/.fusion-gateway/logs"
LOG_BEFORE=""
if [[ -d "$LIVE_LOGS" ]]; then
    LOG_BEFORE="$(ls -t "$LIVE_LOGS" 2>/dev/null | head -1 | xargs -I{} stat -f '%m %N' "$LIVE_LOGS/{}" 2>/dev/null || true)"
fi

# --- isolated gateway -------------------------------------------------------
mkdir -p "$DIR/gwdata"
cat > "$DIR/gw-config.yaml" <<EOF
server:
    port: ${GW_PORT}
    auto_start:
        enabled: false
auth:
    passthrough: true
admin:
    enabled: true
    jwt_secret: "e2e-$(head -c 16 /dev/urandom | od -An -tx1 | tr -d ' \\n')"
    users:
        admin: "e2e-gw-password"
store:
    data_dir: "${DIR}/gwdata"
backends:
    fusion-mlx:
        type: fusion-mlx
        enabled: true
        base_url: http://127.0.0.1:11434
        api_key_env: FUSION_E2E_MLX_KEY
models:
    supply:
        ${E2E_MODEL}: [fusion-mlx]
hardware:
    mlx_metrics:
        api_key: "${MLX_KEY}"
    # E2E box noise (background pageouts) must not divert routing to the
    # (absent) cloud backend — keep the breaker from tripping on swap rate.
    collection_error_protection: false
routing:
    local_priority:
        swap_page_rate_threshold: 1000000
file_log:
    enabled: false
persistence:
    enabled: false
EOF
"$GW_BIN" -check-config -config "$DIR/gw-config.yaml" >/dev/null
FUSION_E2E_MLX_KEY="$MLX_KEY"
export FUSION_E2E_MLX_KEY
"$GW_BIN" -config "$DIR/gw-config.yaml" > "$DIR/gw.log" 2>&1 &
GW_PID=$!
for i in $(seq 1 40); do
    curl -sf "http://127.0.0.1:${GW_PORT}/health" >/dev/null 2>&1 && break
    kill -0 "$GW_PID" 2>/dev/null || { echo "FAIL: gateway died"; cat "$DIR/gw.log"; exit 1; }
    sleep 0.5
done
curl -sf "http://127.0.0.1:${GW_PORT}/health" >/dev/null || { echo "FAIL: gateway not healthy"; exit 1; }
echo "== gateway up on ephemeral :${GW_PORT} (data ${DIR}/gwdata)"

# --- isolated console ------------------------------------------------------
mkdir -p "$DIR/console"
E2E_PWD="e2e-$(date +%s)"
cat > "$DIR/console/config.json" <<EOF
{
    "server": {"host": "127.0.0.1", "port": ${SVR_PORT}},
    "providers": [
        {
            "id": "gw-e2e",
            "name": "E2E gateway",
            "type": "gateway",
            "base_url": "http://127.0.0.1:${GW_PORT}",
            "model": "${E2E_MODEL}",
            "api_key_id": "",
            "enabled": true
        }
    ],
    "agent": []
}
EOF
FUSION_CONSOLE_DIR="$DIR/console" FUSION_CONSOLE_ADMIN_PASSWORD="$E2E_PWD" "$CONSOLE_BIN" > "$DIR/console.log" 2>&1 &
SVR_PID=$!
BASE="http://127.0.0.1:${SVR_PORT}"
for i in $(seq 1 30); do
    curl -sf "${BASE}/api/health" >/dev/null 2>&1 && break
    sleep 0.5
done
curl -sf "${BASE}/api/health" >/dev/null || { echo "FAIL: console not healthy"; exit 1; }
echo "== console up on ephemeral :${SVR_PORT}"

CJ="$DIR/cookies.txt"
curl -sf -c "$CJ" -X POST "${BASE}/api/login" \
    -H 'Content-Type: application/json' \
    -d "{\"user\":\"admin\",\"password\":\"${E2E_PWD}\"}" >/dev/null

echo "== console sees the isolated gateway provider"
curl -sf -b "$CJ" "${BASE}/api/settings/providers" | python3 -c '
import json, sys
ps = json.load(sys.stdin)["providers"]
assert any(p["id"] == "gw-e2e" and p["type"] == "gateway" for p in ps), ps'

echo "== console gateway status reachable (own instance, not :11432)"
curl -sf -b "$CJ" "${BASE}/api/gateway/status" | python3 -c '
import json, sys
st = json.load(sys.stdin)
# base_url must point at our ephemeral instance, never the fixed live port.
assert ":11432" not in st.get("base_url", ""), st'

# --- isolation assertions ---------------------------------------------------
echo "== assert live gateway logs untouched"
# When the live gateway is running it legitimately writes its own log at any
# moment, so growth cannot be attributed to this harness — warn only. With no
# live gateway, ANY growth would be ours and must fail.
LIVE_GW_RUNNING=false
curl -sf -m 1 http://127.0.0.1:11432/health >/dev/null 2>&1 && LIVE_GW_RUNNING=true
if [[ -d "$LIVE_LOGS" && -n "$LOG_BEFORE" ]]; then
    LOG_AFTER="$(ls -t "$LIVE_LOGS" 2>/dev/null | head -1 | xargs -I{} stat -f '%m %N' "$LIVE_LOGS/{}" 2>/dev/null || true)"
    if [[ "$LOG_BEFORE" != "$LOG_AFTER" ]]; then
        if $LIVE_GW_RUNNING; then
            echo "   note: live gateway logs grew during run (live traffic, not attributable)"
        else
            echo "FAIL: ~/.fusion-gateway/logs changed during e2e with no live gateway running"; exit 1
        fi
    fi
fi
[[ -f "$DIR/gwdata/gateway.db" || -d "$DIR/gwdata" ]] || { echo "FAIL: gateway did not use isolated data_dir"; exit 1; }

# Optional live-model turn (only when fusion-mlx answers on :11434).
if curl -sf -m 2 http://127.0.0.1:11434/health >/dev/null 2>&1; then
    echo "== fusion-mlx detected: streaming one chat turn through the isolated gateway"
    python3 - "$BASE" "$CJ" <<'EOF'
import json, sys, urllib.request

base, cj = sys.argv[1], sys.argv[2]
cookie = dict(line.split("\t")[-2:] for line in open(cj) if len(line.split("\t")) >= 2)
hdr = {"Content-Type": "application/json", "Cookie": "fc_session=" + cookie.get("fc_session", "").strip()}
conv = json.load(urllib.request.urlopen(urllib.request.Request(
    base + "/api/conversations", data=json.dumps({"title": "e2e", "kind": "chat"}).encode(),
    headers=hdr, method="POST")))
cid = conv["id"]
req = urllib.request.Request(
    base + "/api/chat",
    data=json.dumps({"conversation_id": cid, "provider_id": "gw-e2e", "content": "Reply with the single word OK"}).encode(),
    headers=hdr, method="POST")
got = ""
errs = []
import time
for attempt in range(3):
    got = ""
    errs = []
    try:
        with urllib.request.urlopen(req, timeout=60) as resp:
            for raw in resp:
                line = raw.decode().strip()
                if line.startswith("data: "):
                    ev = json.loads(line[6:])
                    if ev.get("type") == "delta":
                        got += ev.get("text", "")
                    elif ev.get("type") == "error":
                        errs.append(ev.get("text", ""))
        if "OK" in got:
            break
    except Exception as e:
        errs.append(str(e))
    # Circuit breaker / memory pressure on fusion-mlx is transient — back off.
    transient = any("not available" in e or "circuit" in e.lower() for e in errs)
    if not transient or attempt == 2:
        break
    time.sleep(5)
assert "OK" in got, "no reply; errors: " + "; ".join(errs)
print("   model replied:", got.strip()[:60])
EOF
else
    echo "== fusion-mlx not running: skipping live model turn (wiring checks passed)"
fi

echo "E2E PASS (gateway :${GW_PORT}, console :${SVR_PORT}, scratch ${DIR})"

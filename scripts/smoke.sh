#!/usr/bin/env bash
# Smoke test for a running console instance. Verifies auth, conversation CRUD
# and the dashboard API end-to-end against a throwaway data dir.
# Usage: scripts/smoke.sh [port]   (default 11481; requires curl + sqlite3)
set -euo pipefail

PORT="${1:-11481}"
BASE="http://127.0.0.1:${PORT}"
DIR="$(mktemp -d /tmp/fc-smoke.XXXXXX)"
PASS_PWD="smoke-$(date +%s)"

cleanup() {
    [[ -n "${SVR_PID:-}" ]] && kill "$SVR_PID" 2>/dev/null || true
    rm -rf "$DIR"
}
trap cleanup EXIT

BIN="$(cd "$(dirname "$0")/.." && pwd)/bin/fusion-console"
[[ -x "$BIN" ]] || { echo "FAIL: $BIN missing (run make build)"; exit 1; }

echo "== start console on :${PORT} (dir ${DIR})"
printf '{"server": {"host": "127.0.0.1", "port": %s}, "provider": [], "agent": []}\n' "$PORT" > "$DIR/config.json"
FUSION_CONSOLE_DIR="$DIR" FUSION_CONSOLE_ADMIN_PASSWORD="$PASS_PWD" "$BIN" &
SVR_PID=$!
for i in $(seq 1 30); do
    curl -sf "${BASE}/api/health" >/dev/null 2>&1 && break
    sleep 0.5
done

echo "== login"
CJ="$DIR/cookies.txt"
curl -sf -c "$CJ" -X POST "${BASE}/api/login" \
    -H 'Content-Type: application/json' \
    -d "{\"user\":\"admin\",\"password\":\"${PASS_PWD}\"}" >/dev/null

echo "== create + rename + list + delete conversation"
CID=$(curl -sf -b "$CJ" -X POST "${BASE}/api/conversations" \
    -H 'Content-Type: application/json' -d '{"title":"smoke","kind":"chat"}' | python3 -c 'import json,sys;print(json.load(sys.stdin)["id"])')
curl -sf -b "$CJ" -X PATCH "${BASE}/api/conversations/${CID}" \
    -H 'Content-Type: application/json' -d '{"title":"smoke-renamed"}' >/dev/null
curl -sf -b "$CJ" "${BASE}/api/conversations" | grep -q "smoke-renamed"
curl -sf -b "$CJ" -X DELETE "${BASE}/api/conversations/${CID}" >/dev/null
curl -sf -b "$CJ" "${BASE}/api/conversations" | grep -q "smoke-renamed" && { echo "FAIL: delete did not remove"; exit 1; } || true

echo "== dashboard empty state + bad range"
curl -sf -b "$CJ" "${BASE}/api/dashboard?range=week" | python3 -c 'import json,sys; d=json.load(sys.stdin); assert d["totals"]["brain_tokens"]==0, d'
CODE=$(curl -s -o /dev/null -w '%{http_code}' -b "$CJ" "${BASE}/api/dashboard?range=bogus")
[[ "$CODE" == "400" ]] || { echo "FAIL: bad range gave $CODE"; exit 1; }

echo "== seed a cost row, dashboard must reflect it"
TODAY=$(date -u +%Y-%m-%d)
CID2=$(curl -sf -b "$CJ" -X POST "${BASE}/api/conversations" \
    -H 'Content-Type: application/json' -d '{"title":"usage","kind":"chat"}' | python3 -c 'import json,sys;print(json.load(sys.stdin)["id"])')
sqlite3 "$DIR/console.db" "INSERT INTO cost_log (conv_id,user,source,cost_usd,tokens_in,tokens_out,created_at) VALUES (${CID2},'admin','chat:smoke',0.01,100,50,'${TODAY}T00:00:00Z');"
curl -sf -b "$CJ" "${BASE}/api/dashboard?range=today" | python3 -c 'import json,sys; d=json.load(sys.stdin); assert d["totals"]["brain_tokens"]==150, d["totals"]'

echo "== approvals endpoint: unknown id = 404"
CODE=$(curl -s -o /dev/null -w '%{http_code}' -b "$CJ" -X POST "${BASE}/api/approvals/appr-none" -H 'Content-Type: application/json' -d '{"approve":true}')
[[ "$CODE" == "404" ]] || { echo "FAIL: unknown approval gave $CODE"; exit 1; }

echo "SMOKE PASS (port ${PORT})"

#!/usr/bin/env bash
# fusion-console service lifecycle: start | stop | status | restart.
# Dev/host usage — the repo binary; release packages ship scripts/startup.sh
# (bootstrap + browser auto-login) instead.
# Usage: ./scripts/start.sh [start|stop|status|restart] [--open]
set -euo pipefail
cd "$(dirname "$0")/.."

REPO_BIN="bin/fusion-console"
BIN="$REPO_BIN"
[ -x "$BIN" ] || BIN="$HOME/.fusion/fusion-console/bin/fusion-console"
[ -x "$BIN" ] || { echo "start.sh: no fusion-console binary — run 'make build' first" >&2; exit 1; }

DIR="${FUSION_CONSOLE_DIR:-$HOME/.fusion/fusion-console}"
PID_FILE="$DIR/console.pid"
LOG_FILE="$DIR/log/console.log"
PORT="${FUSION_CONSOLE_PORT:-11480}"
ACTION="${1:-start}"
OPEN_FLAG=""
[ "${2:-}" = "--open" ] && OPEN_FLAG="-open"

pid_alive() { [ -f "$PID_FILE" ] && kill -0 "$(cat "$PID_FILE")" 2>/dev/null; }

port_open() {
    if command -v lsof >/dev/null 2>&1; then
        lsof -iTCP:"$PORT" -sTCP:LISTEN >/dev/null 2>&1
    else
        (exec 3<>"/dev/tcp/127.0.0.1/$PORT") 2>/dev/null
    fi
}

case "$ACTION" in
start)
    if pid_alive || port_open; then
        echo "console: already running on :$PORT (pid $(cat "$PID_FILE" 2>/dev/null || echo '?'))"
        exit 0
    fi
    mkdir -p "$DIR/log"
    nohup "$BIN" $OPEN_FLAG >> "$LOG_FILE" 2>&1 &
    echo $! > "$PID_FILE"
    for _ in $(seq 1 20); do
        curl -sf "http://127.0.0.1:$PORT/api/health" >/dev/null 2>&1 && break
        sleep 0.5
    done
    if curl -sf "http://127.0.0.1:$PORT/api/health" >/dev/null 2>&1; then
        echo "console: started (pid $(cat "$PID_FILE"), :$PORT, log $LOG_FILE)"
    else
        echo "console: started but health check failed — see $LOG_FILE" >&2
        exit 1
    fi
    ;;
stop)
    if pid_alive; then
        PID="$(cat "$PID_FILE")"
        kill "$PID"
        for _ in $(seq 1 20); do
            kill -0 "$PID" 2>/dev/null || break
            sleep 0.5
        done
        kill -0 "$PID" 2>/dev/null && kill -9 "$PID"
        rm -f "$PID_FILE"
        echo "console: stopped (pid $PID)"
    elif port_open; then
        echo "console: running on :$PORT without pid file — stop it manually" >&2
        exit 1
    else
        echo "console: not running"
        rm -f "$PID_FILE"
    fi
    ;;
status)
    if pid_alive || port_open; then
        echo "console: running on :$PORT (pid $(cat "$PID_FILE" 2>/dev/null || echo '?'))"
    else
        echo "console: not running"
        exit 1
    fi
    ;;
restart)
    "$0" stop || true
    "$0" start "${2:-}"
    ;;
*)
    echo "usage: $0 [start|stop|status|restart] [--open]" >&2
    exit 2
    ;;
esac

#!/bin/bash
#
# End-to-end test for the status command. It starts a local gossh server and
# checks both the successful JSON response and the response to a missing path.

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GOSSH="${GOSSH:-$ROOT_DIR/bin/gossh}"
STATUS_PORT="${GOSSH_STATUS_TEST_PORT:-17777}"
STATUS_URL="http://127.0.0.1:$STATUS_PORT"
WORK_DIR="$(mktemp -d)"
SERVER_PID=""

cleanup() {
    if [ -n "$SERVER_PID" ] && kill -0 "$SERVER_PID" 2>/dev/null; then
        kill "$SERVER_PID" 2>/dev/null || true
        wait "$SERVER_PID" 2>/dev/null || true
    fi

    if [ -n "$WORK_DIR" ] && [ -d "$WORK_DIR" ]; then
        rm -rf -- "$WORK_DIR"
    fi
}
trap cleanup EXIT

if [ ! -x "$GOSSH" ]; then
    echo "[FAIL] gossh binary not found or not executable: $GOSSH"
    echo "       Build it first with: make build"
    exit 1
fi

# Keep the server logs and CLI state out of the developer's home directory.
export HOME="$WORK_DIR/home"
mkdir -p "$HOME"

echo "[TEST] Starting gossh server on port $STATUS_PORT..."
"$GOSSH" server --port "$STATUS_PORT" --ssh 1 -q >"$WORK_DIR/server.log" 2>&1 &
SERVER_PID=$!

ready=false
for _ in $(seq 1 50); do
    if ! kill -0 "$SERVER_PID" 2>/dev/null; then
        echo "[FAIL] gossh server exited before becoming ready"
        cat "$WORK_DIR/server.log"
        exit 1
    fi
    if curl --silent --fail "$STATUS_URL/status" >/dev/null; then
        ready=true
        break
    fi
    sleep 0.2
done

if [ "$ready" != true ]; then
    echo "[FAIL] gossh server did not become ready on port $STATUS_PORT"
    cat "$WORK_DIR/server.log"
    exit 1
fi

echo "[TEST] Checking the successful status response..."
status_output="$("$GOSSH" status --target "$STATUS_URL")"
for field in uptime active_sessions bytes_in bytes_out; do
    if ! printf '%s\n' "$status_output" | grep -q "\"$field\":"; then
        echo "[FAIL] status output is missing the $field field"
        printf '%s\n' "$status_output"
        exit 1
    fi
done
if ! printf '%s\n' "$status_output" | grep -Eq '"rtt_ms":[[:space:]]*[0-9]+([.][0-9]+)?'; then
    echo "[FAIL] status output is missing a numeric rtt_ms field"
    printf '%s\n' "$status_output"
    exit 1
fi

echo "[TEST] Checking the status error response..."
if "$GOSSH" status --target "$STATUS_URL/missing" >"$WORK_DIR/error.log" 2>&1; then
    echo "[FAIL] status succeeded for a path that does not exist"
    cat "$WORK_DIR/error.log"
    exit 1
fi
if ! grep -q 'server returned 404 Not Found' "$WORK_DIR/error.log"; then
    echo "[FAIL] status did not report the expected 404 error"
    cat "$WORK_DIR/error.log"
    exit 1
fi

echo "[OK] Status command returned JSON and handled a missing endpoint."

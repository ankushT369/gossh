#!/bin/bash
#
# Launch smoke test: starts gossh in both modes and checks that neither exits
# on its own. It does not move traffic through the tunnel, because the client
# always rewrites the remote URL to wss:// and so cannot talk to a plain HTTP
# server on localhost.

set -e

# Both the Makefile and CI build the binary as bin/gossh. GOSSH overrides that
# for a binary built somewhere else.
GOSSH="${GOSSH:-./bin/gossh}"

SERVER_PORT=7777
CLIENT_PORT=8888
WS_URL="ws://127.0.0.1:$SERVER_PORT"

echo "[TEST] OS detected: $(uname -s)"
echo "[TEST] Using binary: $GOSSH"

if [ ! -x "$GOSSH" ]; then
    echo "[ERROR] gossh binary not found (or not executable) at: $GOSSH"
    echo "[ERROR] Build it first with: make build"
    exit 1
fi

SERVER_PID=""
CLIENT_PID=""

cleanup() {
    if [ -n "$CLIENT_PID" ]; then
        kill "$CLIENT_PID" 2>/dev/null || true
    fi
    if [ -n "$SERVER_PID" ]; then
        kill "$SERVER_PID" 2>/dev/null || true
    fi
}
trap cleanup EXIT

echo "[TEST] Starting gossh server..."
"$GOSSH" server --port "$SERVER_PORT" --ssh 22 -vvv &
SERVER_PID=$!
sleep 1

if ps -p "$SERVER_PID" > /dev/null; then
    echo "[OK] Server started (PID=$SERVER_PID)"
else
    echo "[FAIL] Server failed to start"
    exit 1
fi

echo "[TEST] Starting gossh client..."
"$GOSSH" client --connect "$WS_URL" --port "$CLIENT_PORT" -vvv &
CLIENT_PID=$!
sleep 1

if ps -p "$CLIENT_PID" > /dev/null; then
    echo "[OK] Client started (PID=$CLIENT_PID)"
else
    echo "[FAIL] Client failed to start"
    exit 1
fi

echo ""
echo "[TEST] Both server and client launched successfully!"
sleep 2

# Neither side may have fallen over in the meantime.
if ! ps -p "$SERVER_PID" > /dev/null; then
    echo "[FAIL] Server exited unexpectedly"
    exit 1
fi

if ! ps -p "$CLIENT_PID" > /dev/null; then
    echo "[FAIL] Client exited unexpectedly"
    exit 1
fi

echo "[TEST] Stopping processes..."
cleanup

echo ""

sleep 2

echo "[TEST] Starting gossh server with --daemon flag..."
"$GOSSH" server --port "$SERVER_PORT" --ssh 22 -vvv --daemon
sleep 1

SERVER_PID=$(cat ~/.gossh/server-${SERVER_PORT}.pid)

if ps -p "$SERVER_PID" > /dev/null; then
    echo "[OK] Server started (PID=$SERVER_PID)"
else
    echo "[FAIL] Server failed to start"
    exit 1
fi

echo "[TEST] Starting gossh client with --daemon flag..."
"$GOSSH" client --connect "$WS_URL" --port "$CLIENT_PORT" -vvv --daemon
CLIENT_PID=$(cat ~/.gossh/client-${CLIENT_PORT}.pid)
sleep 1

if ps -p "$CLIENT_PID" > /dev/null; then
    echo "[OK] Client started (PID=$CLIENT_PID)"
else
    echo "[FAIL] Client failed to start"
    exit 1
fi

echo ""
echo "[TEST] Both server and client launched successfully!"
sleep 2

# Neither side may have fallen over in the meantime.
if ! ps -p "$SERVER_PID" > /dev/null; then
    echo "[FAIL] Server exited unexpectedly"
    exit 1
fi

if ! ps -p "$CLIENT_PID" > /dev/null; then
    echo "[FAIL] Client exited unexpectedly"
    exit 1
fi

echo "[TEST] Stopping processes..."
echo "[DONE] Test complete."

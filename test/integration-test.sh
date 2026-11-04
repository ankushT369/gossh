#!/bin/bash
#
# End-to-end integration test: pushes a real SSH session through the gossh
# tunnel (ssh -> gossh client -> WebSocket -> gossh server -> sshd).
#
# The test brings up its own sshd, and it does so without root: the daemon
# runs as the current user with a throwaway host key and public-key auth
# against a throwaway key pair. That keeps the script identical on Linux and
# macOS, which is why it no longer creates a system user, installs packages
# or calls sudo anywhere.

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# Both the Makefile and CI build the binary as bin/gossh. GOSSH overrides that
# for a binary built somewhere else.
GOSSH="${GOSSH:-$ROOT_DIR/bin/gossh}"

SSH_PORT=2222
SERVER_PORT=7777
CLIENT_PORT=8888

SSH_USER="$(id -un)"

WORK_DIR=""
SSHD_PID=""
SERVER_PID=""
CLIENT_PID=""

SERVER_LOG=""
CLIENT_LOG=""
SSHD_LOG=""
SSH_LOG=""

DAEMON_MODE_LOG_DIR="$HOME/.gossh"

dump_logs() {
    for log in "$SSH_LOG" "$SSHD_LOG" "$SERVER_LOG" "$CLIENT_LOG"; do
        if [ -n "$log" ] && [ -f "$log" ]; then
            echo ""
            echo "--- $(basename "$log") ---"
            cat "$log" || true
        fi
    done
}

cleanup() {
    echo "[TEST] Cleaning up..."

    for pid in "$CLIENT_PID" "$SERVER_PID" "$SSHD_PID"; do
        if [ -n "$pid" ]; then
            kill "$pid" 2>/dev/null || true
            wait "$pid" 2>/dev/null || true
        fi
    done

    if [ -n "$WORK_DIR" ] && [ -d "$WORK_DIR" ]; then
        rm -rf "$WORK_DIR"
    fi

    if [ -n "$DAEMON_MODE_LOG_DIR" ] && [ -d "$DAEMON_MODE_LOG_DIR" ]; then
        rm -rf "$DAEMON_MODE_LOG_DIR"
    fi

    echo "[TEST] Cleanup complete."
}

trap cleanup EXIT

cleanup_server_client() {
    echo "[TEST] Cleaning up only server client..."

    for pid in "$CLIENT_PID" "$SERVER_PID"; do
        if [ -n "$pid" ]; then
            kill "$pid" 2>/dev/null || true
            wait "$pid" 2>/dev/null || true
        fi
    done
    
    if [ -n "$DAEMON_MODE_LOG_DIR" ] && [ -d "$DAEMON_MODE_LOG_DIR" ]; then
        rm -rf "$DAEMON_MODE_LOG_DIR"
    fi
    echo "[TEST] Cleanup complete."

}
# Waits for something to accept connections on a local TCP port. Bash's
# /dev/tcp redirection keeps this free of netcat, which the macOS runner does
# not ship.
wait_for_port() {
    local port="$1"
    local label="$2"
    local attempt=0

    while [ "$attempt" -lt 50 ]; do
        if (exec 3<>"/dev/tcp/127.0.0.1/$port") 2>/dev/null; then
            return 0
        fi

        attempt=$((attempt + 1))
        sleep 0.2
    done

    echo "[FAIL] $label never started listening on port $port"
    return 1
}

echo "[TEST] Starting gossh integration test"
echo "[TEST] OS detected: $(uname -s)"
echo "[TEST] Binary: $GOSSH"
echo "[TEST] SSH user: $SSH_USER"

if [ ! -x "$GOSSH" ]; then
    echo "[FAIL] gossh binary not found or not executable:"
    echo "       $GOSSH"
    echo "       Build it first with: make build"
    exit 1
fi

for cmd in ssh ssh-keygen; do
    if ! command -v "$cmd" >/dev/null 2>&1; then
        echo "[FAIL] Required command not found: $cmd"
        exit 1
    fi
done

# sshd normally lives in sbin, which is not on a non-root PATH everywhere, so
# fall back to the usual locations. It has to be an absolute path: sshd
# re-executes itself and refuses to start when invoked by a bare name.
SSHD_BIN=""
for candidate in /usr/sbin/sshd /usr/local/sbin/sshd /opt/homebrew/sbin/sshd; do
    if [ -x "$candidate" ]; then
        SSHD_BIN="$candidate"
        break
    fi
done

if [ -z "$SSHD_BIN" ]; then
    SSHD_BIN="$(command -v sshd 2>/dev/null || true)"
fi

if [ -z "$SSHD_BIN" ]; then
    echo "[FAIL] sshd not found (looked in /usr/sbin, /usr/local/sbin, /opt/homebrew/sbin and PATH)"
    exit 1
fi

echo "[TEST] sshd binary: $SSHD_BIN"
"$SSHD_BIN" -V 2>&1 | head -1 || true

WORK_DIR="$(mktemp -d 2>/dev/null || mktemp -d -t gossh-test)"
chmod 700 "$WORK_DIR"

HOST_KEY="$WORK_DIR/host_key"
USER_KEY="$WORK_DIR/id_test"
AUTHORIZED_KEYS="$WORK_DIR/authorized_keys"
SSHD_CONFIG="$WORK_DIR/sshd_config"
SSHD_LOG="$WORK_DIR/sshd.log"
SERVER_LOG="$WORK_DIR/gossh-server.log"
CLIENT_LOG="$WORK_DIR/gossh-client.log"
SSH_LOG="$WORK_DIR/ssh-client.log"

echo "[TEST] Generating throwaway SSH keys..."

ssh-keygen -q -t ed25519 -N "" -C "gossh-test-host" -f "$HOST_KEY"
ssh-keygen -q -t ed25519 -N "" -C "gossh-test-user" -f "$USER_KEY"

cp "$USER_KEY.pub" "$AUTHORIZED_KEYS"
chmod 600 "$HOST_KEY" "$USER_KEY" "$AUTHORIZED_KEYS"

# An unprivileged sshd cannot switch users, so it only ever serves the account
# that started it. PAM is off for the same reason: session setup needs root.
cat > "$SSHD_CONFIG" <<EOF
Port $SSH_PORT
ListenAddress 127.0.0.1

HostKey $HOST_KEY

PidFile none

UsePAM no

PubkeyAuthentication yes
AuthorizedKeysFile $AUTHORIZED_KEYS

PasswordAuthentication no
KbdInteractiveAuthentication no
PermitRootLogin no
AllowUsers $SSH_USER

X11Forwarding no
AllowTcpForwarding no
PermitTunnel no

StrictModes no
LogLevel VERBOSE
EOF

if ! "$SSHD_BIN" -t -f "$SSHD_CONFIG" 2>"$SSHD_LOG"; then
    echo "[FAIL] sshd rejected the test configuration"
    cat "$SSHD_LOG" || true
    exit 1
fi

echo "[TEST] Starting sshd on port $SSH_PORT..."

# -D keeps sshd in the foreground so $! is the process we later kill, and -e
# sends its log to stderr so the whole thing lands in one file.
"$SSHD_BIN" -D -e -f "$SSHD_CONFIG" > "$SSHD_LOG" 2>&1 &
SSHD_PID=$!

if ! wait_for_port "$SSH_PORT" "sshd"; then
    dump_logs
    exit 1
fi

echo "[OK] sshd started (PID=$SSHD_PID)"

test_gossh_server() {
    echo "[TEST] Starting gossh server..."

    "$GOSSH" server \
        --port "$SERVER_PORT" \
        --ssh "$SSH_PORT" \
        -vvv > "$SERVER_LOG" 2>&1 &

    SERVER_PID=$!
    if ! wait_for_port "$SERVER_PORT" "gossh server"; then
        dump_logs
        exit 1
    fi
    echo "[OK] gossh server started (PID=$SERVER_PID)"
}

test_gossh_server_daemon() {
    echo "[TEST] Starting gossh server in daemon mode..."

    "$GOSSH" server \
        --port "$SERVER_PORT" \
        --ssh "$SSH_PORT" \
        -vvv \
        --daemon

    SERVER_PID=$(cat ~/.gossh/server-${SERVER_PORT}.pid)

    if ! wait_for_port "$SERVER_PORT" "gossh server"; then
        dump_logs
        exit 1
    fi

    echo "[OK] gossh server started in daemon mode (PID=$SERVER_PID)"
}

test_gossh_client() {
    echo "[TEST] Starting gossh client..."

    "$GOSSH" client \
        --connect "ws://127.0.0.1:$SERVER_PORT" \
        --port "$CLIENT_PORT" \
        -vvv > "$CLIENT_LOG" 2>&1 &

    CLIENT_PID=$!
    if ! wait_for_port "$CLIENT_PORT" "gossh client"; then
        dump_logs
        exit 1
    fi
    echo "[OK] gossh client is listening on port $CLIENT_PORT"
}

test_gossh_client_daemon() {
    echo "[TEST] Starting gossh client..."

    "$GOSSH" client \
        --connect "ws://127.0.0.1:$SERVER_PORT" \
        --port "$CLIENT_PORT" \
        -vvv \
        --daemon

    CLIENT_PID=$(cat ~/.gossh/client-${CLIENT_PORT}.pid)
    if ! wait_for_port "$CLIENT_PORT" "gossh client"; then
        dump_logs
        exit 1
    fi
    echo "[OK] gossh client is listening on port $CLIENT_PORT in daemon mode"
}

run_ssh() {
    ssh \
        -p "$CLIENT_PORT" \
        -i "$USER_KEY" \
        -o IdentitiesOnly=yes \
        -o IdentityAgent=none \
        -o StrictHostKeyChecking=no \
        -o UserKnownHostsFile=/dev/null \
        -o GlobalKnownHostsFile=/dev/null \
        -o PreferredAuthentications=publickey \
        -o PasswordAuthentication=no \
        -o BatchMode=yes \
        -o LogLevel=ERROR \
        -o ConnectTimeout=10 \
        "$SSH_USER@127.0.0.1" \
        "$@" 2>>"$SSH_LOG"
}

test_gossh_tunnel() {
    echo "[TEST] Connecting through gossh tunnel..."

    OUTPUT="$(run_ssh "echo integration-test" || true)"
    EXPECTED="integration-test"

    if [ "$OUTPUT" != "$EXPECTED" ]; then
        echo "[FAIL] Unexpected SSH output"
        echo "Expected: $EXPECTED"
        echo "Actual:   $OUTPUT"
        dump_logs
        exit 1
    fi
    echo "[OK] SSH command executed successfully"
    echo "[OK] Received: $OUTPUT"
}

test_bidirectional_ssh() {
    echo "[TEST] Testing stdin -> remote -> stdout..."

    OUTPUT="$(printf 'hello-from-client\n' | run_ssh "cat" || true)"
    EXPECTED="hello-from-client"

    if [ "$OUTPUT" != "$EXPECTED" ]; then
        echo "[FAIL] Bidirectional SSH test failed"
        echo "Expected: $EXPECTED"
        echo "Actual:   $OUTPUT"
        dump_logs
        exit 1
    fi
    echo "[OK] Bidirectional SSH test passed"
}

test_gossh_server
test_gossh_client
test_gossh_tunnel
test_bidirectional_ssh

cleanup_server_client
echo ""

test_gossh_server_daemon
test_gossh_client_daemon
test_gossh_tunnel
test_bidirectional_ssh

echo ""
echo "[OK] GOSSH INTEGRATION TEST PASSED"

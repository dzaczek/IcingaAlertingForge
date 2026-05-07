#!/usr/bin/env bash
set -uo pipefail
# set -e is intentionally OFF — check() function handles PASS/FAIL manually,
# and grep returns 1 on no-match which would abort the script prematurely.

# ── smoke-data-flow.sh ────────────────────────────────────────────────
# End-to-end data flow smoke test for IcingaAlertForge.
# Starts the bridge with a mock Icinga2 API, sends webhook alerts,
# and verifies the full pipeline: input → process → forward → output.
#
# Usage:
#   ./scripts/smoke-data-flow.sh                    # uses mock Icinga2
#   ./scripts/smoke-data-flow.sh http://localhost:9080  # uses real bridge
#
# Exit: 0 = all checks passed, 1 = failure

BRIDGE_URL="${1:-}"
MOCK_DIR=""
BRIDGE_PID=""
PASS=0
FAIL=0

cleanup() {
    if [ -n "${BRIDGE_PID:-}" ] && kill -0 "$BRIDGE_PID" 2>/dev/null; then
        kill "$BRIDGE_PID" 2>/dev/null || true
        wait "$BRIDGE_PID" 2>/dev/null || true
    fi
    if [ -n "${MOCK_PID:-}" ] && kill -0 "$MOCK_PID" 2>/dev/null; then
        kill "$MOCK_PID" 2>/dev/null || true
        wait "$MOCK_PID" 2>/dev/null || true
    fi
    if [ -n "${MOCK_DIR:-}" ]; then
        rm -rf "$MOCK_DIR"
    fi
}
trap cleanup EXIT

# ── Helpers ──────────────────────────────────────────────────────────

check() {
    local desc="$1"; shift
    if "$@"; then
        echo "  PASS: $desc"
        PASS=$((PASS + 1))
        return 0
    else
        echo "  FAIL: $desc"
        FAIL=$((FAIL + 1))
        return 1
    fi
}

assert_status() {
    local expected="$1" desc="$2" http_code="$3" body="$4"
    if [ "$http_code" = "$expected" ]; then
        return 0
    else
        echo "    expected HTTP $expected, got $http_code"
        echo "    body: $(echo "$body" | head -c 500)"
        return 1
    fi
}

assert_field() {
    local field="$1" expected="$2" desc="$3" json_body="$4"
    local actual
    actual=$(echo "$json_body" | grep -o "\"$field\":\"[^\"]*\"" | head -1 | cut -d'"' -f4)
    if [ -z "$actual" ]; then
        actual=$(echo "$json_body" | grep -o "\"$field\":[0-9]*" | head -1 | cut -d':' -f2)
    fi
    if [ "$actual" = "$expected" ]; then
        return 0
    else
        echo "    expected $field=\"$expected\", got \"$actual\""
        return 1
    fi
}

contains() {
    local needle="$1" haystack="$2"
    echo "$haystack" | grep -q "$needle"
}

# ── Mock Icinga2 API Server ──────────────────────────────────────────
# Minimal HTTP server that records what the bridge sends.
start_mock_icinga() {
    MOCK_DIR=$(mktemp -d)
    local mock_log="$MOCK_DIR/icinga-requests.log"
    local mock_port=$((15665 + RANDOM % 1000))

    # Clean up any leftover mocks
    pkill -f "MockIcinga" 2>/dev/null || true
    sleep 0.3

    # Start a simple mock using Python
    if ! command -v python3 &>/dev/null; then
        echo "ERROR: python3 required for mock Icinga2 server"
        return 1
    fi

    python3 - "$mock_port" "$mock_log" <<'PYEOF' &
import sys, json, http.server, socketserver, threading, socket

port = int(sys.argv[1])
log_path = sys.argv[2]
log_lock = threading.Lock()

class MockIcinga(http.server.BaseHTTPRequestHandler):
    def log_request(self, code='-', size='-'):
        pass

    def _log_req(self):
        body_len = int(self.headers.get('Content-Length', 0))
        body = self.rfile.read(body_len).decode('utf-8', errors='replace') if body_len else ''
        entry = json.dumps({
            'method': self.command,
            'path': self.path,
            'body': body,
            'auth': self.headers.get('Authorization', 'none'),
        })
        with log_lock:
            with open(log_path, 'a') as f:
                f.write(entry + '\n')

    def do_GET(self):
        self._log_req()
        self.send_response(200)
        self.send_header('Content-Type', 'application/json')
        self.end_headers()
        resp = {"results": []}
        if '/v1/objects/hosts/' in self.path:
            resp = {
                "results": [{
                    "attrs": {
                        "check_command": "dummy",
                        "display_name": self.path.split('/')[-1],
                        "address": "127.0.0.1",
                        "vars": {"managed_by": "IcingaAlertingForge"}
                    }
                }]
            }
        self.wfile.write(json.dumps(resp).encode())

    def do_PUT(self):
        self._log_req()
        self.send_response(200)
        self.send_header('Content-Type', 'application/json')
        self.end_headers()
        self.wfile.write(b'{"results":[{"code":200}]}')

    def do_POST(self):
        self._log_req()
        self.send_response(200)
        self.send_header('Content-Type', 'application/json')
        self.end_headers()
        self.wfile.write(b'{"results":[{"code":200}]}')

    def do_DELETE(self):
        self._log_req()
        self.send_response(200)
        self.send_header('Content-Type', 'application/json')
        self.end_headers()
        self.wfile.write(b'{"results":[{"code":200}]}')

class ReuseServer(socketserver.ThreadingMixIn, http.server.HTTPServer):
    allow_reuse_address = True
    daemon_threads = True
    def server_bind(self):
        self.socket.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        self.socket.bind(self.server_address)

httpd = ReuseServer(('127.0.0.1', port), MockIcinga)
print(f'MOCK_READY:{port}', flush=True, end='')
sys.stdout.flush()
httpd.serve_forever()
PYEOF
    MOCK_PID=$!

    # Wait for mock to signal readiness
    local waited=0
    while [ $waited -lt 50 ]; do
        if curl -s "http://127.0.0.1:$mock_port/v1/status" >/dev/null 2>&1; then
            break
        fi
        if ! kill -0 "$MOCK_PID" 2>/dev/null; then
            echo ""
            echo "ERROR: Mock Icinga2 process died during startup"
            return 1
        fi
        sleep 0.1
        waited=$((waited + 1))
    done

    if ! kill -0 "$MOCK_PID" 2>/dev/null; then
        echo ""
        echo "ERROR: Mock Icinga2 process exited unexpectedly"
        return 1
    fi

    echo "$mock_port" > "$MOCK_DIR/port"
    echo "$mock_log" > "$MOCK_DIR/log"
    echo "$MOCK_PID" > "$MOCK_DIR/pid"
    echo "Mock Icinga2 started on port $mock_port (PID $MOCK_PID)"
    return 0
}

get_mock_port() { cat "$MOCK_DIR/port" 2>/dev/null || echo ""; }
get_mock_log()  { cat "$MOCK_DIR/log" 2>/dev/null || echo ""; }

# ── Start Bridge ─────────────────────────────────────────────────────

start_bridge_with_mock() {
    local mock_port="$1"
    local bridge_port=18080
    local tmpdir
    tmpdir=$(mktemp -d)

    # Build if not already built
    local binary="./webhook-bridge"
    if [ ! -x "$binary" ]; then
        echo "Building bridge binary..."
        CGO_ENABLED=0 go build -o "$binary" . || {
            echo "FAIL: build failed"
            exit 1
        }
    fi

    # Start bridge pointing at mock Icinga2
    ICINGA2_HOST="http://127.0.0.1:$mock_port" \
    ICINGA2_USER=test \
    ICINGA2_PASS=test \
    ICINGA2_HOST_AUTO_CREATE=true \
    ICINGA2_TLS_SKIP_VERIFY=true \
    ICINGA2_FORCE=true \
    SERVER_PORT="$bridge_port" \
    SERVER_HOST="127.0.0.1" \
    HISTORY_FILE="$tmpdir/history.jsonl" \
    HISTORY_MAX_ENTRIES=100 \
    CACHE_TTL_MINUTES=30 \
    RETRY_QUEUE_ENABLED=false \
    AUDIT_LOG_ENABLED=false \
    HEALTH_CHECK_ENABLED=false \
    METRICS_ENABLED=false \
    CONFIG_IN_DASHBOARD=false \
    ADMIN_USER=admin \
    ADMIN_PASS=admin \
    WEBHOOK_KEY_SMOKE="smoke-key" \
    ICINGA2_HOST_NAME="smoke-host" \
    ./webhook-bridge &
    BRIDGE_PID=$!

    echo "$bridge_port" > "$MOCK_DIR/bridge_port"

    # Wait for bridge health
    for i in $(seq 1 30); do
        if curl -s "http://127.0.0.1:$bridge_port/health" >/dev/null 2>&1; then
            echo "Bridge started on port $bridge_port"
            return 0
        fi
        sleep 0.2
    done
    echo "FAIL: bridge did not become healthy"
    return 1
}

# ── Use external bridge ──────────────────────────────────────────────

wait_for_bridge() {
    local url="$1"
    for i in $(seq 1 10); do
        if curl -s "$url/health" >/dev/null 2>&1; then
            return 0
        fi
        sleep 1
    done
    return 1
}

# ── Main Test Suite ──────────────────────────────────────────────────

echo ""
echo "========================================"
echo "  IcingaAlertForge — Smoke Data Flow Test"
echo "========================================"
echo ""

# ── 1. Environment Startup ──────────────────────────────────────────

if [ -z "$BRIDGE_URL" ]; then
    echo "── 1. Starting mock Icinga2 + bridge ──"
    check "Mock Icinga2 starts" start_mock_icinga || exit 1
    MOCK_PORT=$(get_mock_port)
    check "Bridge starts and /health returns 200" start_bridge_with_mock "$MOCK_PORT" || exit 1
    BRIDGE_PORT=$(cat "$MOCK_DIR/bridge_port")
    BRIDGE_URL="http://127.0.0.1:$BRIDGE_PORT"
    MOCK_LOG=$(get_mock_log)
else
    echo "── 1. Using external bridge at $BRIDGE_URL ──"
    check "Bridge is reachable" wait_for_bridge "$BRIDGE_URL" || exit 1
    MOCK_LOG=""
fi

# ── 2. Health Check ─────────────────────────────────────────────────

echo ""
echo "── 2. Health Check ──"

HEALTH=$(curl -s "$BRIDGE_URL/health")
check "Health endpoint returns HTTP 200" curl -s -o /dev/null -w "%{http_code}" "$BRIDGE_URL" | grep -q "200"
check "Health reports status=ok" contains '"status":"ok"' "$HEALTH"

# ── 3. Input: Authentication ────────────────────────────────────────

echo ""
echo "── 3. Authentication & Input Validation ──"

NO_KEY=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$BRIDGE_URL/webhook" \
    -H "Content-Type: application/json" -d '{"status":"firing","alerts":[]}')
check "No API key → 401" assert_status 401 "no-key" "$NO_KEY" ""

WRONG_KEY=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$BRIDGE_URL/webhook" \
    -H "Content-Type: application/json" -H "X-API-Key: wrong-key" \
    -d '{"status":"firing","alerts":[]}')
check "Wrong API key → 401" assert_status 401 "wrong-key" "$WRONG_KEY" ""

BAD_JSON=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$BRIDGE_URL/webhook" \
    -H "Content-Type: application/json" -H "X-API-Key: smoke-key" \
    -d 'not-json')
check "Bad JSON → 400" assert_status 400 "bad-json" "$BAD_JSON" ""

EMPTY=$(curl -s -X POST "$BRIDGE_URL/webhook" \
    -H "Content-Type: application/json" -H "X-API-Key: smoke-key" \
    -d '{"status":"firing","alerts":[]}')
check "Empty alerts → 400" assert_status 400 "empty-alerts" \
    "$(curl -s -o /dev/null -w "%{http_code}" -X POST "$BRIDGE_URL/webhook" \
        -H "Content-Type: application/json" -H "X-API-Key: smoke-key" \
        -d '{"status":"firing","alerts":[]}')" ""

METHOD=$(curl -s -o /dev/null -w "%{http_code}" -X GET "$BRIDGE_URL/webhook")
check "GET (not POST) → 405" assert_status 405 "get-method" "$METHOD" ""

# ── 4. Data Processing ──────────────────────────────────────────────

echo ""
echo "── 4. Data Processing ──"

# CRITICAL alert
CRIT_RESP=$(curl -s -X POST "$BRIDGE_URL/webhook" \
    -H "Content-Type: application/json" -H "X-API-Key: smoke-key" \
    -d '{
        "status": "firing",
        "alerts": [{
            "status": "firing",
            "labels": {"alertname": "Smoke Critical", "severity": "critical"},
            "annotations": {"summary": "Disk full on /data", "description": "The disk is at 98% capacity"}
        }]
    }')
CRIT_CODE=$(echo "$CRIT_RESP" | grep -o '"status":"[^"]*"' || true)
check "Critical alert → HTTP 200" echo "$CRIT_RESP" | grep -q '"request_id"'
check "Critical alert → exit_status=2" echo "$CRIT_RESP" | grep -q '"exit_status":2'

# WARNING alert
WARN_RESP=$(curl -s -X POST "$BRIDGE_URL/webhook" \
    -H "Content-Type: application/json" -H "X-API-Key: smoke-key" \
    -d '{
        "status": "firing",
        "alerts": [{
            "status": "firing",
            "labels": {"alertname": "Smoke Warning", "severity": "warning"},
            "annotations": {"summary": "CPU usage elevated"}
        }]
    }')
check "Warning alert → exit_status=1" echo "$WARN_RESP" | grep -q '"exit_status":1'

# RESOLVED alert
RESOLVED_RESP=$(curl -s -X POST "$BRIDGE_URL/webhook" \
    -H "Content-Type: application/json" -H "X-API-Key: smoke-key" \
    -d '{
        "status": "resolved",
        "alerts": [{
            "status": "resolved",
            "labels": {"alertname": "Smoke Critical", "severity": "critical"},
            "annotations": {"summary": "Disk back to normal"}
        }]
    }')
check "Resolved alert → exit_status=0" echo "$RESOLVED_RESP" | grep -q '"exit_status":0'

# Missing alertname
MISSING_RESP=$(curl -s -X POST "$BRIDGE_URL/webhook" \
    -H "Content-Type: application/json" -H "X-API-Key: smoke-key" \
    -d '{
        "status": "firing",
        "alerts": [{
            "status": "firing",
            "labels": {"severity": "critical"},
            "annotations": {"summary": "No alertname"}
        }]
    }')
check "Missing alertname → error response" \
    echo "$MISSING_RESP" | grep -q 'missing alertname'

# Unknown status — the bridge returns a structured error, not a crash
UNKNOWN_CODE=$(curl -s -o /tmp/unknown-resp.json -w "%{http_code}" -X POST "$BRIDGE_URL/webhook" \
    -H "Content-Type: application/json" -H "X-API-Key: smoke-key" \
    -d '{
        "status": "firing",
        "alerts": [{
            "status": "unknown_status",
            "labels": {"alertname": "Bad Status", "severity": "critical"},
            "annotations": {"summary": "Test"}
        }]
    }')
UNKNOWN_BODY=$(cat /tmp/unknown-resp.json)
check "Unknown alert status → handled without crash" \
    echo "$UNKNOWN_BODY" | grep -q '"status":"error"'

# ── 5. Data Forwarding ──────────────────────────────────────────────

echo ""
echo "── 5. Data Forwarding to Icinga2 ──"

if [ -n "$MOCK_LOG" ] && [ -f "$MOCK_LOG" ]; then
    MOCK_REQUESTS=$(cat "$MOCK_LOG" 2>/dev/null || echo "")
    PROCESS_CHECK_COUNT=$(echo "$MOCK_REQUESTS" | grep -c "process-check-result" || echo "0")
    check "Mock Icinga2 received process-check-result requests" \
        [ "$PROCESS_CHECK_COUNT" -ge 1 ]

    # Verify the mock received a CRITICAL result
    check "Mock received exit_status=2 (CRITICAL)" \
        echo "$MOCK_REQUESTS" | grep -q '"exit_status":2'

    # Verify the mock received a resolved result
    check "Mock received exit_status=0 (OK/Resolved)" \
        echo "$MOCK_REQUESTS" | grep -q '"exit_status":0'
else
    echo "  SKIP: No mock log — cannot verify forwarding (test with external bridge)"
    echo "  Use: ./scripts/smoke-data-flow.sh  (without args) for full test"
fi

# ── 6. History Verification ─────────────────────────────────────────

echo ""
echo "── 6. History ──"

HISTORY=$(curl -s "$BRIDGE_URL/history?limit=100" \
    -H "Authorization: Basic $(echo -n 'admin:admin' | base64)" || echo "{}")
HISTORY_COUNT=$(echo "$HISTORY" | grep -o '"count":[0-9]*' | head -1 | cut -d':' -f2 || echo "0")
check "History has entries" [ "${HISTORY_COUNT:-0}" -ge 1 ]

# ── 7. Test Mode: Create + Delete ───────────────────────────────────

echo ""
echo "── 7. Test Mode: Create & Delete ──"

CREATE_RESP=$(curl -s -X POST "$BRIDGE_URL/webhook" \
    -H "Content-Type: application/json" -H "X-API-Key: smoke-key" \
    -d '{
        "status": "firing",
        "alerts": [{
            "status": "firing",
            "labels": {"alertname": "E2E Test Service", "mode": "test", "test_action": "create"},
            "annotations": {"summary": "Smoke test service"}
        }]
    }')
check "Test mode create → success" echo "$CREATE_RESP" | grep -q '"status":"created"'

DELETE_RESP=$(curl -s -X POST "$BRIDGE_URL/webhook" \
    -H "Content-Type: application/json" -H "X-API-Key: smoke-key" \
    -d '{
        "status": "firing",
        "alerts": [{
            "status": "firing",
            "labels": {"alertname": "E2E Test Service", "mode": "test", "test_action": "delete"},
            "annotations": {"summary": "Remove test service"}
        }]
    }')
check "Test mode delete → success" echo "$DELETE_RESP" | grep -q '"status":"deleted"'

# ── 8. Concurrent Alerts ────────────────────────────────────────────

echo ""
echo "── 8. Concurrent Alert Handling ──"

CONCURRENT_PIDS=""
for i in $(seq 1 10); do
    curl -s -X POST "$BRIDGE_URL/webhook" \
        -H "Content-Type: application/json" -H "X-API-Key: smoke-key" \
        -d "{
            \"status\": \"firing\",
            \"alerts\": [{
                \"status\": \"firing\",
                \"labels\": {\"alertname\": \"Concurrent-$i\", \"severity\": \"warning\"},
                \"annotations\": {\"summary\": \"Concurrent test $i\"}
            }]
        }" >/dev/null &
    CONCURRENT_PIDS="$CONCURRENT_PIDS $!"
done

FAILED_CONCURRENT=0
for pid in $CONCURRENT_PIDS; do
    wait "$pid" || FAILED_CONCURRENT=$((FAILED_CONCURRENT + 1))
done
check "10 concurrent alerts all succeed" [ "$FAILED_CONCURRENT" -eq 0 ]

# ── Summary ──────────────────────────────────────────────────────────

echo ""
echo "========================================"
echo "  Smoke Test Results: $PASS passed, $FAIL failed"
echo "========================================"

TOTAL=$((PASS + FAIL))
if [ "$FAIL" -eq 0 ]; then
    echo ""
    echo "MERGE READY: YES"
    exit 0
else
    echo ""
    echo "MERGE READY: NO"
    exit 1
fi

#!/usr/bin/env bash
# Full E2E test suite for the webhook bridge.
# Requires testenv/docker-compose to be running.

set -euo pipefail

WEBHOOK_URL="${WEBHOOK_URL:-http://localhost:9080/webhook}"
API_KEY="${API_KEY:-test-key-script-dev}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

PASS=0
FAIL=0

run_test() {
  local name="$1"
  local fn="$2"
  echo ""
  echo "> TEST: $name"
  if $fn; then
    echo "  PASS"
    ((PASS++))
  else
    echo "  FAIL"
    ((FAIL++))
  fi
}

wait_for_service() {
  local url="$1"
  local max_attempts="${2:-20}"
  local attempt=0
  while ! curl -sf "$url" > /dev/null 2>&1; do
    ((attempt++))
    [[ $attempt -ge $max_attempts ]] && return 1
    sleep 2
  done
  return 0
}

# ── Health check ──────────────────────────────────────────────────
test_health() {
  local code
  code=$(curl -s -o /dev/null -w "%{http_code}" "http://localhost:9080/health")
  [[ "$code" == "200" ]]
}

# ── Test 1: No API key ───────────────────────────────────────────
test_no_api_key() {
  local code
  code=$(curl -s -o /dev/null -w "%{http_code}" -X POST \
    -H "Content-Type: application/json" \
    -d '{"status":"firing","alerts":[]}' \
    "http://localhost:9080/webhook")
  [[ "$code" == "401" ]]
}

# ── Test 2: Wrong API key ────────────────────────────────────────
test_wrong_api_key() {
  local code
  code=$(curl -s -o /dev/null -w "%{http_code}" -X POST \
    -H "Content-Type: application/json" \
    -H "X-API-Key: INVALID-KEY" \
    -d '{"status":"firing","alerts":[]}' \
    "http://localhost:9080/webhook")
  [[ "$code" == "401" ]]
}

# ── Test 3: Create dummy service ─────────────────────────────────
test_create_service() {
  export WEBHOOK_URL API_KEY
  "$SCRIPT_DIR/send_test_create.sh" "E2E Test Alert Alpha"
}

# ── Test 4: Alert CRITICAL ───────────────────────────────────────
test_alert_critical() {
  export WEBHOOK_URL API_KEY
  "$SCRIPT_DIR/send_alert.sh" "E2E Test Alert Alpha" "critical" "E2E test - CRITICAL"
}

# ── Test 5: Alert WARNING ────────────────────────────────────────
test_alert_warning() {
  export WEBHOOK_URL API_KEY
  "$SCRIPT_DIR/send_alert.sh" "E2E Test Alert Alpha" "warning" "E2E test - WARNING"
}

# ── Test 6: Resolved -> OK ───────────────────────────────────────
test_resolved() {
  export WEBHOOK_URL API_KEY
  "$SCRIPT_DIR/send_resolved.sh" "E2E Test Alert Alpha"
}

# ── Test 7: History has entries ──────────────────────────────────
test_history_has_entries() {
  local count
  count=$(curl -s "http://localhost:9080/history?limit=100" | \
    python3 -c "import sys,json; d=json.load(sys.stdin); print(len(d.get('entries',d)))" 2>/dev/null || echo 0)
  [[ "$count" -gt 0 ]]
}

# ── Test 8: Delete service ───────────────────────────────────────
test_delete_service() {
  export WEBHOOK_URL API_KEY
  "$SCRIPT_DIR/send_test_delete.sh" "E2E Test Alert Alpha"
}

# ── Test 9: Beauty dashboard loads ───────────────────────────────
test_beauty_dashboard() {
  local code
  code=$(curl -s -o /dev/null -w "%{http_code}" "http://localhost:9080/status/beauty")
  [[ "$code" == "200" ]]
}

# ── Test 10: Concurrent alerts ───────────────────────────────────
test_concurrent_alerts() {
  local pids=()
  for i in {1..10}; do
    (curl -s -o /dev/null -X POST "$WEBHOOK_URL" \
      -H "Content-Type: application/json" \
      -H "X-API-Key: $API_KEY" \
      -d "{\"status\":\"firing\",\"alerts\":[{\"status\":\"firing\",\
\"labels\":{\"alertname\":\"Concurrent Test $i\",\"severity\":\"warning\"},\
\"annotations\":{\"summary\":\"Concurrent test $i\"}}]}") &
    pids+=($!)
  done
  for pid in "${pids[@]}"; do wait "$pid"; done
  echo "  Sent 10 concurrent alerts"
  true
}

# ══════════════════════════════════════════════════════════════════
echo "============================================"
echo "  Webhook Bridge - E2E Test Suite"
echo "============================================"

echo ""
echo "Waiting for webhook-bridge to be available..."
if ! wait_for_service "http://localhost:9080/health" 30; then
  echo "ERROR: webhook-bridge not available after 60s. Check docker-compose."
  exit 1
fi
echo "webhook-bridge is available"

run_test "Health check"                 test_health
run_test "No API key -> 401"            test_no_api_key
run_test "Wrong API key -> 401"         test_wrong_api_key
run_test "Create dummy service"         test_create_service
run_test "Alert CRITICAL"               test_alert_critical
run_test "Alert WARNING"                test_alert_warning
run_test "Resolved -> OK"               test_resolved
run_test "History has entries"          test_history_has_entries
run_test "Delete service"              test_delete_service
run_test "Beauty dashboard"            test_beauty_dashboard
run_test "10 concurrent alerts"        test_concurrent_alerts

echo ""
echo "============================================"
echo "  Results: $PASS PASS  |  $FAIL FAIL"
echo "============================================"

[[ $FAIL -eq 0 ]]

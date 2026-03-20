#!/usr/bin/env bash
# ══════════════════════════════════════════════════════════════════════
#  IcingaAlertForge - Load Test Suite
#  Sends series of 1, 10, 100, 500, 1000 alerts and measures:
#   - Webhook response time
#   - Icinga2 service creation/update latency
#   - Error rates
# ══════════════════════════════════════════════════════════════════════
set -euo pipefail

WEBHOOK_URL="${WEBHOOK_URL:-http://localhost:9080/webhook}"
API_KEY="${API_KEY:-test-key-script-dev}"
ICINGA2_URL="${ICINGA2_URL:-https://localhost:5665}"
ICINGA2_USER="${ICINGA2_USER:-apiuser}"
ICINGA2_PASS="${ICINGA2_PASS:-apipassword}"
ICINGA2_HOST="${ICINGA2_HOST_NAME:-test-host}"
CONCURRENCY="${CONCURRENCY:-10}"
RESULTS_DIR="${RESULTS_DIR:-/tmp/loadtest_results}"

mkdir -p "$RESULTS_DIR"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

log()  { echo -e "${CYAN}[$(date +%H:%M:%S)]${NC} $*"; }
ok()   { echo -e "${GREEN}[OK]${NC} $*"; }
warn() { echo -e "${YELLOW}[WARN]${NC} $*"; }
err()  { echo -e "${RED}[ERROR]${NC} $*"; }
header() {
  echo ""
  echo -e "${BOLD}${BLUE}══════════════════════════════════════════════════════════════${NC}"
  echo -e "${BOLD}${BLUE}  $*${NC}"
  echo -e "${BOLD}${BLUE}══════════════════════════════════════════════════════════════${NC}"
}

# ── Helpers ────────────────────────────────────────────────────────────

now_ms() {
  python3 -c "import time; print(int(time.time()*1000))"
}

# Send a single alert and return HTTP code + response time in ms
send_alert() {
  local name="$1"
  local severity="$2"
  local message="$3"
  local now
  now=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

  curl -s -o /dev/null -w "%{http_code} %{time_total}" \
    -X POST "$WEBHOOK_URL" \
    -H "Content-Type: application/json" \
    -H "X-API-Key: $API_KEY" \
    -d "{
      \"status\": \"firing\",
      \"alerts\": [{
        \"status\": \"firing\",
        \"labels\": {
          \"alertname\": \"$name\",
          \"severity\": \"$severity\"
        },
        \"annotations\": {
          \"summary\": \"$message\"
        },
        \"startsAt\": \"$now\",
        \"endsAt\": \"0001-01-01T00:00:00Z\"
      }]
    }"
}

# Send a resolved alert
send_resolved() {
  local name="$1"
  local now
  now=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

  curl -s -o /dev/null -w "%{http_code} %{time_total}" \
    -X POST "$WEBHOOK_URL" \
    -H "Content-Type: application/json" \
    -H "X-API-Key: $API_KEY" \
    -d "{
      \"status\": \"resolved\",
      \"alerts\": [{
        \"status\": \"resolved\",
        \"labels\": {
          \"alertname\": \"$name\",
          \"severity\": \"warning\"
        },
        \"annotations\": {
          \"summary\": \"Resolved\"
        },
        \"startsAt\": \"$now\",
        \"endsAt\": \"$now\"
      }]
    }"
}

# Check if a service exists in Icinga2
check_icinga_service() {
  local svc_name="$1"
  curl -s -k -u "${ICINGA2_USER}:${ICINGA2_PASS}" \
    "${ICINGA2_URL}/v1/objects/services/${ICINGA2_HOST}!${svc_name}" 2>/dev/null | \
    python3 -c "import sys,json; d=json.load(sys.stdin); print(len(d.get('results',[])))" 2>/dev/null || echo "0"
}

# Count all services on the host in Icinga2
count_icinga_services() {
  curl -s -k -u "${ICINGA2_USER}:${ICINGA2_PASS}" \
    "${ICINGA2_URL}/v1/objects/services?filter=host.name==%22${ICINGA2_HOST}%22" 2>/dev/null | \
    python3 -c "import sys,json; d=json.load(sys.stdin); print(len(d.get('results',[])))" 2>/dev/null || echo "0"
}

# Delete a service from Icinga2 (cleanup)
delete_icinga_service() {
  local svc_name="$1"
  curl -s -k -u "${ICINGA2_USER}:${ICINGA2_PASS}" \
    -X DELETE \
    "${ICINGA2_URL}/v1/objects/services/${ICINGA2_HOST}!${svc_name}?cascade=1" \
    -o /dev/null 2>/dev/null
}

# Wait for a service to appear in Icinga2
wait_for_icinga_service() {
  local svc_name="$1"
  local timeout="${2:-30}"
  local start
  start=$(now_ms)
  local elapsed=0

  while [[ $elapsed -lt $((timeout * 1000)) ]]; do
    local count
    count=$(check_icinga_service "$svc_name")
    if [[ "$count" -gt 0 ]]; then
      local end
      end=$(now_ms)
      echo $((end - start))
      return 0
    fi
    sleep 0.2
    elapsed=$(( $(now_ms) - start ))
  done
  echo "-1"
  return 1
}

# ── Pre-flight checks ────────────────────────────────────────────────

preflight() {
  header "PRE-FLIGHT CHECKS"

  log "Checking webhook-bridge health..."
  local code
  code=$(curl -s -o /dev/null -w "%{http_code}" "${WEBHOOK_URL%/webhook}/health" 2>/dev/null || echo "000")
  if [[ "$code" != "200" ]]; then
    err "Webhook bridge not available (HTTP $code). Start testenv first:"
    echo "  cd testenv && docker compose up -d"
    exit 1
  fi
  ok "Webhook bridge is up"

  log "Checking Icinga2 API..."
  code=$(curl -s -k -o /dev/null -w "%{http_code}" -u "${ICINGA2_USER}:${ICINGA2_PASS}" \
    "${ICINGA2_URL}/v1/status" 2>/dev/null || echo "000")
  if [[ "$code" != "200" ]]; then
    err "Icinga2 API not available (HTTP $code)"
    exit 1
  fi
  ok "Icinga2 API is up"

  local svc_count
  svc_count=$(count_icinga_services)
  log "Current services on '${ICINGA2_HOST}': ${svc_count}"
}

# ── Cleanup helper ───────────────────────────────────────────────────

cleanup_test_services() {
  local prefix="$1"
  local count="$2"
  log "Cleaning up ${count} test services (prefix: ${prefix})..."

  local cleaned=0
  for i in $(seq 1 "$count"); do
    local svc_name="${prefix}-${i}"
    delete_icinga_service "$svc_name" &
    ((cleaned++))
    # batch deletes to not overwhelm
    if (( cleaned % 20 == 0 )); then
      wait
    fi
  done
  wait
  ok "Cleanup done ($cleaned services)"
}

# ══════════════════════════════════════════════════════════════════════
#  TEST SERIES
# ══════════════════════════════════════════════════════════════════════

run_series() {
  local count="$1"
  local prefix="LT-$(date +%s)-${count}"
  local severities=("critical" "warning")
  local results_file="${RESULTS_DIR}/series_${count}.csv"

  header "SERIES: ${count} ALERT(S)  [prefix: ${prefix}]"

  echo "index,alert_name,severity,http_code,response_time_s,icinga_latency_ms" > "$results_file"

  local total_start
  total_start=$(now_ms)
  local http_ok=0
  local http_fail=0
  local total_response_time=0
  local pids=()
  local tmpdir
  tmpdir=$(mktemp -d)

  # ── Send alerts ──
  log "Sending ${count} alerts (concurrency: ${CONCURRENCY})..."

  local batch_start
  batch_start=$(now_ms)
  local active=0

  for i in $(seq 1 "$count"); do
    local sev="${severities[$(( (i - 1) % 2 ))]}"
    local alert_name="${prefix}-${i}"
    local msg="Load test alert ${i}/${count} - ${sev}"

    (
      result=$(send_alert "$alert_name" "$sev" "$msg")
      echo "$result" > "${tmpdir}/result_${i}.txt"
    ) &
    pids+=($!)
    ((active++))

    # Throttle concurrency
    if (( active >= CONCURRENCY )); then
      # Wait for oldest
      wait "${pids[0]}" 2>/dev/null || true
      pids=("${pids[@]:1}")
      ((active--))
    fi
  done

  # Wait for remaining
  for pid in "${pids[@]}"; do
    wait "$pid" 2>/dev/null || true
  done

  local batch_end
  batch_end=$(now_ms)
  local send_duration=$(( batch_end - batch_start ))

  # ── Collect send results ──
  for i in $(seq 1 "$count"); do
    local sev="${severities[$(( (i - 1) % 2 ))]}"
    local alert_name="${prefix}-${i}"

    if [[ -f "${tmpdir}/result_${i}.txt" ]]; then
      local result
      result=$(cat "${tmpdir}/result_${i}.txt")
      local code
      code=$(echo "$result" | awk '{print $1}')
      local rtime
      rtime=$(echo "$result" | awk '{print $2}')

      if [[ "$code" == "200" ]]; then
        ((http_ok++))
      else
        ((http_fail++))
      fi

      # Accumulate response time (convert to ms)
      local rtime_ms
      rtime_ms=$(python3 -c "print(int(float('${rtime}') * 1000))" 2>/dev/null || echo "0")
      total_response_time=$((total_response_time + rtime_ms))

      echo "${i},${alert_name},${sev},${code},${rtime}," >> "$results_file"
    else
      ((http_fail++))
      echo "${i},${alert_name},${sev},000,0," >> "$results_file"
    fi
  done

  rm -rf "$tmpdir"

  # ── Measure Icinga2 propagation delay ──
  log "Checking Icinga2 propagation (sampling up to 5 services)..."

  local icinga_latencies=()
  local sample_size=$((count < 5 ? count : 5))
  local sample_step=$((count / sample_size))
  [[ $sample_step -lt 1 ]] && sample_step=1

  local check_start
  check_start=$(now_ms)

  for s in $(seq 1 "$sample_size"); do
    local idx=$(( s * sample_step ))
    [[ $idx -gt $count ]] && idx=$count
    local svc_name="${prefix}-${idx}"

    local latency
    latency=$(wait_for_icinga_service "$svc_name" 60)
    if [[ "$latency" != "-1" ]]; then
      icinga_latencies+=("$latency")
      ok "Service '${svc_name}' appeared in Icinga2 after ${latency}ms"
    else
      warn "Service '${svc_name}' NOT found in Icinga2 within 60s"
      icinga_latencies+=("-1")
    fi
  done

  # ── Wait for all services to propagate ──
  if [[ $count -gt 5 ]]; then
    log "Waiting for all ${count} services to appear in Icinga2..."
    local wait_start
    wait_start=$(now_ms)
    local target_count=$((count))
    local max_wait=120  # 2 minutes
    local current=0
    local prev=0

    while true; do
      current=$(count_icinga_services)
      local elapsed_s=$(( ($(now_ms) - wait_start) / 1000 ))

      if [[ $current -ne $prev ]]; then
        log "  Icinga2 services: ${current} (elapsed: ${elapsed_s}s)"
        prev=$current
      fi

      # We can't easily know pre-existing count, so check if we have at least count
      if [[ $elapsed_s -ge $max_wait ]]; then
        warn "Timeout after ${max_wait}s. Services in Icinga2: ${current}"
        break
      fi

      # Check if we seem to have them all by sampling the last one
      local last_svc="${prefix}-${count}"
      local last_check
      last_check=$(check_icinga_service "$last_svc")
      if [[ "$last_check" -gt 0 ]]; then
        local total_propagation=$(( $(now_ms) - batch_start ))
        ok "All services confirmed in Icinga2 (total: ${total_propagation}ms)"
        break
      fi

      sleep 1
    done
  fi

  local total_end
  total_end=$(now_ms)
  local total_duration=$(( total_end - total_start ))

  # ── Calculate stats ──
  local avg_response=0
  if [[ $http_ok -gt 0 ]]; then
    avg_response=$((total_response_time / (http_ok + http_fail) ))
  fi

  local avg_icinga_latency=0
  local valid_latencies=0
  for lat in "${icinga_latencies[@]}"; do
    if [[ "$lat" != "-1" ]]; then
      avg_icinga_latency=$((avg_icinga_latency + lat))
      ((valid_latencies++))
    fi
  done
  if [[ $valid_latencies -gt 0 ]]; then
    avg_icinga_latency=$((avg_icinga_latency / valid_latencies))
  fi

  local min_icinga=999999
  local max_icinga=0
  for lat in "${icinga_latencies[@]}"; do
    if [[ "$lat" != "-1" ]]; then
      [[ $lat -lt $min_icinga ]] && min_icinga=$lat
      [[ $lat -gt $max_icinga ]] && max_icinga=$lat
    fi
  done
  [[ $min_icinga -eq 999999 ]] && min_icinga=0

  # ── Print report ──
  echo ""
  echo -e "${BOLD}  RESULTS: ${count} ALERTS${NC}"
  echo "  ─────────────────────────────────────────"
  echo -e "  Webhook HTTP 200:      ${GREEN}${http_ok}${NC}"
  echo -e "  Webhook HTTP errors:   ${RED}${http_fail}${NC}"
  echo -e "  Send phase duration:   ${CYAN}${send_duration}ms${NC}"
  echo -e "  Avg response time:     ${CYAN}${avg_response}ms${NC}"
  echo -e "  Icinga2 latency (avg): ${YELLOW}${avg_icinga_latency}ms${NC}"
  echo -e "  Icinga2 latency (min): ${YELLOW}${min_icinga}ms${NC}"
  echo -e "  Icinga2 latency (max): ${YELLOW}${max_icinga}ms${NC}"
  echo -e "  Total duration:        ${BOLD}${total_duration}ms${NC}"
  echo "  ─────────────────────────────────────────"

  # Save summary
  cat >> "${RESULTS_DIR}/summary.txt" <<EOSUMMARY
--- Series: ${count} alerts ---
  Send duration:     ${send_duration}ms
  HTTP OK:           ${http_ok}
  HTTP Errors:       ${http_fail}
  Avg response:      ${avg_response}ms
  Icinga2 avg lat:   ${avg_icinga_latency}ms
  Icinga2 min lat:   ${min_icinga}ms
  Icinga2 max lat:   ${max_icinga}ms
  Total duration:    ${total_duration}ms

EOSUMMARY

  # ── Cleanup ──
  log "Cleaning up test services..."
  cleanup_test_services "$prefix" "$count"

  # Small pause between series
  if [[ $count -lt 1000 ]]; then
    log "Pause 5s before next series..."
    sleep 5
  fi
}

# ══════════════════════════════════════════════════════════════════════
#  MAIN
# ══════════════════════════════════════════════════════════════════════

main() {
  header "IcingaAlertForge - LOAD TEST SUITE"
  echo -e "  Webhook:     ${WEBHOOK_URL}"
  echo -e "  Icinga2:     ${ICINGA2_URL}"
  echo -e "  Host:        ${ICINGA2_HOST}"
  echo -e "  Concurrency: ${CONCURRENCY}"
  echo -e "  Results:     ${RESULTS_DIR}"

  preflight

  # Initialize summary
  echo "IcingaAlertForge Load Test - $(date)" > "${RESULTS_DIR}/summary.txt"
  echo "======================================" >> "${RESULTS_DIR}/summary.txt"
  echo "" >> "${RESULTS_DIR}/summary.txt"

  # Run test series
  run_series 1
  run_series 10
  run_series 100
  run_series 500
  run_series 1000

  # ── Final report ──
  header "FINAL REPORT"
  cat "${RESULTS_DIR}/summary.txt"

  local final_svc_count
  final_svc_count=$(count_icinga_services)
  echo ""
  echo -e "  Services remaining on '${ICINGA2_HOST}': ${final_svc_count}"
  echo -e "  CSV files saved to: ${RESULTS_DIR}/"
  echo ""
  ok "Load test complete!"
}

main "$@"

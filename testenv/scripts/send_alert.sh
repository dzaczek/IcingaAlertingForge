#!/usr/bin/env bash
# Simulates a firing alert from Grafana (WARNING or CRITICAL).
# Usage: ./send_alert.sh "Alert Name" critical "Description of the problem"

set -euo pipefail

WEBHOOK_URL="${WEBHOOK_URL:-http://localhost:9080/webhook}"
API_KEY="${API_KEY:-test-key-script-dev}"
ALERT_NAME="${1:-Test Alert Default}"
SEVERITY="${2:-critical}"
SUMMARY="${3:-Problem detected by test script}"

if [[ "$SEVERITY" != "critical" && "$SEVERITY" != "warning" ]]; then
  echo "ERROR: Severity must be 'critical' or 'warning'"
  exit 1
fi

echo "Sending FIRING alert"
echo "  Alert:    $ALERT_NAME"
echo "  Severity: $SEVERITY"
echo "  Message:  $SUMMARY"

NOW=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

RESPONSE=$(curl -s -w "\n%{http_code}" -X POST "$WEBHOOK_URL" \
  -H "Content-Type: application/json" \
  -H "X-API-Key: $API_KEY" \
  -d "{
    \"status\": \"firing\",
    \"alerts\": [
      {
        \"status\": \"firing\",
        \"labels\": {
          \"alertname\": \"$ALERT_NAME\",
          \"severity\": \"$SEVERITY\"
        },
        \"annotations\": {
          \"summary\": \"$SUMMARY\"
        },
        \"startsAt\": \"$NOW\",
        \"endsAt\": \"0001-01-01T00:00:00Z\"
      }
    ]
  }")

HTTP_CODE=$(echo "$RESPONSE" | tail -n1)
BODY=$(echo "$RESPONSE" | head -n-1)

echo "  HTTP status: $HTTP_CODE"
echo "  Response: $BODY"

if [[ "$HTTP_CODE" == "200" ]]; then
  echo "OK: Alert FIRING sent - Icinga2 should show $SEVERITY"
else
  echo "ERROR: HTTP $HTTP_CODE"
  exit 1
fi

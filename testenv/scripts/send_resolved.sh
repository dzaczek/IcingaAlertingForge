#!/usr/bin/env bash
# Simulates a resolved (back to OK) alert from Grafana.
# Usage: ./send_resolved.sh "Alert Name"
#
# Use the same ALERT_SUMMARY as for send_alert.sh so the resolve reaches the
# same Icinga2 service.

set -euo pipefail

WEBHOOK_URL="${WEBHOOK_URL:-http://localhost:9080/webhook}"
API_KEY="${API_KEY:-test-key-script-dev}"
ALERT_NAME="${1:-Test Alert Default}"
ALERT_SUMMARY="${ALERT_SUMMARY:-}"

ANNOTATIONS="\"description\": \"Problem resolved - back to OK\""
if [[ -n "$ALERT_SUMMARY" ]]; then
  ANNOTATIONS="\"summary\": \"$ALERT_SUMMARY\", $ANNOTATIONS"
fi

echo "Sending RESOLVED for alert: '$ALERT_NAME'"

NOW=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
STARTED=$(date -u -v-10M +"%Y-%m-%dT%H:%M:%SZ" 2>/dev/null || \
          date -u -d "-10 minutes" +"%Y-%m-%dT%H:%M:%SZ")

RESPONSE=$(curl -s -w "\n%{http_code}" -X POST "$WEBHOOK_URL" \
  -H "Content-Type: application/json" \
  -H "X-API-Key: $API_KEY" \
  -d "{
    \"status\": \"resolved\",
    \"alerts\": [
      {
        \"status\": \"resolved\",
        \"labels\": {
          \"alertname\": \"$ALERT_NAME\",
          \"severity\": \"critical\"
        },
        \"annotations\": {
          $ANNOTATIONS
        },
        \"startsAt\": \"$STARTED\",
        \"endsAt\": \"$NOW\"
      }
    ]
  }")

HTTP_CODE=$(echo "$RESPONSE" | tail -n1)
BODY=$(echo "$RESPONSE" | head -n-1)

echo "  HTTP status: $HTTP_CODE"
echo "  Response: $BODY"

if [[ "$HTTP_CODE" == "200" ]]; then
  echo "OK: Resolved sent - Icinga2 should show OK (green)"
else
  echo "ERROR: HTTP $HTTP_CODE"
  exit 1
fi

#!/usr/bin/env bash
# Deletes a dummy service from Icinga2 via the webhook bridge.
# Usage: ./send_test_delete.sh "Alert Name"

set -euo pipefail

WEBHOOK_URL="${WEBHOOK_URL:-http://localhost:9080/webhook}"
API_KEY="${API_KEY:-test-key-script-dev}"
ALERT_NAME="${1:-Test Alert Default}"

echo "Deleting dummy service: '$ALERT_NAME'"

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
          \"mode\": \"test\",
          \"test_action\": \"delete\"
        },
        \"annotations\": {
          \"summary\": \"Test dummy service deletion via script\"
        }
      }
    ]
  }")

HTTP_CODE=$(echo "$RESPONSE" | tail -n1)
BODY=$(echo "$RESPONSE" | head -n-1)

echo "  HTTP status: $HTTP_CODE"
echo "  Response: $BODY"

if [[ "$HTTP_CODE" == "200" || "$HTTP_CODE" == "202" ]]; then
  echo "OK: Service '$ALERT_NAME' - delete request sent"
else
  echo "ERROR: HTTP $HTTP_CODE"
  exit 1
fi

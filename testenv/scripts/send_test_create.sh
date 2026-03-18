#!/usr/bin/env bash
# Creates a dummy service in Icinga2 via the webhook bridge.
# Usage: ./send_test_create.sh "Alert Name"

set -euo pipefail

WEBHOOK_URL="${WEBHOOK_URL:-http://localhost:9080/webhook}"
API_KEY="${API_KEY:-test-key-script-dev}"
ALERT_NAME="${1:-Test Alert Default}"

echo "Creating dummy service: '$ALERT_NAME'"
echo "  URL: $WEBHOOK_URL"

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
          \"test_action\": \"create\"
        },
        \"annotations\": {
          \"summary\": \"Test dummy service creation via script\"
        }
      }
    ]
  }")

HTTP_CODE=$(echo "$RESPONSE" | tail -n1)
BODY=$(echo "$RESPONSE" | head -n-1)

echo "  HTTP status: $HTTP_CODE"
echo "  Response: $BODY"

if [[ "$HTTP_CODE" == "200" || "$HTTP_CODE" == "202" ]]; then
  echo "OK: Service '$ALERT_NAME' - request sent successfully"
else
  echo "ERROR: HTTP $HTTP_CODE"
  exit 1
fi

#!/usr/bin/env bash
# Check webhook history entries.
# Usage: ./check_history.sh [service_name] [limit]

set -euo pipefail

BASE_URL="${WEBHOOK_URL:-http://localhost:9080}"
SERVICE="${1:-}"
LIMIT="${2:-20}"

URL="$BASE_URL/history?limit=$LIMIT"
[[ -n "$SERVICE" ]] && URL="$URL&service=$(python3 -c "import urllib.parse,sys; print(urllib.parse.quote(sys.argv[1]))" "$SERVICE")"

echo "Webhook history (last $LIMIT entries)"
[[ -n "$SERVICE" ]] && echo "  Filter: service = '$SERVICE'"
echo "  URL: $URL"
echo ""

curl -s "$URL" | python3 -c "
import sys, json
data = json.load(sys.stdin)
entries = data.get('entries', data) if isinstance(data, dict) else data
for e in entries:
    ts = e.get('timestamp','?')[:19]
    svc = e.get('service_name','?')
    mode = e.get('mode','?')
    action = e.get('action','?')
    status_map = {0:'OK  ', 1:'WARN', 2:'CRIT'}
    es = e.get('exit_status', -1)
    status = status_map.get(es, '?   ')
    src = e.get('source_key','?')
    ok = 'OK' if e.get('icinga_ok') else 'FAIL'
    print(f'{ts} | {ok:4} | {status} | {mode:6} | {action:8} | [{src}] {svc}')
"

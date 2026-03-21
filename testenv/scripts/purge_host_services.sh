#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${BASE_URL:-http://localhost:9080}"
ADMIN_USER="${ADMIN_USER:-admin}"
ADMIN_PASS="${ADMIN_PASS:-admin123}"

apply=0
mode="managed"
pattern=""

usage() {
  cat <<'EOF'
Usage:
  purge_host_services.sh [--apply] [--managed | --all | --regex PATTERN]

Modes:
  --managed         services tagged managed_by=IcingaAlertingForge or legacy webhook-bridge
  --all             all services on the configured host
  --regex PATTERN   services whose name matches PATTERN

Default mode is dry-run + --managed.
Examples:
  ./purge_host_services.sh --regex '^Synthetic Device'
  ./purge_host_services.sh --apply --regex '^Synthetic Device'
  ./purge_host_services.sh --apply --all
EOF
}

while [ $# -gt 0 ]; do
  case "$1" in
    --apply)
      apply=1
      ;;
    --managed)
      mode="managed"
      ;;
    --all)
      mode="all"
      ;;
    --regex)
      mode="regex"
      shift
      pattern="${1:-}"
      if [ -z "$pattern" ]; then
        echo "Missing PATTERN after --regex" >&2
        exit 1
      fi
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown argument: $1" >&2
      usage
      exit 1
      ;;
  esac
  shift
done

tmp_json="$(mktemp)"
tmp_names="$(mktemp)"
cleanup() {
  rm -f "$tmp_json" "$tmp_names"
}
trap cleanup EXIT

curl -fsS -u "${ADMIN_USER}:${ADMIN_PASS}" "${BASE_URL}/admin/services" > "$tmp_json"

case "$mode" in
  managed)
    jq -r '.services[] | select(.managed_by == "webhook-bridge" or .managed_by == "IcingaAlertingForge") | .name' "$tmp_json" > "$tmp_names"
    ;;
  all)
    jq -r '.services[] | .name' "$tmp_json" > "$tmp_names"
    ;;
  regex)
    jq -r --arg re "$pattern" '.services[] | select(.name | test($re)) | .name' "$tmp_json" > "$tmp_names"
    ;;
esac

count="$(wc -l < "$tmp_names" | tr -d ' ')"
echo "Matched services: ${count}"
if [ "$count" -eq 0 ]; then
  exit 0
fi

sed -n '1,30p' "$tmp_names"
if [ "$count" -gt 30 ]; then
  echo "... truncated ..."
fi

if [ "$apply" -ne 1 ]; then
  echo "Dry-run only. Re-run with --apply to delete."
  exit 0
fi

jq -Rs 'split("\n") | map(select(length > 0)) | {services: .}' "$tmp_names" | \
  curl -fsS -u "${ADMIN_USER}:${ADMIN_PASS}" \
    -H 'Content-Type: application/json' \
    -X POST "${BASE_URL}/admin/services/bulk-delete" \
    -d @- | jq '{deleted: [.results[] | select(.status == "deleted")] | length, errors: [.results[] | select(.status != "deleted")] | length}'

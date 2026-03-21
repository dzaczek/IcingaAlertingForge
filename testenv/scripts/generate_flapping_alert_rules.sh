#!/usr/bin/env bash
set -euo pipefail

count="${1:-50}"
output="${2:-testenv/grafana/provisioning/alerting/flapping-device-rules.yml}"
datasource_uid="${DATASOURCE_UID:-prometheus-iaf-testenv}"
group_name="${GROUP_NAME:-synthetic-flapping-devices}"
folder_name="${FOLDER_NAME:-Test Alerts}"
interval="${INTERVAL:-10s}"

mkdir -p "$(dirname "$output")"

{
  cat <<EOF
apiVersion: 1

groups:
  - orgId: 1
    name: ${group_name}
    folder: ${folder_name}
    interval: ${interval}
    rules:
EOF

  i=1
  while [ "$i" -le "$count" ]; do
    idx="$(printf '%02d' "$i")"
    offset=$(( (i - 1) * 2 ))
    if [ $(( i % 2 )) -eq 0 ]; then
      severity="warning"
      state_label="WARNING"
    else
      severity="critical"
      state_label="CRITICAL"
    fi

    cat <<EOF
      - uid: flap_dev_${idx}
        title: "Synthetic Device ${idx}"
        condition: B
        data:
          - refId: A
            queryType: ""
            relativeTimeRange:
              from: 120
              to: 0
            datasourceUid: ${datasource_uid}
            model:
              editorMode: code
              expr: "vector((((time() + ${offset}) % 120) < bool 60) * 1)"
              instant: true
              intervalMs: 1000
              legendFormat: ""
              maxDataPoints: 43200
              range: false
              refId: A
          - refId: B
            queryType: ""
            relativeTimeRange:
              from: 120
              to: 0
            datasourceUid: __expr__
            model:
              expression: "\$A > 0"
              intervalMs: 1000
              maxDataPoints: 43200
              refId: B
              type: math
        dashboardUid: ""
        panelId: 0
        noDataState: NoData
        execErrState: Error
        for: 0s
        annotations:
          summary: "Lab Device ${idx} flips every 60s"
          description: "${state_label}/OK flapping test device with ${offset}s phase offset"
        labels:
          alertname: "Lab Device ${idx} Flap"
          severity: ${severity}
          device: "lab-device-${idx}"
          source: "synthetic-lab"
          pattern: "minute-flap"
        isPaused: false
EOF

    i=$(( i + 1 ))
  done
} > "$output"

echo "Generated ${count} alert rules in ${output}"

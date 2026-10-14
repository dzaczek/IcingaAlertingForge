# Prometheus Alertmanager Integration

Back to the [Documentation Index](../README.md)

This guide shows how to send alerts from Prometheus Alertmanager to Icinga2
through IcingaAlertForge (IAF), from the Prometheus alerting rule to the Icinga2
service.

```text
Prometheus rules ──► Alertmanager ──webhook──► IcingaAlertForge ──REST API──► Icinga2
```

## How It Works

1. Prometheus evaluates alerting rules and sends firing and resolved alerts to
   Alertmanager.
2. Alertmanager groups them and POSTs its standard webhook payload to
   `/webhook` on IAF.
3. IAF recognises the Alertmanager format automatically (the payload has
   `version`, `groupKey` or `receiver`). No extra setting is needed.
4. The API key picks the target Icinga2 host. Each alert in the payload is
   handled on its own:
   - the service is created on first use (passive, `dummy` check command)
   - a passive check result is sent with the state taken from the alert

| Alert `status` | `severity` label | Icinga2 state |
|---|---|---|
| `resolved` | any | `OK (0)` |
| `firing` | `warning` | `WARNING (1)` |
| `firing` | `critical` | `CRITICAL (2)` |
| `firing` | missing or other | `CRITICAL (2)` |

## Field Mapping

IAF uses the labels and annotations **of each individual alert**. The
group-level fields (`groupLabels`, `commonLabels`, `commonAnnotations`,
`externalURL`) are ignored.

| Alertmanager alert field | Used by IAF as |
|---|---|
| `labels.alertname` | **Required.** First part of the service name |
| `annotations.summary` | Second part of the service name: `<alertname> - <summary>` |
| `annotations.description` | Long output of the check result, updated on every notification |
| `labels.severity` | Icinga2 state (see table above) |
| `status` | `firing` or `resolved` |
| `annotations.runbook_url` / `dashboard_url` | `notes_url` of the service (set when the service is created) |
| `annotations.panel_url` | `action_url` of the service (set when the service is created) |
| all labels and annotations | stored on the service as `vars.grafana_label_<name>` and `vars.grafana_annotation_<name>` |

> The `grafana_` prefix in the custom variable names is historical. It is used
> for every source, Alertmanager included.

## Step 1: Get An API Key

Each API key belongs to one target, and the target decides which Icinga2 host
the services are created on. Generate a key for the target in the IAF
dashboard settings, or define one with environment variables
(`IAF_TARGET_<ID>_API_KEYS`, see [Configuration](configuration.md)).

Store the key in a file that only Alertmanager can read, for example:

```bash
install -m 0400 -o alertmanager -g alertmanager /dev/null /etc/alertmanager/iaf-key
printf '%s' 'YOUR_API_KEY' > /etc/alertmanager/iaf-key   # no trailing newline
```

On Kubernetes, put the key in a Secret and mount it into the Alertmanager pod.
Don't put it inline in `alertmanager.yml`.

## Step 2: Configure The Receiver

```yaml
# alertmanager.yml
route:
  receiver: default
  routes:
    - receiver: icinga
      matchers:
        - severity =~ "warning|critical"
      group_by: ['alertname', 'instance']
      group_wait: 30s
      group_interval: 5m
      repeat_interval: 4h
      continue: true          # keep notifying other receivers as well

receivers:
  - name: default
  - name: icinga
    webhook_configs:
      - url: https://iaf.example.com/webhook
        send_resolved: true   # required, otherwise services never return to OK
        max_alerts: 200       # keep payloads well below IAF's 1 MiB body limit
        http_config:
          authorization:
            type: ApiKey
            credentials_file: /etc/alertmanager/iaf-key
```

Notes:

- **Authentication:** Alertmanager sends `Authorization: ApiKey <key>`. IAF
  strips the scheme word and uses the rest as the key, so any `type` works.
  Don't use `basic_auth`: IAF would read the base64-encoded `user:password`
  as the key and reject it.
- **`send_resolved: true`** is the Alertmanager default for webhooks, but set
  it explicitly. Without resolved notifications the Icinga2 services stay in
  their problem state.
- **`group_by`** only controls how alerts are batched into one request. IAF
  handles every alert in the batch separately, so grouping does not merge
  services.
- **`repeat_interval`:** firing alerts are re-sent at this interval and update
  the existing services. They don't create new ones.
- **Several Icinga2 hosts:** use one receiver per target, each with its own
  API key, and route alerts to them with matchers.

Validate and reload:

```bash
amtool check-config /etc/alertmanager/alertmanager.yml
curl -X POST http://localhost:9093/-/reload
```

## Step 3: Write Prometheus Rules That Map Well

The Icinga2 service name is `<alertname> - <summary>`. Follow these rules:

1. **`alert` (alertname) is the rule name only**, never per-object data.
2. **`summary` identifies the affected object** (instance, mountpoint, job...)
   and contains **no measured values**.
3. **`description` carries everything that changes**: values, durations,
   counts, hints.
4. **`severity`** is `warning` or `critical`.

This matters more with Prometheus than with other sources. A resolved alert
carries the annotations from its **last** evaluation. If `summary` contains
`{{ $value }}`, the resolved notification can carry a different summary than
the firing one. It then lands on a **different** service, and the problem
service stays open.

```yaml
groups:
  - name: node
    rules:
      - alert: NodeDown
        expr: up{job="node"} == 0
        for: 5m
        labels:
          severity: critical
        annotations:
          summary: "{{ $labels.instance }}"
          description: "node_exporter has been unreachable for more than 5 minutes"
          runbook_url: https://wiki.example.com/runbooks/node-down

      - alert: DiskAlmostFull
        expr: node_filesystem_avail_bytes{fstype!~"tmpfs|overlay"} / node_filesystem_size_bytes < 0.10
        for: 15m
        labels:
          severity: warning
        annotations:
          summary: "{{ $labels.instance }}:{{ $labels.mountpoint }}"
          description: "{{ $value | humanizePercentage }} free on {{ $labels.device }}"
```

This produces one Icinga2 service per object:

```text
NodeDown - web-01:9100
DiskAlmostFull - web-01:9100:/data
DiskAlmostFull - db-01:9100:/var/lib/postgresql
```

| Good `summary` | Bad `summary` |
|---|---|
| `{{ $labels.instance }}` | `{{ $labels.instance }} is down for {{ $value }}s` |
| `{{ $labels.namespace }}/{{ $labels.pod }}` | `CPU at {{ $value \| humanize }}%` |
| `{{ $labels.instance }}:{{ $labels.mountpoint }}` | (empty, when the rule fires for many objects) |

Leaving `summary` empty is fine for rules that can only ever fire once (for
example a single cluster-wide check). The service is then named after the
alertname alone.

### Kubernetes Jobs Created By CronJobs

Every run of a CronJob creates a Job with a numeric suffix
(`backup-29706961`). Strip the suffix so all runs share one service:

```yaml
- alert: K8sJobActiveTooLong
  expr: |
    label_replace(
      kube_job_status_active > 0,
      "cronjob", "$1", "job_name", "(.+)-[0-9]+$"
    )
  for: 30m
  labels:
    severity: warning
  annotations:
    summary: "{{ $labels.cluster }}:{{ $labels.namespace }}-{{ or $labels.cronjob $labels.job_name }}"
    description: "job {{ $labels.job_name }} has been active for more than 30 minutes"
```

## Step 4: Test The Integration

Send a test payload in the Alertmanager format straight to IAF:

```bash
curl -sS -X POST https://iaf.example.com/webhook \
  -H "Content-Type: application/json" \
  -H "Authorization: ApiKey ${IAF_API_KEY}" \
  -d '{
    "version": "4",
    "groupKey": "{}:{alertname=\"IAFSmokeTest\"}",
    "status": "firing",
    "receiver": "icinga",
    "alerts": [{
      "status": "firing",
      "labels": {"alertname": "IAFSmokeTest", "severity": "warning", "instance": "test-01"},
      "annotations": {"summary": "test-01", "description": "manual test from curl"}
    }]
  }'
```

The expected response is HTTP 200 with one result per alert:

```json
{"results": [{"status": "processed", "service": "IAFSmokeTest - test-01", "label": "WARNING", "icinga_ok": true}]}
```

Send the same request with `"status": "resolved"` (in both places) to set the
service back to OK, then delete it from the IAF dashboard.

To test the whole path through Alertmanager, inject an alert with `amtool`:

```bash
amtool alert add IAFSmokeTest severity=warning instance=test-01 \
  --annotation=summary=test-01 --annotation=description="amtool test" \
  --alertmanager.url=http://localhost:9093
```

Allow for `group_wait` before the request reaches IAF.

## Responses, Retries And Limits

| HTTP status | Meaning | Alertmanager behaviour |
|---|---|---|
| `200` | all alerts were handled | done |
| `400` | invalid JSON, no alerts, or body over 1 MiB | not retried, so fix the payload or lower `max_alerts` |
| `401` | missing or wrong API key | not retried, so check the key file |
| `429` | per-IP ingress rate limit hit | raise `WEBHOOK_RATELIMIT_PER_SEC` / `WEBHOOK_RATELIMIT_BURST` if Alertmanager hits it |
| `502` | at least one alert in the request failed | retried by Alertmanager. IAF also queues failed check results in its own retry queue |

Each alert in a request is processed independently and reported in the
`results` array. One failing alert, for example one without an `alertname`
label, makes the whole response a `502`, and Alertmanager then re-sends the
whole batch. Re-sending is harmless (check results are idempotent), but fix
the rule so the batch stops failing.

## Troubleshooting

| Symptom | Likely cause |
|---|---|
| Nothing arrives in IAF | wrong URL (it must end in `/webhook`), firewall, or the route's matchers don't match. Check `amtool config routes test --config.file=alertmanager.yml severity=warning` |
| `401 unauthorized` | trailing newline or wrong key in `credentials_file`, or `basic_auth` used instead of `authorization` |
| Services never turn OK | `send_resolved: false`, or `summary` differs between firing and resolved (values in `summary`) |
| A new service appears on every evaluation | measured values or per-run suffixes in `summary` |
| All objects share one service | `summary` is empty or identical for every object. Add the identifying labels |
| Service shows `CRITICAL` for a warning | `severity` label missing or not exactly `warning` |

IAF logs every request (`Webhook received ... format=alertmanager`) and every
check result. Use `docker compose logs webhook-bridge` or the history view in
the dashboard to follow a single alert.

## See Also

- [Building Webhooks](webhook-integrations.md): naming rules shared by all sources
- [Configuration](configuration.md): targets, API keys, `ICINGA2_SERVICE_NAME_MODE`
- [Usage and API](usage-and-api.md): full `/webhook` response reference

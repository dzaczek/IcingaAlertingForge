# Building Webhooks For IcingaAlertForge

Back to the [Documentation Index](../README.md)

This guide explains how an incoming alert becomes an Icinga2 service and how to
build webhooks that behave well, whether they come from Grafana, Prometheus
Alertmanager, or any other tool that can send an HTTP POST.

## The Three Fields That Matter

Every alert, whatever its source, is reduced to the same internal shape. Three
fields decide what happens in Icinga2:

| Field | Purpose | Should it change between evaluations? |
|---|---|---|
| `alertname` | The rule or check name | **No** |
| `summary` | **Which object** is affected (host, job, disk, URL...) | **No** — identifiers only |
| `description` | **Current details**: values, durations, counts, hints | Yes, freely |

### How the service name is built

```text
service name = "<alertname> - <summary>"     (default: ICINGA2_SERVICE_NAME_MODE=alertname_summary)
service name = "<alertname>"                 (when summary is empty)
service name = "<alertname>"                 (legacy: ICINGA2_SERVICE_NAME_MODE=alertname)
```

> **Breaking change for custom senders:** firing and resolved notifications
> must carry the **same** `summary`. A sender that resolves with a different
> text (e.g. firing `Disk full on /data`, resolved `Disk back to normal`) now
> creates a second, OK service while the CRITICAL one stays open forever. Put
> such messages in `description`, or omit `summary` entirely so the service is
> named after the alertname. Grafana and Alertmanager re-render the same
> template on resolve, so a summary built from labels is safe.
>
> Test mode (`mode=test`) always uses the alertname only, so `create` and
> `delete` requests may carry different summaries.

The name is sanitized before use: `!` (the Icinga2 host/service separator) is
replaced with `_`, newlines and repeated whitespace collapse into one space,
and names longer than 200 bytes are truncated with a short hash suffix so they
stay unique.

### How the check output is built

```text
<STATE>: <summary>
<description>
```

Icinga2 shows the first line as the plugin output and the following lines as
long output. Because the description is **not** part of the service name, it
can change on every evaluation and simply updates the existing service.

### Why this split matters

One alert rule usually fires for many objects at once (one instance per job,
pod, disk...). Each instance must map to its own Icinga2 service, otherwise:

- a `resolved` from instance A turns the shared service OK while instance B is
  still firing (hidden problems, flapping);
- `notes`, `display_name` and `vars.grafana_label_*` are written only when the
  service is created, so they describe whichever instance came first.

Putting the object identity in `summary` gives every instance its own service.

> **Rule of thumb:** if a value in `summary` can change while the problem is
> still the same problem, move it to `description`. A summary like
> `CPU at {{ $values.A }}%` creates a new Icinga2 service on every evaluation.

| Good `summary` | Bad `summary` |
|---|---|
| `{{ $labels.instance }}` | `CPU at {{ $values.A }}%` |
| `{{ $labels.cluster }}:{{ $labels.namespace }}-{{ $labels.cronjob }}` | `{{ $labels.job_name }}` when it contains a per-run suffix |
| `web-01:/data` | `web-01 /data 91.3% used` |

Stale services left behind after renaming can be removed from the dashboard, or
pruned automatically once they are OK and idle with `SERVICE_PRUNE_AFTER_DAYS=<days>`
and `SERVICE_PRUNE_DRY_RUN=false` (dry-run, which only logs, is the default).

## Grafana Unified Alerting

Grafana is detected automatically (top-level `status` field). Configure the
rule's annotations:

```text
Summary:      {{ $labels.cluster }}:{{ $labels.namespace }}-{{ $labels.job_name }}
Description:  Job active for more than 30m (value: {{ $values.A }})
```

Add a `severity` label (`warning` or `critical`) on the rule. Point a webhook
contact point at `/webhook` with your API key (see
[Usage and API](usage-and-api.md#grafana-contact-points)).

### Example: Kubernetes jobs and CronJobs

Jobs created by a CronJob carry a per-run suffix (`backup-29706961`), so each
run would create a new service. Strip it in the query and use the resulting
label in the summary:

```promql
label_replace(
  kube_job_status_active{cluster=~".*", namespace=~".*", job_name=~".*"},
  "cronjob", "$1", "job_name", "(.+)-[0-9]+$"
)
```

```text
Summary:      {{ $labels.cluster }}:{{ $labels.namespace }}-{{ $labels.cronjob }}
Description:  job {{ $labels.job_name }} active for > 30m
```

Note that `label_replace` leaves `cronjob` empty for jobs without a numeric
suffix; use `{{ or $labels.cronjob $labels.job_name }}` if you mix both kinds.

## Prometheus Alertmanager

Alertmanager is detected automatically (`version`, `groupKey` or `receiver`
present). Labels and annotations are taken from each alert unchanged, so the
same rules apply: put identifiers in `annotations.summary` and changing values
in `annotations.description`.

```yaml
# alertmanager.yml
receivers:
  - name: icinga
    webhook_configs:
      - url: https://iaf.example.com/webhook
        http_config:
          authorization:
            type: ApiKey
            credentials_file: /etc/alertmanager/iaf-key
        send_resolved: true
```

```yaml
# Prometheus rule
- alert: DiskAlmostFull
  expr: node_filesystem_avail_bytes / node_filesystem_size_bytes < 0.1
  labels:
    severity: warning
  annotations:
    summary: "{{ $labels.instance }}:{{ $labels.mountpoint }}"
    description: "{{ $value | humanizePercentage }} free"
```

Keep `send_resolved: true`, otherwise services never return to OK.

## Any Other Tool: The Universal Format

For scripts, CI pipelines, cron jobs, uptime checkers or any tool with a
configurable HTTP webhook, use the universal format. It is detected when the
payload has `alerts` but no top-level `status`.

| Field | Maps to | Notes |
|---|---|---|
| `name` | `alertname` | Required |
| `status` | `firing` / `resolved` | Required |
| `severity` | `severity` label | `warning` or `critical` (default `critical`) |
| `message` | `summary` annotation | Identifies the object — keep it stable |
| `labels` | labels | Stored as `vars.grafana_label_*` |
| `annotations` | annotations | Put `description` here for changing details |

```bash
curl -X POST https://iaf.example.com/webhook \
  -H "Content-Type: application/json" \
  -H "X-API-Key: ${IAF_API_KEY}" \
  -d '{
    "alerts": [{
      "name": "BackupFailed",
      "status": "firing",
      "severity": "critical",
      "message": "db-01:nightly",
      "labels": {"team": "dba"},
      "annotations": {
        "description": "exit code 2 after 41m, last success 2 days ago",
        "runbook_url": "https://wiki.example.com/backup"
      }
    }]
  }'
```

This creates (once) the service `BackupFailed - db-01:nightly` and sets it to
CRITICAL with the description as long output. Send the same `name` and
`message` with `"status": "resolved"` to clear it.

### Example: a cron job wrapper

```bash
#!/usr/bin/env bash
set -euo pipefail

job="nightly"
started=$(date +%s)

if output=$(/usr/local/bin/backup.sh 2>&1); then
  status=resolved
else
  status=firing
fi

jq -n --arg status "$status" --arg job "$job" --arg host "$(hostname -s)" \
      --arg desc "took $(( $(date +%s) - started ))s: ${output: -200}" \
  '{alerts: [{name: "BackupFailed", status: $status, severity: "critical",
              message: ($host + ":" + $job),
              annotations: {description: $desc}}]}' |
curl -fsS -X POST https://iaf.example.com/webhook \
  -H "Content-Type: application/json" \
  -H "X-API-Key: ${IAF_API_KEY}" \
  --data-binary @-
```

## Optional Annotations

| Annotation | Effect in Icinga2 |
|---|---|
| `summary` | Part of the service name and first line of the output |
| `description` | Long output (updated on every alert) |
| `runbook_url` or `dashboard_url` | `notes_url` (set at service creation) |
| `panel_url` | `action_url` (set at service creation) |

All labels and annotations are also stored on the service as
`vars.grafana_label_<name>` and `vars.grafana_annotation_<name>` when it is
created.

## Checklist For A New Integration

1. `alertname` / `name` is the rule name and never contains per-object data.
2. `summary` / `message` identifies one object and contains no measured values.
3. Changing details go to `description`.
4. `severity` is `warning` or `critical`.
5. A `resolved` message with the same name and summary is sent when the problem
   clears.
6. Authentication uses `X-API-Key` or `Authorization: <scheme> <key>`.

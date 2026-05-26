# Observability

The bridge exposes metrics and structured logs for production monitoring.

## Metrics Endpoint

`GET /metrics` — Prometheus text format, auth-protected.

### Application Metrics (`iaf_*`)

| Metric | Type | Description |
|--------|------|-------------|
| `iaf_uptime_seconds` | Gauge | Process uptime |
| `iaf_goroutines` | Gauge | Active goroutines |
| `iaf_memory_alloc_bytes` | Gauge | Heap allocated |
| `iaf_memory_sys_bytes` | Gauge | OS memory |
| `iaf_memory_heap_bytes` | Gauge | Heap in use |
| `iaf_memory_stack_bytes` | Gauge | Stack in use |
| `iaf_gc_pause_total_seconds` | Counter | GC pause |
| `iaf_gc_runs_total` | Counter | GC cycles |

### Request Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `iaf_requests_total` | Counter | Total webhook requests |
| `iaf_errors_total` | Counter | Failed requests |
| `iaf_requests_per_minute` | Gauge | Rolling RPM |
| `iaf_request_latency_milliseconds` | Histogram | Request latency distribution (buckets: 10, 50, 100, 250, 500, 1000, 2500, 5000ms) |

### Per-Source Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `iaf_source_requests_total` | Counter | `source` | Requests per API key |
| `iaf_source_errors_total` | Counter | `source` | Errors per API key |
| `iaf_source_history_entries` | Gauge | `source` | History entries per source |
| `iaf_source_last_seen_timestamp` | Gauge | `source` | Last webhook timestamp |

### History Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `iaf_history_entries_total` | Gauge | | Total history entries |
| `iaf_history_by_mode` | Gauge | `mode` | Entry count per mode |
| `iaf_history_by_action` | Gauge | `action` | Entry count per action |
| `iaf_history_by_severity` | Gauge | `severity` | Count per severity |
| `iaf_history_by_severity_firing` | Gauge | `severity` | Firing-only per severity |
| `iaf_history_errors_total` | Gauge | | Icinga API failures |
| `iaf_history_avg_duration_milliseconds` | Gauge | | Avg Icinga API duration |

### Security Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `iaf_auth_failures_total` | Counter | Cumulative auth failures |
| `iaf_brute_force_ips_active` | Gauge | IPs flagged for brute-force |

### Queue Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `iaf_queue_depth` | Gauge | Queue items |
| `iaf_queue_max_size` | Gauge | Max capacity |
| `iaf_queue_retried_total` | Counter | Retry attempts |
| `iaf_queue_dropped_total` | Counter | Dropped items |
| `iaf_queue_failed_total` | Counter | Permanently failed |
| `iaf_queue_processing` | Gauge | 1 if processor active |

### Rate Limiter Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `iaf_ratelimiter_slots_in_use` | Gauge | `type` | Active slots |
| `iaf_ratelimiter_slots_max` | Gauge | `type` | Max slots |
| `iaf_ratelimiter_queue_depth` | Gauge | | Pending |
| `iaf_ratelimiter_queue_max` | Gauge | | Max queue |

### Health Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `iaf_health_icinga_up` | Gauge | 1 if Icinga2 reachable |
| `iaf_health_consecutive_failures` | Gauge | Consecutive health failures |

## Structured Logging

All logs use `log/slog` (Go 1.21+) with the JSON handler. Key fields:
- `request_id` — UUID per webhook request
- `source` — Grafana, Alertmanager, or custom
- `host` / `service` — target Icinga2 host/service
- `exit_status` / `label` — OK/WARNING/CRITICAL

## Prometheus Scrape Config

```yaml
scrape_configs:
  - job_name: icingaalertforge
    scrape_interval: 30s
    metrics_path: /metrics
    basic_auth:
      username: admin
      password: ${IAF_METRICS_PASSWORD}
    static_configs:
      - targets:
          - bridge-host:8080
```

## Grafana Dashboard

The `IAF Operations Analytics` dashboard is auto-provisioned in the test environment at `http://localhost:3000`.

Dashboard file: [`testenv/grafana/dashboards/iaf-operations-analytics.json`](../../testenv/grafana/dashboards/iaf-operations-analytics.json)

### Sections

| Section | Content |
|---|---|
| Executive Overview | RPM, error rate, p95 latency, history entries, queue saturation, Icinga2 health, brute-force IPs, RL queue, consecutive failures |
| Traffic & Reliability | RPM time series with 7d/14d/28d overlays, error rate trend, latency p50/p95/p99 |
| History & Alert Flow | By action, by mode, by severity (firing), history errors, avg duration, all entries by severity |
| Per-Source Analysis | Source breakdown table (requests, errors, error rate, history entries, last seen), RPM by source, history entries by source |
| Queue & Rate Limiter | Queue depth vs max, queue throughput (retried/dropped/failed), rate limiter saturation, rate limiter queue |
| Security & Auth | Auth failure rate, brute-force active IPs, cumulative failures |
| Runtime & Capacity | Goroutines, memory (heap/alloc/sys/stack), GC activity, uptime |
| Health | Icinga2 health state timeline, consecutive failures |

### Time Comparisons

Key panels overlay `offset 7d`, `offset 14d`, and `offset 28d` series for trend comparison. A Prometheus recording rule at [`testenv/prometheus/recording-rules.yml`](../../testenv/prometheus/recording-rules.yml) pre-computes the reference-day offset with day-of-week logic:

| Today | Reference day | Offset |
|---|---|---|
| Monday | Previous Friday | 3d |
| Saturday | Previous Saturday | 7d |
| Sunday | Previous Sunday | 7d |
| Tuesday–Friday | Previous day | 1d |

### Import to Existing Grafana

1. Copy `testenv/grafana/dashboards/iaf-operations-analytics.json` to your Grafana instance
2. Import via Grafana UI: Dashboards → New → Import
3. Select your Prometheus datasource
4. Optionally copy `testenv/prometheus/recording-rules.yml` to your Prometheus server for the pre-computed aggregates

## Alerting Recommendations

```yaml
# Prometheus alerting rules
groups:
  - name: icingaalertforge
    rules:
      - alert: IAFQueueBacklog
        expr: iaf_queue_depth / iaf_queue_max_size > 0.8
        for: 5m
        labels: {severity: warning}
        annotations: {summary: "Retry queue is 80% full"}

      - alert: IAFQueueDropping
        expr: rate(iaf_queue_dropped_total[5m]) > 0
        labels: {severity: critical}
        annotations: {summary: "Queue is dropping items"}

      - alert: IAFIcingaUnreachable
        expr: iaf_health_icinga_up == 0
        for: 2m
        labels: {severity: critical}
        annotations: {summary: "Icinga2 API is unreachable"}

      - alert: IAFHighErrorRate
        expr: rate(iaf_errors_total[5m]) / rate(iaf_requests_total[5m]) > 0.1
        for: 5m
        labels: {severity: warning}
        annotations: {summary: "Error rate above 10%"}
```

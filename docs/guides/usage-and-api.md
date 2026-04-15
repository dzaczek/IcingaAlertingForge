# Usage and API

Back to the [Documentation Index](../README.md)

## Work Mode

Work mode is the default behaviour. Incoming Grafana alerts are turned into passive check results for Icinga.

| Grafana Status | Severity | Icinga State |
|---|---|---|
| `resolved` | any | `OK (0)` |
| `firing` | `warning` | `WARNING (1)` |
| `firing` | `critical` | `CRITICAL (2)` |
| `firing` | missing or unknown | `CRITICAL (2)` |

The host does not come from the payload. It is selected entirely by the webhook key.

Example:

```bash
curl -X POST http://localhost:8080/webhook \
  -H "Content-Type: application/json" \
  -H "X-API-Key: key-a-1" \
  -d '{
    "status": "firing",
    "alerts": [{
      "status": "firing",
      "labels": {
        "alertname": "HighCPU",
        "severity": "critical"
      },
      "annotations": {
        "summary": "CPU usage above 95%"
      }
    }]
  }'
```

What happens next:

1. the bridge authenticates `key-a-1`
2. it resolves the configured host for Team A
3. it creates the missing service if needed
4. it sends a passive `CRITICAL` result to Icinga

## Test Mode

Test mode is useful when you want to create or delete services through the webhook path itself.

Set `mode=test` and `test_action=create|delete` in the alert labels.

Create a service:

```bash
curl -X POST http://localhost:8080/webhook \
  -H "Content-Type: application/json" \
  -H "X-API-Key: key-b-1" \
  -d '{
    "status": "firing",
    "alerts": [{
      "status": "firing",
      "labels": {
        "alertname": "SandboxProbe",
        "mode": "test",
        "test_action": "create"
      },
      "annotations": {
        "summary": "Create a manual sandbox service"
      }
    }]
  }'
```

Delete a service:

```bash
curl -X POST http://localhost:8080/webhook \
  -H "Content-Type: application/json" \
  -H "X-API-Key: key-b-1" \
  -d '{
    "status": "firing",
    "alerts": [{
      "status": "firing",
      "labels": {
        "alertname": "SandboxProbe",
        "mode": "test",
        "test_action": "delete"
      },
      "annotations": {
        "summary": "Delete manual sandbox service"
      }
    }]
  }'
```

## Grafana Contact Points

In most setups every team sends to the same bridge URL and uses a different API key.

That gives you one physical endpoint and many logical webhook destinations.

In other words:

- the URL stays the same
- the API key selects the configured target
- the selected target decides the host, source label, and notification settings

Example:

- Team A uses `authorization_credentials: key-a-1`
- Team B uses `authorization_credentials: key-b-1`
- both send to `http://webhook-bridge:8080/webhook`

Provisioning example:

```yaml
apiVersion: 1

contactPoints:
  - orgId: 1
    name: webhook-team-a
    receivers:
      - uid: webhook-team-a
        type: webhook
        settings:
          url: http://webhook-bridge:8080/webhook
          httpMethod: POST
          authorization_scheme: ApiKey
          authorization_credentials: key-a-1

  - orgId: 1
    name: webhook-team-b
    receivers:
      - uid: webhook-team-b
        type: webhook
        settings:
          url: http://webhook-bridge:8080/webhook
          httpMethod: POST
          authorization_scheme: ApiKey
          authorization_credentials: key-b-1
```

Accepted headers:

- `X-API-Key: <key>`
- `Authorization: ApiKey <key>`
- `Authorization: Bearer <key>`

## HTTP API

### `POST /webhook`

This is the main endpoint for Grafana or any other sender that can produce the same payload shape.

Response example:

```json
{
  "request_id": "550e8400-e29b-41d4-a716-446655440000",
  "source": "team-a",
  "target_id": "team-a",
  "host": "a-dummy-dev",
  "results": [
    {
      "status": "processed",
      "host": "a-dummy-dev",
      "service": "HighCPU",
      "exit_status": 2,
      "label": "CRITICAL",
      "icinga_ok": true,
      "duration_ms": 45
    }
  ]
}
```

Possible result values:

| Status | Meaning |
|---|---|
| `processed` | work mode result sent |
| `created` | test mode service created |
| `deleted` | test mode service deleted |
| `already_exists` | test mode create skipped because the cache says the service exists |
| `error` | the request was processed but the Icinga operation failed |

HTTP status codes:

| Code | Meaning |
|---|---|
| `200` | all alerts were handled successfully |
| `400` | invalid JSON or missing alerts |
| `401` | invalid or missing API key |
| `405` | wrong HTTP method |
| `502` | at least one alert failed against Icinga |

### Admin Endpoints

All admin endpoints use HTTP Basic Auth with `ADMIN_USER` and `ADMIN_PASS`.

#### `GET /admin/services`

Returns services across all configured hosts.

Optional filter:

```text
GET /admin/services?host=a-dummy-dev
```

Response example:

```json
{
  "host": "ALL TARGETS",
  "hosts": ["a-dummy-dev", "b-dummy-device"],
  "count": 52,
  "services": [
    {
      "host": "a-dummy-dev",
      "name": "HighCPU",
      "display_name": "HighCPU - CPU usage above 95%",
      "managed_by": "IcingaAlertingForge",
      "bridge_created_at": "2026-03-21T09:23:11Z",
      "exit_status": 2,
      "output": "CRITICAL: CPU usage above 95%",
      "last_check": "2026-03-21T09:24:00Z",
      "has_check_result": true
    }
  ]
}
```

#### `DELETE /admin/services/{name}`

Single host example:

```text
DELETE /admin/services/HighCPU
```

Multi host example:

```text
DELETE /admin/services/HighCPU?host=a-dummy-dev
```

#### `POST /admin/services/{name}/status`

**Fast Track:** Manually sets the status of a specific service.

**Deep Dive:** Sends a manual passive check result to Icinga2 for the specified service. The request body must include the `host`, `exit_status` (0=OK, 1=WARNING, 2=CRITICAL, 3=UNKNOWN), and `output` message.

Request body example:

```json
{
  "host": "a-dummy-dev",
  "exit_status": 2,
  "output": "CRITICAL: Manual status set via dashboard"
}
```

Response example:

```json
{
  "status": "updated",
  "host": "a-dummy-dev",
  "service": "HighCPU",
  "exit_status": 2
}
```

#### `POST /admin/services/bulk-delete`

<!-- LANG: hyphenation -->
Preferred request body in a multi-host setup:

```json
{
  "services": [
    {"host": "a-dummy-dev", "service": "HighCPU"},
    {"host": "b-dummy-device", "service": "DiskFull"}
  ]
}
```

The older string array form still works if exactly one host is configured:

```json
{"services": ["HighCPU", "DiskFull"]}
```

#### `POST` / `DELETE` `/admin/services/{name}/freeze`

**Fast Track:** Freezes (POST) or unfreezes (DELETE) a specific service to prevent it from auto-resolving.

**Deep Dive:** A frozen service will ignore subsequent OK check results (e.g., from an auto-resolving alert). The POST request accepts a JSON body with the `host` and an optional `duration_seconds`. If `duration_seconds` is provided, the freeze will automatically expire after the duration. DELETE removes the freeze.

Request body example (POST):

```json
{
  "host": "a-dummy-dev",
  "duration_seconds": 3600
}
```

Response example:

```json
{"status": "frozen"}
```

#### `GET /admin/services/frozen`

**Fast Track:** Lists all currently frozen services.

**Deep Dive:** Returns a list of all frozen services across all hosts. Each item contains the host, the service name, and optionally the timestamp until which the service is frozen.

Response example:

```json
{
  "frozen": [
    {
      "host": "a-dummy-dev",
      "service": "HighCPU",
      "frozen_until": "2026-03-21T10:24:00Z"
    }
  ]
}
```

#### `GET /admin/ratelimit`

Returns the current mutate and status slot usage, plus queue depth.

#### `GET /admin/queue`

**Fast Track:** Returns the current retry queue statistics including the number of pending items.

**Deep Dive:** Returns a JSON object with `size` representing the total number of queued webhooks, and `is_flushing` indicating if a flush operation is currently active.

#### `POST /admin/queue/flush`

**Fast Track:** Flushes the retry queue immediately, forcing an attempt to send all queued items.

**Deep Dive:** Resets the backoff timers for all items in the retry queue and wakes up the background worker to process them immediately. Returns an empty JSON object with HTTP 200 on success.

#### `GET /admin/users`

**Fast Track:** Returns a list of all RBAC users.

**Deep Dive:** Returns a JSON object mapping usernames to their respective roles (e.g. `{"admin": "admin", "viewer": "viewer"}`). Secrets/passwords are not returned.

#### `POST /admin/users`

**Fast Track:** Creates or updates an RBAC user.

**Deep Dive:** Upserts a user in the RBAC system. Requires `username`, `password`, and `role` (one of `admin`, `operator`, `viewer`) in the JSON body. If the user already exists, their password or role is updated.

Request body example:

```json
{
  "username": "jane.doe",
  "password": "secretpassword",
  "role": "operator"
}
```

#### `DELETE /admin/users/{username}`

**Fast Track:** Deletes an RBAC user. You cannot delete your own account.

**Deep Dive:** Removes the specified user from the RBAC system. Returns HTTP 200 with `{"status": "deleted"}`. Attempting to delete the currently authenticated user will return HTTP 400.

```text
DELETE /admin/users/jane.doe
```

<!-- CHANGED: added history clear and debug toggle admin endpoints -->

#### `POST /admin/history/clear`

Clears all history entries.

```bash
curl -u admin:secret -X POST http://localhost:8080/admin/history/clear
```

Response:

```json
{"status": "history cleared"}
```

#### `GET /admin/debug/toggle`

Returns the current state of the API debug ring buffer.

```bash
curl -u admin:secret http://localhost:8080/admin/debug/toggle
```

Response:

```json
{"enabled": false}
```

#### `POST /admin/debug/toggle`

Enables or disables the API debug capture ring buffer.

```bash
curl -u admin:secret -X POST http://localhost:8080/admin/debug/toggle \
  -H "Content-Type: application/json" \
  -d '{"enabled": true}'
```

Response:

```json
{"enabled": true}
```

### Settings Endpoints (Dashboard Config Mode)

These endpoints are only available when `CONFIG_IN_DASHBOARD=true`. All require HTTP Basic Auth.

#### `GET /admin/settings`

Returns the full configuration with secrets masked as `***`.

#### `PATCH /admin/settings`

Partially updates the configuration. Only non-empty fields are applied. Password fields with value `***` are ignored (preserving the current value).

```bash
curl -u admin:secret -X PATCH http://localhost:8080/admin/settings \
  -H "Content-Type: application/json" \
  -d '{"log_level": "debug", "cache_ttl_minutes": 15}'
```

#### `POST /admin/settings/targets`

Adds a new target. Auto-generates a UUID if `id` is empty and an API key if none is provided. Returns the new API key in cleartext (shown only once). Input fields are validated against HTML/script injection.

```bash
curl -u admin:secret -X POST http://localhost:8080/admin/settings/targets \
  -H "Content-Type: application/json" \
  -d '{"id": "team-c", "source": "team-c", "host_name": "c-dummy-dev"}'
```

#### `DELETE /admin/settings/targets/{id}`

Removes a target and all its API keys.

#### `POST /admin/settings/targets/{id}/generate-key`

Generates a new API key for the target. Returns the key in cleartext (shown only once).

#### `GET /admin/settings/targets/{id}/reveal-keys`

Returns the unmasked API keys for a specific target. Admin-only.

#### `POST /admin/settings/test-icinga`

Tests the Icinga2 connection using stored credentials. Returns connection status and Icinga2 version.

```json
{"status": "ok", "icinga2_version": "v2.15.2"}
```

#### `GET /admin/settings/export`

Downloads the full configuration as a JSON backup file. Secrets are included in cleartext for restore purposes.

#### `POST /admin/settings/import`

Restores configuration from a previously exported backup. Validates schema and target structure. If secrets are masked as `***`, current values are preserved.

### Status Endpoints

#### `GET /status/beauty`

Public panel.

#### `GET /status/beauty?admin=1`

Admin panel.

#### `GET /status/{service_name}`

Queries one service from the cache and from Icinga.

Single host example:

```text
GET /status/HighCPU
```

Multi host example:

```text
GET /status/HighCPU?host=a-dummy-dev
```

If several hosts are configured and you omit `host`, the endpoint returns `400`.

Response example:

```json
{
  "host": "a-dummy-dev",
  "service": "HighCPU",
  "cache_state": "ready",
  "exists_in_icinga": true,
  "last_check_result": {
    "exit_status": 2,
    "output": "CRITICAL: CPU usage above 95%",
    "timestamp": "2026-03-21T09:24:00Z"
  }
}
```

### History Endpoints

#### `GET /history`

Supported filters:

| Query | Description |
|---|---|
| `limit` | maximum number of entries |
| `service` | filter by service name |
| `source` | filter by source label |
| `host` | filter by target host |
| `mode` | `work` or `test` |
| `from` | `YYYY-MM-DD` or RFC3339 |
| `to` | `YYYY-MM-DD` or RFC3339 |

Example:

```text
GET /history?source=team-b&host=b-dummy-device&limit=50
```

Each history row includes `host_name`.

#### `GET /history/export`

Downloads the raw JSONL file.

<!-- CHANGED: added SSE and logout endpoints -->

### SSE Endpoint

#### `GET /status/beauty/events`

Server-Sent Events stream for real-time dashboard updates. No authentication required.

Event types:

| Event | Description |
|---|---|
| `webhook` | Alert data (default unnamed event) |
| `debug` | API traffic when debug capture is enabled |

The broker accepts up to 50 concurrent clients. When the limit is exceeded, the server returns `503 Service Unavailable`.

Example:

```bash
curl -N http://localhost:8080/status/beauty/events
```

### Logout Endpoint

#### `GET /status/beauty/logout`

Forces the browser to clear cached HTTP Basic Auth credentials. Returns `401` with a `WWW-Authenticate` header and a meta-refresh redirect to `/status/beauty`.

### Health Endpoint

#### `GET /health`

```json
{"status":"ok","version":"1.0.0"}
```

### Metrics Endpoint

#### `GET /metrics`

Exposes application and system metrics in Prometheus text format (version 0.0.4).

**Authentication:**
- If `METRICS_TOKEN` is configured, it can be accessed via `Authorization: Bearer <token>`.
- Otherwise, it requires the same Basic Auth as other admin endpoints.

**Available Metrics:**
- `iaf_uptime_seconds`: Seconds since server start.
- `iaf_requests_total`: Total webhook requests received.
- `iaf_request_latency_milliseconds`: Request latency distribution.
- `iaf_source_requests_total{source="..."}`: Real-time per-API-key request counter.
- `iaf_history_by_severity{severity="..."}`: Entry count per alert severity from history.
- `iaf_queue_depth`: Current retry queue depth.
- `iaf_ratelimiter_slots_in_use{type="..."}`: Currently occupied concurrency slots.
- `iaf_health_icinga_up`: 1 if Icinga2 is reachable, 0 otherwise.

Example scrape:
```text
# HELP iaf_requests_total Total webhook requests received
# TYPE iaf_requests_total counter
iaf_requests_total 4821
```

## Next Step

Continue with [Icinga Integration](icinga-integration.md).

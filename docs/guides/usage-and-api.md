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

#### `POST /admin/services/bulk-delete`

Preferred request body in a multi host setup:

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

#### `GET /admin/ratelimit`

Returns the current mutate and status slot usage, plus queue depth.

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

### Health Endpoint

#### `GET /health`

```json
{"status":"ok","version":"1.0.0"}
```

## Next Step

Continue with [Icinga Integration](icinga-integration.md).

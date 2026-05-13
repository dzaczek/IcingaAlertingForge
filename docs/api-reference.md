# API Reference

## Authentication

All admin endpoints require HTTP Basic Auth. Webhook endpoints require an `X-API-Key` header.

## Webhook Endpoints

### `POST /webhook`

Receives alerts from Grafana, Alertmanager, or custom sources.

**Headers:**
- `X-API-Key: <key>` (required)
- `Content-Type: application/json`

**Request:** Grafana payload
```json
{
  "status": "firing",
  "alerts": [
    {
      "status": "firing",
      "labels": {"alertname": "CPU", "severity": "critical"},
      "annotations": {"summary": "CPU > 90%"}
    }
  ]
}
```

**Response:** `200 OK`
```json
{"results":[{"status":"processed","host":"host-a","service":"CPU","exit_status":2,"label":"CRITICAL","icinga_ok":true}]}
```

## Health & Status

### `GET /health`

Health check endpoint.

**Response:** `200 OK`
```json
{"status":"ok","icinga_up":true,"uptime":"2h30m15s","version":"v1.0.0"}
```

### `GET /status`

Redirects to the beauty dashboard (`/status/beauty`).

### `GET /status/beauty`

Beauty dashboard (HTML, SSE-powered live updates).

### `GET /status/beauty/events`

SSE event stream for live dashboard updates.

### `GET /history`

Recent alert history (HTML).

### `GET /history/export`

Export history as JSON. Query params: `?format=json&limit=1000`.

## Admin API

All admin endpoints require HTTP Basic Auth with admin credentials.

### `GET /admin/services`

List cached services.

**Response:**
```json
{"services":[{"host":"host-a","service":"CPU","state":"ok"}]}
```

### `DELETE /admin/services/{name}?host=<host>`

Delete a service from Icinga2.

### `POST /admin/services/{name}/status`

Manually set a service's status.

**Body:**
```json
{"host": "host-a", "exit_status": 2, "output": "CRITICAL: Manual check"}
```

### `POST /admin/services/bulk-delete`

Bulk delete services.

**Body:**
```json
{"services": ["svc1", "svc2"]}
```

### `POST /admin/services/{name}/freeze`

Freeze/unfreeze a service. POST to freeze, DELETE to unfreeze.

**Body:**
```json
{"host": "host-a", "duration_seconds": 3600}
```

### `GET /admin/services/frozen`

List frozen services.

### `GET /admin/queue`

Retry queue statistics.

### `POST /admin/queue/flush`

Flush the retry queue.

### `GET /admin/ratelimit`

Rate limiter statistics.

### `POST /admin/history/clear`

Clear all history.

### `GET /admin/debug/toggle` | `POST /admin/debug/toggle`

View or toggle API debug ring buffer.

### `GET /admin/users`

List RBAC users.

### `POST /admin/users`

Create an RBAC user.

**Body:**
```json
{"username": "operator1", "password": "secret", "role": "operator"}
```

### `DELETE /admin/users/{username}`

Delete an RBAC user.

## Settings API

### `GET /admin/settings`

Get current configuration (secrets masked).

### `PATCH /admin/settings`

Partially update configuration.

### `POST /admin/settings/targets`

Add a new target.

**Body:**
```json
{"host_name": "prod-icinga", "source": "grafana-prod"}
```

### `DELETE /admin/settings/targets/{id}`

Delete a target.

### `POST /admin/settings/targets/{id}/generate-key`

Generate a new API key for a target.

### `GET /admin/settings/targets/{id}/reveal-keys`

Reveal API keys for a target (requires admin).

### `POST /admin/settings/test-icinga`

Test Icinga2 connectivity.

### `GET /admin/settings/export`

Export configuration as JSON (for backup).

### `POST /admin/settings/import`

Import configuration from JSON.

## Metrics

### `GET /metrics`

Prometheus metrics endpoint. Requires Basic Auth.

## Error Codes

| Code | Meaning |
|------|---------|
| 200 | Success |
| 400 | Bad request (invalid JSON, missing fields) |
| 401 | Unauthorized (invalid/missing credentials) |
| 403 | Forbidden (insufficient permissions) |
| 404 | Not found |
| 405 | Method not allowed |
| 409 | Conflict (duplicate target, ownership conflict) |
| 429 | Rate limited |
| 500 | Internal server error |
| 502 | Bad gateway (Icinga2 API error) |
| 503 | Service unavailable (rate limit queue full) |

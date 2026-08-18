# API Reference

## Authentication

All admin endpoints require HTTP Basic Auth. Webhook endpoints require an `X-API-Key` header.

## Webhook Endpoints

### `POST /webhook`

**Fast Track:** Receives alerts from external sources.

**Deep Dive:** Receives webhook POST payloads from Grafana, Alertmanager, or custom sources. Parses the payload, maps it to a target host based on the `X-API-Key` header, and forwards the status to Icinga2.

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

**Fast Track:** Health check endpoint.

**Deep Dive:** Returns system status, version, uptime, and basic Icinga2 connectivity state.

**Response:** `200 OK`
```json
{"status":"ok","icinga_up":true,"uptime":"2h30m15s","version":"v1.0.0"}
```

### `GET /status`

**Fast Track:** Redirects to the beauty dashboard.

**Deep Dive:** Returns a `302 Found` to `/status/beauty`.

### `GET /status/beauty`

**Fast Track:** The beauty dashboard interface.

**Deep Dive:** Serves the HTML, JS, and CSS for the main dashboard. Supports an optional `?admin=1` query parameter for the admin panel.

### `GET /status/beauty/stats`

**Fast Track:** Returns a JSON snapshot of dashboard statistics.

**Deep Dive:** Returns current dashboard overview metrics including total webhooks processed, error counts, active alerts by severity, test mode statistics, and server uptime.

### `GET /status/beauty/logout`

**Fast Track:** Logs out of the admin session.

**Deep Dive:** Clears the Basic Auth credentials by returning a `401 Unauthorized` with a new `WWW-Authenticate` header, and redirects the user back to the dashboard interface.

### `GET /status/beauty/events`

**Fast Track:** SSE event stream for live updates.

**Deep Dive:** Server-Sent Events stream providing real-time dashboard updates. Emits `webhook` and `debug` events.

### `GET /status/{service_name}`

**Fast Track:** Queries one service state from cache and Icinga2.

**Deep Dive:** Returns detailed state including `cache_state`, `exists_in_icinga`, and `last_check_result`. If multiple hosts are configured, the `?host=<host>` query parameter is mandatory.

**Response:** `200 OK`
```json
{
  "host": "host-a",
  "service": "CPU",
  "cache_state": "ready",
  "exists_in_icinga": true,
  "last_check_result": {
    "exit_status": 2,
    "output": "CRITICAL: CPU usage above 95%",
    "timestamp": "2026-03-21T09:24:00Z"
  }
}
```

### `GET /history`

**Fast Track:** Recent alert history.

**Deep Dive:** Returns a JSON list of recent alerts. Supports query filters: `limit`, `service`, `source`, `host`, `mode`, `from`, `to`.

### `GET /history/export`

**Fast Track:** Export history as JSONL.

**Deep Dive:** Downloads the raw JSONL file containing the alert history.

## Admin API

All admin endpoints require HTTP Basic Auth with admin credentials.

### `GET /admin/services`

**Fast Track:** Lists all managed services across configured target hosts.

**Deep Dive:** Queries the cache and Icinga2 to return a combined list of all passive check services managed by the bridge. Accepts an optional `host` query parameter to filter by a specific target host.

**Response:**
```json
{"services":[{"host":"host-a","service":"CPU","state":"ok"}]}
```

### `DELETE /admin/services/{name}?host=<host>`

**Fast Track:** Deletes a specific service from Icinga2.

**Deep Dive:** Removes the specified service object from Icinga2 via its API and evicts it from the local bridge cache. If multiple hosts are configured, the `host` query parameter is mandatory to prevent ambiguous deletions.

### `POST /admin/services/{name}/status`

**Fast Track:** Manually sets the status of a specific service.

**Deep Dive:** Sends a manual passive check result to Icinga2 for the specified service. The request body must include the `host`, `exit_status` (0=OK, 1=WARNING, 2=CRITICAL, 3=UNKNOWN), and `output` message.

**Body:**
```json
{"host": "host-a", "exit_status": 2, "output": "CRITICAL: Manual check"}
```

### `POST /admin/services/bulk-delete`

**Fast Track:** Bulk deletes services from Icinga2.

**Deep Dive:** Accepts a list of objects specifying `host` and `service` and deletes all matching services from Icinga2 and local cache.

**Body:**
```json
{"services": [{"host": "host-a", "service": "svc1"}, {"host": "host-b", "service": "svc2"}]}
```

### `POST /admin/services/{name}/freeze`

**Fast Track:** Freezes or unfreezes a specific service to prevent it from auto-resolving.

**Deep Dive:** A frozen service will ignore subsequent OK check results (e.g., from an auto-resolving alert). POST to freeze, DELETE to unfreeze.

**Body:**
```json
{"host": "host-a", "duration_seconds": 3600}
```

### `GET /admin/services/frozen`

**Fast Track:** Lists all currently frozen services.

**Deep Dive:** Returns a list of all frozen services across all hosts, along with their `frozen_until` timestamps.

### `GET /admin/queue`

**Fast Track:** Returns the current retry queue statistics.

**Deep Dive:** Returns a JSON object with `size` representing the total number of queued webhooks, and `is_flushing`.

### `POST /admin/queue/flush`

**Fast Track:** Flushes the retry queue immediately.

**Deep Dive:** Resets the backoff timers for all items in the retry queue and wakes up the background worker to process them immediately.

### `GET /admin/ratelimit`

**Fast Track:** Returns the current rate limiter statistics.

**Deep Dive:** Returns the current mutate and status slot usage for bounded concurrency API calls to Icinga2, plus the queue depth.

### `POST /admin/history/clear`

**Fast Track:** Clears all history entries.

**Deep Dive:** Truncates the history log file and clears the in-memory history ring buffer. This action is irreversible.

### `GET /admin/debug/toggle` | `POST /admin/debug/toggle`

**Fast Track:** View or toggle API debug capture ring buffer.

**Deep Dive:** When enabled via POST `{"enabled": true}`, recent HTTP requests and responses to/from Icinga2 are captured in memory for dashboard inspection.

### `GET /admin/users`

**Fast Track:** Returns a list of all RBAC users.

**Deep Dive:** Returns a JSON object mapping usernames to their respective roles. Secrets/passwords are not returned.

### `POST /admin/users`

**Fast Track:** Creates or updates an RBAC user.

**Deep Dive:** Upserts a user in the RBAC system. Requires `username`, `password`, and `role` (one of `admin`, `operator`, `viewer`) in the JSON body.

**Body:**
```json
{"username": "operator1", "password": "secret", "role": "operator"}
```

### `DELETE /admin/users/{username}`

**Fast Track:** Deletes an RBAC user.

**Deep Dive:** Removes the specified user from the RBAC system. Attempting to delete the currently authenticated user will fail.

## Settings API

### `GET /admin/settings`

**Fast Track:** Returns the current application configuration.

**Deep Dive:** Returns the full configuration with secrets masked as `***`.

### `PATCH /admin/settings`

**Fast Track:** Partially updates the configuration.

**Deep Dive:** Only non-empty fields are applied. Password fields with value `***` are ignored (preserving the current value).

### `POST /admin/settings/targets`

**Fast Track:** Adds a new target host mapping.

**Deep Dive:** Auto-generates a UUID if `id` is empty and an API key if none is provided. Returns the new API key in cleartext (shown only once).

**Body:**
```json
{"host_name": "prod-icinga", "source": "grafana-prod"}
```

### `DELETE /admin/settings/targets/{id}`

**Fast Track:** Removes a target.

**Deep Dive:** Deletes the target mapping and revokes all its API keys.

### `POST /admin/settings/targets/{id}/generate-key`

**Fast Track:** Generates a new API key for a target.

**Deep Dive:** Creates a new key and returns it in cleartext (shown only once).

### `GET /admin/settings/targets/{id}/reveal-keys`

**Fast Track:** Reveals API keys for a target.

**Deep Dive:** Returns the unmasked API keys for a specific target. Admin-only.

### `POST /admin/settings/test-icinga`

**Fast Track:** Tests Icinga2 connectivity.

**Deep Dive:** Uses the stored Icinga2 credentials to verify connection status and fetch the Icinga2 version.

### `GET /admin/settings/export`

**Fast Track:** Exports configuration as JSON.

**Deep Dive:** Downloads the full configuration as a JSON backup file. Secrets are included in cleartext for restore purposes.

### `POST /admin/settings/import`

**Fast Track:** Imports configuration from JSON.

**Deep Dive:** Restores configuration from a previously exported backup. Validates schema and target structure.

## Metrics

### `GET /metrics`

**Fast Track:** Prometheus metrics endpoint.

**Deep Dive:** Exposes application and system metrics in Prometheus text format. Authentication defaults to Basic Auth, or Bearer token if `METRICS_TOKEN` is configured.

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

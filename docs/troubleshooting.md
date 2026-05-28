# Troubleshooting

## Common Issues

### "Unauthorized webhook request" (401)

The API key in the `X-API-Key` header does not match any configured key.

Check:
- The correct API key is in the webhook configuration
- The key was not regenerated (check `ADMIN_PASS` for dashboard access)
- Environment variables are loaded correctly

### "Failed to decode webhook payload" (400)

The JSON body is malformed.

Check:
- The source is sending valid JSON
- Content-Type header is `application/json`
- The payload structure matches the expected format

### "Rate limit: status update queue full" (503)

Too many concurrent status updates.

Check:
- Adjust `RATE_LIMIT_STATUS` and `RATE_LIMIT_MAX_QUEUE` environment variables
- Check for alert storms in the source

### "Host query parameter required when multiple targets" (400)

More than one target is configured but no host was specified.

Fix:
- Add a `?host=<name>` query parameter to the API call
- Or configure only a single target

### "Not managed by the bridge" (403)

Attempting to delete or modify a service that was not created by the bridge.

Fix:
- Use the `ICINGA2_FORCE=true` flag to override ownership checks
- Or set the service's `vars.managed_by` to `IcingaAlertingForge` in Icinga2

### "Import references unknown template" (HTTP 500)

When the bridge tries to auto-create a host or service, Icinga2 may respond with HTTP 500 and the error message `Import references unknown template`. This happens because the bridge hardcodes the `generic-host` and `generic-service` templates, which are missing in some Icinga2 environments — especially those managed by Icinga Director or minimal installations.

**Solution A: Add the missing templates**

Create `/etc/icinga2/conf.d/templates.conf` and reload Icinga2:

```icinga2
template Host "generic-host" {
  max_check_attempts = 3
  check_interval = 1m
  retry_interval = 30s
  check_command = "hostalive"
}

template Service "generic-service" {
  max_check_attempts = 5
  check_interval = 1m
  retry_interval = 30s
}
```

```bash
icinga2 daemon -C && systemctl reload icinga2
```

**Solution B: Disable auto-creation**

Set `ICINGA2_HOST_AUTO_CREATE=false` and manually create the dummy host in Icinga2 with the required custom variables (`vars.managed_by`, `vars.iaf_managed`).

### Icinga2 connection refused

The bridge cannot reach the Icinga2 API.

Check:
- Icinga2 API is enabled: `icinga2 feature enable api`
- Port 5665 is accessible from the bridge
- TLS certificate is valid (or set `ICINGA2_TLS_SKIP_VERIFY=true`)
- Credentials are correct

### Retry queue growing

Items are consistently failing to reach Icinga2.

Check:
- Icinga2 API health via `/health` endpoint
- `iaf_queue_depth` metric in Prometheus
- Logs for specific error patterns
- `make smoke` to test the full pipeline locally

### Config storage issues

The encrypted JSON config storage may become corrupted.

Recovery:
1. The bridge falls back to environment variables if the config file is invalid
2. Delete the config file and restart to start fresh
3. Use `/admin/settings/export` to back up config before making changes

## Diagnostic Commands

```bash
# Health check
curl -s http://localhost:8080/health | jq .

# Metrics
curl -s -u admin:pass http://localhost:8080/metrics | grep iaf_

# Export config (backup)
curl -s -u admin:pass http://localhost:8080/admin/settings/export > backup.json

# Queue stats
curl -s -u admin:pass http://localhost:8080/admin/queue | jq .

# Rate limit stats
curl -s -u admin:pass http://localhost:8080/admin/ratelimit | jq .
```

## Log Analysis

All logs use structured JSON format. Example queries with `jq`:

```bash
# Filter ERROR level
jq 'select(.level=="ERROR")' audit.log

# Recent auth failures
jq 'select(.event_type=="auth.failure")' audit.log | tail -5

# Webhook latency
jq 'select(.msg | test("Check result sent")) | .duration_ms' audit.log
```

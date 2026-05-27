# Production Deployment — Complete Guide

This guide covers the full path from `git clone` to a working production setup:
installing the bridge, configuring Icinga2, wiring Grafana, testing the flow,
and setting up notifications.

## 1. Clone and Configure

```bash
git clone https://github.com/dzaczek/IcingaAlertingForge.git
cd IcingaAlertingForge
```

Copy the example env file and edit it:

```bash
cp .env.example .env
```

### Minimal `.env` for production

```env
# ── Server ──
SERVER_PORT=8080
SERVER_HOST=0.0.0.0

# ── Icinga2 API ──
ICINGA2_HOST=https://your-icinga2-server:5665
ICINGA2_USER=apiuser
ICINGA2_PASS=your-api-password
ICINGA2_HOST_AUTO_CREATE=true
ICINGA2_TLS_SKIP_VERIFY=false

# ── Target host: where alerts land in Icinga2 ──
# Syntax: IAF_TARGET_{ID}_*
# ID becomes the target identifier, used in routing
IAF_TARGET_MYAPP_SOURCE=myapp-alerts
IAF_TARGET_MYAPP_HOST_NAME=myapp-alerts
IAF_TARGET_MYAPP_HOST_DISPLAY=MyApp Alerting Host
IAF_TARGET_MYAPP_HOST_ADDRESS=10.0.0.50
IAF_TARGET_MYAPP_API_KEYS=sk-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
IAF_TARGET_MYAPP_NOTIFICATION_USERS=oncall-team
IAF_TARGET_MYAPP_NOTIFICATION_SERVICE_STATES=critical
IAF_TARGET_MYAPP_NOTIFICATION_HOST_STATES=down

# ── Admin dashboard credentials ──
ADMIN_USER=admin
ADMIN_PASS=your-admin-password

# ── History ──
HISTORY_FILE=/var/log/webhook-bridge/history.jsonl
HISTORY_MAX_ENTRIES=10000

# ── Metrics ──
METRICS_ENABLED=true

# ── Optional: store config in dashboard UI instead of env vars ──
CONFIG_IN_DASHBOARD=true
```

### Target configuration — how it works

Each `IAF_TARGET_{ID}_*` block creates one managed host in Icinga2:

| Variable | Required | Description |
|---|---|---|
| `SOURCE` | yes | Routing key — alerts from this source land on this host |
| `HOST_NAME` | yes | Icinga2 host object name (short, no spaces) |
| `HOST_DISPLAY` | no | Display name in Icinga2 |
| `HOST_ADDRESS` | no | IP stored as metadata (no active checks run) |
| `API_KEYS` | yes | Comma-separated keys. Grafana sends one of these in `X-API-Key` header |
| `NOTIFICATION_USERS` | no | Comma-separated Icinga2 user names for notifications |
| `NOTIFICATION_SERVICE_STATES` | no | Which service states trigger notification (critical, warning, ok, unknown) |
| `NOTIFICATION_HOST_STATES` | no | Which host states trigger notification (down, up) |

Add as many targets as you need — one per team, service group, or environment.

## 2. Prepare Icinga2

### Enable the REST API

The bridge communicates with Icinga2 exclusively through its REST API on port 5665.

If the API is not yet enabled:

```bash
icinga2 api setup
```

This creates `/etc/icinga2/features-enabled/api.conf`. Edit it:

```icinga2
object ApiListener "api" {
  bind_port = 5665
  accept_config = true
  accept_commands = true
}
```

Create an API user with full permissions:

```icinga2
# /etc/icinga2/conf.d/api-users.conf
object ApiUser "apiuser" {
  password = "your-api-password"
  permissions = [ "*" ]
}
```

Restart Icinga2:

```bash
systemctl restart icinga2
```

Verify the API is reachable:

```bash
curl -k -u apiuser:your-api-password https://your-icinga2:5665/v1/status
```

### Required templates

The bridge needs `generic-host` and `generic-service` templates. These are present in a default Icinga2 installation. Verify:

```bash
icinga2 object list --type Host --name 'generic-host'
icinga2 object list --type Service --name 'generic-service'
```

### Auto-created hosts

When `ICINGA2_HOST_AUTO_CREATE=true`, the bridge creates passive dummy hosts on startup:

- `check_command = "dummy"` — no active checks
- `enable_active_checks = false`
- `max_check_attempts = 1`
- Hosts are marked with `vars.managed_by = "IcingaAlertingForge"`

These hosts exist purely to receive passive check results. They do not ping anything.

## 3. Install with Docker Compose

Create `docker-compose.yml`:

```yaml
version: "3.9"

services:
  webhook-bridge:
    image: ghcr.io/dzaczek/icingaalertingforge:v1.0.0
    container_name: webhook-bridge
    restart: unless-stopped
    ports:
      - "8080:8080"
    env_file:
      - .env
    volumes:
      - webhook_logs:/var/log/webhook-bridge
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:8080/health"]
      interval: 10s
      timeout: 5s
      retries: 5

volumes:
  webhook_logs:
```

Start:

```bash
docker compose up -d
```

Or build from source:

```yaml
services:
  webhook-bridge:
    build: .
    # ... rest same as above
```

Check that the bridge is healthy:

```bash
curl http://localhost:8080/health
```

```json
{
  "healthy": true,
  "icinga_up": true,
  "queue_depth": 0,
  "status": "ok",
  "version": "v1.0.0"
}
```

## 4. Configure Grafana

### Create a contact point

In Grafana, go to **Alerting → Contact Points → New contact point**:

- **Name:** `IcingaAlertForge – MyApp`
- **Integration:** Webhook
- **URL:** `http://your-bridge:8080/webhook`
- **HTTP Method:** POST
- **Content Type:** JSON
- **Additional headers:**
  - Header: `X-API-Key`, Value: `sk-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx`

**Body template** (Grafana default template works, or use this):

```json
{
  "receiver": "{{ .Receiver }}",
  "status": "{{ .Status }}",
  "alerts": {{ .Alerts }},
  "groupLabels": {{ .GroupLabels }},
  "commonLabels": {{ .CommonLabels }},
  "commonAnnotations": {{ .CommonAnnotations }}
}
```

### Create a notification policy

**Alerting → Notification Policies → New policy:**

- **Matching labels:** e.g. `team=myapp`
- **Contact point:** `IcingaAlertForge – MyApp`
- **Group by:** keep defaults

Optionally set this as the **default** policy to route all unmatched alerts through the bridge.

### Test from Grafana UI

In the contact point settings, click **Test**. Grafana sends a sample firing alert. Check:

```bash
curl -u admin:your-admin-password http://your-bridge:8080/history?limit=1
```

You should see an entry with `action: "firing"` and `icinga_ok: true`.

## 5. Test the Flow

### Send a test alert manually

```bash
curl -X POST http://your-bridge:8080/webhook \
  -H "Content-Type: application/json" \
  -H "X-API-Key: sk-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx" \
  -d '{
    "receiver": "myapp-alerts",
    "status": "firing",
    "alerts": [{
      "status": "firing",
      "labels": {
        "alertname": "TestCPUHigh",
        "severity": "critical",
        "host": "myapp-alerts"
      },
      "annotations": {
        "summary": "CPU usage above 95%",
        "description": "Manual test from CLI"
      }
    }]
  }'
```

Expected response:

```json
{
  "host": "myapp-alerts",
  "request_id": "...",
  "results": [{
    "status": "processed",
    "exit_status": 2,
    "label": "CRITICAL",
    "icinga_ok": true,
    "service": "TestCPUHigh"
  }]
}
```

### Verify in Icinga2

```bash
# List services on the managed host
icinga2 object list --type Service --filter 'host.name=="myapp-alerts"'

# Check the last check result
curl -k -u apiuser:your-api-password \
  https://your-icinga2:5665/v1/objects/services/myapp-alerts!TestCPUHigh
```

The Icinga Web 2 dashboard should also show the service with the passive check result.

### Send a resolved alert

```bash
curl -X POST http://your-bridge:8080/webhook \
  -H "Content-Type: application/json" \
  -H "X-API-Key: sk-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx" \
  -d '{
    "receiver": "myapp-alerts",
    "status": "resolved",
    "alerts": [{
      "status": "resolved",
      "labels": {
        "alertname": "TestCPUHigh",
        "severity": "critical",
        "host": "myapp-alerts"
      },
      "annotations": {
        "summary": "CPU back to normal"
      }
    }]
  }'
```

The service in Icinga2 should now show `OK` status.

## 6. Configure Notifications for Custom-Named Devices

The bridge writes notification metadata onto Icinga2 host objects. Your Icinga2 notification rules read this metadata and decide who gets notified.

### What the bridge writes

For a target configured with:
```env
IAF_TARGET_MYAPP_NOTIFICATION_USERS=oncall-team,escalation-group
IAF_TARGET_MYAPP_NOTIFICATION_SERVICE_STATES=critical
IAF_TARGET_MYAPP_NOTIFICATION_HOST_STATES=down
```

The bridge creates a host with these custom variables:

```text
vars.notification.user_groups = [ "oncall-team", "escalation-group" ]
vars.notification.service_states = [ "critical" ]
vars.notification.host_states = [ "down" ]
```

### Icinga2 notification rules

Create notification rules that read these variables.

**Service notification** (`/etc/icinga2/conf.d/notifications.conf`):

```icinga2
apply Notification "iaf-service-notification" to Service {
  import "mail-service-notification"

  interval = 0s

  if (host.vars.notification.user_groups) {
    users = host.vars.notification.user_groups
  }

  if (host.vars.notification.service_states) {
    var svc_states = []
    if ("ok" in host.vars.notification.service_states)      { svc_states += [ OK ] }
    if ("warning" in host.vars.notification.service_states)  { svc_states += [ Warning ] }
    if ("critical" in host.vars.notification.service_states) { svc_states += [ Critical ] }
    if ("unknown" in host.vars.notification.service_states)  { svc_states += [ Unknown ] }
    if (len(svc_states) > 0) { states = svc_states }
  }

  assign where host.vars.managed_by == "IcingaAlertingForge"
}
```

**Host notification:**

```icinga2
apply Notification "iaf-host-notification" to Host {
  import "mail-host-notification"

  interval = 0s

  if (host.vars.notification.user_groups) {
    users = host.vars.notification.user_groups
  }

  if (host.vars.notification.host_states) {
    var hst_states = []
    if ("up" in host.vars.notification.host_states)   { hst_states += [ Up ] }
    if ("down" in host.vars.notification.host_states)  { hst_states += [ Down ] }
    if (len(hst_states) > 0) { states = hst_states }
  }

  assign where host.vars.managed_by == "IcingaAlertingForge"
}
```

### Custom device notifications (SMS, Slack, custom scripts)

To send notifications to a different transport, create a custom `NotificationCommand`:

```icinga2
object NotificationCommand "sms-notification" {
  command = [
    "/etc/icinga2/scripts/sms-notify.sh"
  ]
  arguments = {
    "-u" = "$user.display_name$"
    "-n" = "$host.display_name$"
    "-s" = "$service.display_name$"
    "-o" = "$service.output$"
    "-e" = "$service.state$"
    "-t" = "$notification.type$"
  }
}

template Notification "sms-service-notification" {
  command = "sms-notification"
  period = "24x7"
}
```

Then use it in your apply rule instead of `mail-service-notification`:

```icinga2
apply Notification "iaf-sms-alert" to Service {
  import "sms-service-notification"
  // ... same user_groups and states logic as above ...
  assign where host.vars.managed_by == "IcingaAlertingForge"
}
```

### Multiple transports from the same bridge target

If you want both email AND SMS from the same managed host, add separate user group variables:

```env
# In .env:
IAF_TARGET_MYAPP_NOTIFICATION_USERS=sms-team
```

Then in your Icinga2 config, use two apply rules with different templates:

```icinga2
# SMS goes to users from the bridge
apply Notification "iaf-sms" to Service {
  import "sms-service-notification"
  users = host.vars.notification.user_groups
  assign where host.vars.managed_by == "IcingaAlertingForge"
}

# Email goes to a static group
apply Notification "iaf-mail" to Service {
  import "mail-service-notification"
  user_groups = [ "ops-team" ]
  assign where host.vars.managed_by == "IcingaAlertingForge"
}
```

## 7. Add Prometheus + Grafana Dashboard

The bridge exposes metrics at `/metrics` (Basic Auth: `admin` / your admin password).

### Prometheus scrape config

```yaml
scrape_configs:
  - job_name: iaf-webhook-bridge
    scrape_interval: 30s
    metrics_path: /metrics
    basic_auth:
      username: admin
      password: your-admin-password
    static_configs:
      - targets:
          - your-bridge:8080
```

### Import Grafana dashboard

The repo includes a pre-built dashboard at:
`testenv/grafana/dashboards/iaf-operations-analytics.json`

In Grafana, go to **Dashboards → New → Import** and upload this JSON. The dashboard has 8 sections covering RPM, error rate, latency, queue depth, per-source breakdown, health, and more.

To auto-provision it, copy the dashboard JSON and the provisioning config from `testenv/grafana/provisioning/dashboards/` to your Grafana instance.

## 8. Monitoring Endpoints

| Endpoint | Auth | Description |
|---|---|---|
| `GET /health` | none | Bridge health + Icinga2 connectivity |
| `GET /metrics` | Basic | Prometheus metrics |
| `GET /status/beauty` | none | Public dashboard |
| `GET /status/beauty?admin=1` | Basic | Admin dashboard (RBAC, settings, queue) |
| `GET /history` | Basic | Alert history JSON |
| `GET /admin/services` | Basic | Managed services list |
| `GET /admin/queue` | Basic | Retry queue stats |

## 9. Health Check + Uptime

The bridge has a built-in health endpoint.

Docker healthcheck (already in the compose file):
```yaml
healthcheck:
  test: ["CMD", "curl", "-f", "http://localhost:8080/health"]
```

Prometheus alert:
```yaml
- alert: IAFBridgeUnhealthy
  expr: iaf_health_icinga_up == 0
  for: 2m
  labels: { severity: critical }
  annotations:
    summary: "IcingaAlertForge cannot reach Icinga2"
```

## 10. Troubleshooting

**Bridge can't reach Icinga2:**
```bash
curl -k -u apiuser:your-password https://your-icinga2:5665/v1/status
```
Check firewall rules — port 5665 must be reachable from the bridge container.

**404 on /metrics:**
Set `METRICS_ENABLED=true` in `.env` and restart.

**"unauthorized" on webhook:**
Verify the API key matches exactly. Check `X-API-Key` header is set, not `Authorization`.

**Grafana alerts not appearing in Icinga2:**
Check the bridge history: `curl -u admin:pass http://bridge:8080/history?limit=5`. Look for `icinga_ok: false` — this means the Icinga2 API call failed.

**Service already exists error:**
Set `ICINGA2_FORCE=true` to update existing services instead of skipping them.

**Host not auto-created:**
Set `ICINGA2_HOST_AUTO_CREATE=true` and verify the API user has config modification permissions.

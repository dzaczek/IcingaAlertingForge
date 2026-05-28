# IcingaAlertForge — Quick Setup Guide

> **Goal**: 15 minutes to a working Grafana/Prometheus → Icinga2 bridge.
> You do everything possible in the Beauty Panel. Zero poking around in files after startup.

---

## Step 1: Create an API user in Icinga2

On the Icinga2 server, create the file `/etc/icinga2/conf.d/api-users.conf`:

```bash
cat > /etc/icinga2/conf.d/api-users.conf << 'EOF'
object ApiUser "icinga-alertforge" {
  password = "Your-API-Password-Min-12-Chars"
  permissions = [
    "actions/process-check-result",
    "objects/query/Host",
    "objects/query/Service",
    "objects/create/Host",
    "objects/create/Service",
    "objects/delete/Service",
    "status/query"
  ]
}
EOF

systemctl restart icinga2
```

**Check if it works:**
```bash
curl -k -u icinga-alertforge:Your-API-Password-Min-12-Chars \
  https://your-icinga2:5665/v1/status
```

---

## Step 2: Prepare `.env` — absolute minimum

Copy and edit **3 variables** (the rest is done from the panel):

```bash
# .env — only this is required to start
ICINGA2_HOST=https://your-icinga2:5665
ICINGA2_USER=icinga-alertforge
ICINGA2_PASS=Your-API-Password-Min-12-Chars

ADMIN_USER=admin
ADMIN_PASS=my-admin-password

CONFIG_IN_DASHBOARD=true

# Optional: encryption key for config.json (if not provided, it will generate one itself)
# CONFIG_ENCRYPTION_KEY=my-secret-encryption-key
```

**That's it.** Targets, API keys, rate limiting, history — you'll configure everything in the panel.

---

## Step 3: Run the bridge

```bash
# Quick start locally
go build -o webhook-bridge . && ./webhook-bridge

# Or Docker Compose (recommended for production)
docker compose up -d --build
```

**Check if it's alive:**
```bash
curl http://localhost:8080/health
# → {"status":"ok","version":"v1.0.0-beta.163",...}
```

Upon the first run, the bridge automatically:
- creates hosts in Icinga2 (if `ICINGA2_HOST_AUTO_CREATE=true` — default),
- migrates configuration from `.env` to `config.json` (AES-256-GCM encrypted),
- from this point on, `config.json` is the source of truth.

---

## Step 4: Panel — configure everything from GUI

Open in your browser:

```
http://localhost:8080/status/beauty?admin=1
```

Log in (`admin` / `my-admin-password`).

### 4a. Check connection to Icinga2

In the side menu: **Settings** → **Icinga2 API** section → click **Test Connection**.

You should see the Icinga2 version. If not — check host/user/pass/TLS.

### 4b. Add a target (host) and generate an API key

In **Settings** → **Targets** → click **Add Target**:

| Field | Value | Description |
|---|---|---|
| ID | `grafana-prod` | automatic, can be changed |
| Host Name | `grafana-alerts` | host name in Icinga2 |
| Source | `grafana` | label of the alerts source |

After saving, click **Generate Key** — copy the generated key. **It is shown only once.**

### 4c. (Optional) Add more targets

E.g., a separate target for Prometheus, a second one for the dev team:

| ID | Host Name | Source |
|---|---|---|
| `grafana-prod` | `grafana-alerts` | `grafana` |
| `prometheus-dev` | `prom-alerts-dev` | `prometheus` |

Each target gets its own API key — alerts are routed to the corresponding host in Icinga2.

---

## Step 5: Connect Grafana (or Prometheus)

### 5a. Grafana — Contact Point

In Grafana: **Alerting** → **Contact points** → **New contact point**:

- **Integration**: Webhook
- **URL**: `http://your-bridge:8080/webhook`
- **HTTP Method**: POST
- **HTTP Header**: `X-API-Key` = `your-copied-api-key`

Click **Test** — if you see `"Webhook received"` in the bridge logs, it works.

### 5b. Prometheus Alertmanager — webhook_config

In `alertmanager.yml`:

```yaml
receivers:
  - name: 'icinga-alertforge'
    webhook_configs:
      - url: 'http://your-bridge:8080/webhook'
        http_config:
          headers:
            X-API-Key: 'your-copied-api-key'
```

The bridge automatically detects the Alertmanager format (by the `version`, `groupKey`, `receiver` fields) and converts it internally.

---

## Step 6: Check if it works end-to-end

Send a test alert manually:

```bash
curl -X POST http://localhost:8080/webhook \
  -H "Content-Type: application/json" \
  -H "X-API-Key: your-copied-api-key" \
  -d '{
    "status": "firing",
    "alerts": [{
      "status": "firing",
      "labels": {"alertname": "Test Alert", "severity": "critical"},
      "annotations": {"summary": "Test with curl - it works!"}
    }]
  }'
```

**Expected response:**
```json
{
  "request_id": "...",
  "host": "grafana-alerts",
  "results": [{
    "status": "processed",
    "service": "Test Alert",
    "exit_status": 2,
    "label": "CRITICAL",
    "icinga_ok": true
  }]
}
```

**In Icinga2** check:
```bash
curl -k -u icinga-alertforge:password \
  https://your-icinga2:5665/v1/objects/services/grafana-alerts!Test%20Alert
```

---

## What happens under the hood (data flow)

```
Grafana/Prometheus → webhook POST → Bridge → Icinga2 API
                                            → History (JSONL)
                                            → SSE (live dashboard)
                                            → Metrics (/metrics)
```

1. The bridge receives a webhook at `/webhook` with the `X-API-Key` header
2. The API key maps to a target → host name in Icinga2
3. The bridge creates a service in Icinga2 (if it's the first time) as a passive check
4. The bridge sends a `process-check-result` with exit_status: 0=OK, 1=WARNING, 2=CRITICAL
5. The result is saved in history, published via SSE to the dashboard, updates metrics

---

## Panel — what else can you do

| Feature | Where in the panel | Description |
|---|---|---|
| Add/remove targets | Settings → Targets | New host in Icinga2 + API key |
| Generate API keys | Settings → Targets → Generate Key | New key for an existing target |
| Reveal keys | Settings → Targets → Reveal Keys | Shows masked keys |
| Change admin password | Settings → Admin | Hot-reload, without restart |
| Test connection | Settings → Test Icinga2 | Checks if the bridge sees Icinga2 |
| Configuration backup | Settings → Export | Downloads encrypted JSON |
| Restore configuration | Settings → Import | Uploads backup JSON |
| Manage users | Admin → Users | RBAC: viewer, operator, admin |
| Freeze alerts | Services → Freeze | Mutes alerts for X seconds or permanently |
| Dev panel (debug) | Dev → Toggle Debug | Preview of requests/responses to the Icinga2 API |

---

## Troubleshooting

| Problem | Check |
|---|---|
| Bridge doesn't start | `ICINGA2_HOST` must be reachable — check firewall and TLS |
| 401 Unauthorized | API key does not match any target |
| 502 Bad Gateway | Icinga2 is not responding — check `ICINGA2_HOST` and credentials |
| "Host does not exist" | Set `ICINGA2_HOST_AUTO_CREATE=true` or create the host manually |
| "Import references unknown template" (500) | Missing `generic-host`/`generic-service` templates in Icinga2 — add them to `/etc/icinga2/conf.d/templates.conf` or disable auto-creation with `ICINGA2_HOST_AUTO_CREATE=false`. See [Troubleshooting](../troubleshooting.md#import-references-unknown-template-http-500) |
| Panel doesn't show Settings | `CONFIG_IN_DASHBOARD=true` is not set |
| Panel changes don't work | Check logs — hot-reload might fail on invalid data |

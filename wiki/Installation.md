# Installation Guide

This guide covers the two main ways to deploy IcingaAlertForge: using Docker Compose (recommended) or as a standalone binary on the host.

---

## Step 0: Prerequisites — Icinga2 API User

Before deploying the bridge, create an API user in Icinga2 with the required permissions.

Create or edit a file in `/etc/icinga2/conf.d/`, for example `iaf-apiuser.conf`:

```conf
object ApiUser "icinga-alertforge" {
  password = "secret-api-pass"

  permissions = [
    "actions/process-check-result",
    "objects/query/Service",
    "objects/create/Service",
    "objects/delete/Service",
    "objects/query/Host",
    "objects/create/Host"
  ]
}
```

Then reload Icinga2:

```bash
sudo systemctl reload icinga2
```

> The `objects/create/Host` and `objects/query/Host` permissions are only needed when `ICINGA2_HOST_AUTO_CREATE=true`. If you create dummy hosts manually, you can omit them.

---

## A. Docker Compose (Recommended)

### Requirements

- Docker Engine 20.10+ and Docker Compose v2 (`docker compose` command)
- Access to the Icinga2 REST API (port 5665)
- Git

### 1. Clone the Repository

```bash
git clone https://github.com/dzaczek/IcingaAlertingForge.git
cd IcingaAlertingForge
```

### 2. Create the Environment File

```bash
cp .env.example .env
```

Open `.env` in your editor and fill in the required values:

```bash
# --- Required ---
ICINGA2_HOST=https://icinga2.example.com:5665   # your Icinga2 URL
ICINGA2_USER=icinga-alertforge                   # API user from Step 0
ICINGA2_PASS=secret-api-pass                     # API password from Step 0
ICINGA2_HOST_AUTO_CREATE=true                    # creates dummy hosts automatically

# --- Admin panel credentials (change these!) ---
ADMIN_USER=admin
ADMIN_PASS=change-me-immediately

# --- Webhook routing: one block per team / source ---
IAF_TARGET_TEAM_A_HOST_NAME=a-dummy-dev
IAF_TARGET_TEAM_A_HOST_DISPLAY=Team A Alerts
IAF_TARGET_TEAM_A_API_KEYS=replace-with-long-random-key
IAF_TARGET_TEAM_A_NOTIFICATION_GROUPS=sms-admins
IAF_TARGET_TEAM_A_NOTIFICATION_SERVICE_STATES=critical,warning

# Add more teams by copying the block above with a different prefix,
# for example IAF_TARGET_TEAM_B_*
```

> **TLS note:** If your Icinga2 uses a self-signed certificate, set `ICINGA2_TLS_SKIP_VERIFY=true`. For production, keep it `false` and ensure your CA is trusted.

For the full list of all available variables, see the [Configuration Reference](../docs/guides/configuration.md).

### 3. Start the Bridge

```bash
docker compose up -d --build
```

Docker Compose will build the image from the local `Dockerfile` and start the container. The service runs on port `8080` by default.

### 4. Check Startup Logs

```bash
docker compose logs -f webhook-bridge
```

A successful startup looks like:

```
{"level":"info","msg":"IcingaAlertForge starting","version":"..."}
{"level":"info","msg":"Icinga2 connection OK"}
{"level":"info","msg":"Listening on 0.0.0.0:8080"}
```

If you see `connection refused` or `401 Unauthorized`, check `ICINGA2_HOST`, `ICINGA2_USER`, and `ICINGA2_PASS` in your `.env`.

### 5. Verify the Health Endpoint

```bash
curl -s http://localhost:8080/health
```

Expected response: `{"status":"ok"}` (or a JSON object with component statuses).

### 6. Open the Admin Panel

Navigate to:

```
http://<your-server>:8080/status/beauty?admin=1
```

Log in with `ADMIN_USER` / `ADMIN_PASS` from your `.env`.

You should see the LCARS dashboard. The **Overview** section shows totals, **Icinga Mgmt** shows managed hosts, and **Settings** (when `CONFIG_IN_DASHBOARD=true`) lets you manage configuration from the UI.

---

## B. Panel Configuration After Startup

### Option 1: Environment variables (default)

Everything is already configured through `.env`. No further action needed in the panel. Restart the container after changing `.env`:

```bash
docker compose restart webhook-bridge
```

### Option 2: Dashboard config mode (manage config from the UI)

Enable it by adding to `.env`:

```env
CONFIG_IN_DASHBOARD=true
# Optional: provide your own encryption key (32+ chars), or one is auto-generated
# CONFIG_ENCRYPTION_KEY=my-secure-key-32-chars-long-!!!
```

On first startup, the bridge migrates your current env vars into a persistent JSON file on the Docker volume. After that, you can change all settings from the **Settings** tab in the admin panel without touching `.env` or restarting.

> Secrets (Icinga2 password, API keys, admin password) are encrypted at rest with AES-256-GCM.

To access Settings:

1. Go to `http://<your-server>:8080/status/beauty?admin=1`
2. Click the **Settings** tab in the sidebar
3. Sections available:
   - **Icinga2 Connection** — host URL, credentials, TLS toggle, test button
   - **Targets & Webhooks** — add/delete targets, generate and copy API keys
   - **Admin Credentials** — change admin username and password
   - **History & Cache** — file paths, max entries, cache TTL
   - **Logging** — log level and format
   - **Rate Limiting** — concurrency and queue limits
   - **Export / Import** — full config backup and restore as JSON

---

## C. Connect Grafana

After the bridge is running, connect Grafana:

1. In Grafana, go to **Alerting → Contact points → Add contact point**
2. Set **Integration** to `Webhook`
3. Set **URL** to `http://<your-server>:8080/webhook`
4. Under **HTTP Headers**, add:
   - Key: `X-API-Key`
   - Value: the key from `IAF_TARGET_<ID>_API_KEYS` in your `.env`
5. Click **Test** → **Send test notification**
6. Open the admin panel — you should see the test alert in **Alerts** and **Real-time Webhook Flow**
7. In Icinga2, a new service named `TestAlert` should appear on the target dummy host

For the full Grafana integration walkthrough, see [Grafana Integration Setup](Grafana-Setup).

---

## D. Manual / Binary Installation

Use this method only if you cannot use Docker.

### Requirements

- Go 1.24 or newer
- A writable directory for data and configuration

### 1. Build from Source

```bash
git clone https://github.com/dzaczek/IcingaAlertingForge.git
cd IcingaAlertingForge
go build -o webhook-bridge .
```

### 2. Configure Environment

```bash
cp .env.example /opt/iaf/.env
# Edit /opt/iaf/.env with your actual values (see Section A, Step 2)
```

### 3. Create Directories and User

```bash
sudo useradd -r -s /bin/false iaf
sudo mkdir -p /opt/iaf /var/log/webhook-bridge
sudo cp webhook-bridge /opt/iaf/
sudo cp .env.example /opt/iaf/.env
sudo chown -R iaf:iaf /opt/iaf /var/log/webhook-bridge
```

### 4. Create a Systemd Service

Create `/etc/systemd/system/icinga-alertforge.service`:

```ini
[Unit]
Description=IcingaAlertForge Webhook Bridge
After=network.target

[Service]
Type=simple
User=iaf
Group=iaf
WorkingDirectory=/opt/iaf
EnvironmentFile=/opt/iaf/.env
ExecStart=/opt/iaf/webhook-bridge
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
```

### 5. Enable and Start

```bash
sudo systemctl daemon-reload
sudo systemctl enable icinga-alertforge
sudo systemctl start icinga-alertforge
```

### 6. Verify

```bash
journalctl -u icinga-alertforge -f
curl -s http://localhost:8080/health
```

---

## Troubleshooting

| Symptom | Likely Cause | Fix |
|---|---|---|
| `connection refused` on health check | Container not started or wrong port | Check `docker compose ps` and `ADMIN_PASS` |
| `401 Unauthorized` in bridge logs | Wrong Icinga2 credentials | Check `ICINGA2_USER` / `ICINGA2_PASS` in `.env` |
| `502 Bad Gateway` from Grafana | Bridge cannot reach Icinga2 port 5665 | Check network / firewall between bridge and Icinga2 |
| Service not created in Icinga2 | Target host missing or wrong API key | Check `ICINGA2_HOST_AUTO_CREATE=true` and that the API key in Grafana matches `.env` |
| Panel shows no data | Browser cache or wrong admin URL | Use `?admin=1` suffix and clear browser cache |
| `tls: failed to verify certificate` | Self-signed Icinga2 cert | Set `ICINGA2_TLS_SKIP_VERIFY=true` (dev only) |

---

## Next Steps

- [Grafana Integration Setup](Grafana-Setup) — connect Grafana contact points
- [Configuration Reference](../docs/guides/configuration.md) — all environment variables explained
- [Beauty Panel](../docs/guides/beauty-panel.md) — admin dashboard guide
- [Fast Track Deployment](../docs/guides/fast-track-deployment.md) — quick smoke test

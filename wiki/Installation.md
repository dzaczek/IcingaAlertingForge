# Installation Guide

This guide covers the two main ways to deploy IcingaAlertForge: using Docker (recommended) or as a standalone binary.

## Prerequisites: Icinga2 API User

Before deploying the bridge you need an API user in Icinga2 with the right permissions.

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

The `objects/create/Host` and `objects/query/Host` permissions are only required when `ICINGA2_HOST_AUTO_CREATE=true`. If you create dummy hosts manually you can omit them.

---

## A. Docker Deployment (Recommended)

Docker is the easiest way to deploy IcingaAlertForge with all its dependencies and proper isolation.

### Prerequisites
- Docker and Docker Compose installed.
- Access to an Icinga2 REST API (see section above).

### `docker-compose.yml` Example

Create a `docker-compose.yml` file with the following content:

```yaml
version: '3.8'

services:
  icinga-alertforge:
    image: icinga-alertforge:latest
    container_name: icinga-alertforge
    restart: unless-stopped
    ports:
      - "8080:8080"
    environment:
      # --- Icinga2 Connection ---
      - ICINGA2_HOST=https://icinga2:5665
      - ICINGA2_USER=icinga-alertforge
      - ICINGA2_PASS=secret-api-pass
      - ICINGA2_TLS_SKIP_VERIFY=false
      - ICINGA2_HOST_AUTO_CREATE=true

      # --- Admin Credentials ---
      - ADMIN_USER=admin
      - ADMIN_PASS=change-me-immediately

      # --- Routing: one block per logical webhook destination ---
      # API key selects the target; all senders use the same /webhook URL.
      - IAF_TARGET_TEAM_A_HOST_NAME=a-dummy-dev
      - IAF_TARGET_TEAM_A_HOST_DISPLAY=Team A Alerts
      - IAF_TARGET_TEAM_A_API_KEYS=replace-with-long-random-key
      - IAF_TARGET_TEAM_A_NOTIFICATION_GROUPS=sms-admins
      - IAF_TARGET_TEAM_A_NOTIFICATION_SERVICE_STATES=critical,warning

      # Add more blocks for additional teams:
      # - IAF_TARGET_TEAM_B_HOST_NAME=b-dummy-dev
      # - IAF_TARGET_TEAM_B_API_KEYS=replace-with-second-key
      # - IAF_TARGET_TEAM_B_NOTIFICATION_GROUPS=mail-admins

      # --- Configuration Mode (optional dashboard config) ---
      - CONFIG_IN_DASHBOARD=false
      # Set to true to manage config from the Beauty Panel instead of env vars.
      # If enabled, uncomment and set these:
      # - CONFIG_FILE_PATH=/var/lib/iaf/config.json
      # - CONFIG_ENCRYPTION_KEY=my-secure-key-32-chars-long-!!!

      # --- Persistence Paths ---
      - HISTORY_FILE=/var/lib/iaf/history.jsonl
      - RETRY_QUEUE_FILE=/var/lib/iaf/retry-queue.json
      - AUDIT_LOG_ENABLED=false
      - AUDIT_LOG_FILE=/var/lib/iaf/audit.log
    volumes:
      - iaf_data:/var/lib/iaf

volumes:
  iaf_data:
```

### Deployment Steps

1. **Build the image:**
    ```bash
    git clone https://github.com/dzaczek/IcingaAlertingForge.git
    cd IcingaAlertingForge
    docker build -t icinga-alertforge .
    ```
2. **Configure environment:** Edit the `environment` section in `docker-compose.yml` with your actual Icinga2 details, secure passwords, and target blocks.
3. **Start the container:**
    ```bash
    docker-compose up -d
    ```
4. **Verify:** Check the logs to ensure the bridge connected to Icinga2 successfully:
    ```bash
    docker logs -f icinga-alertforge
    ```
5. **Health Check:** Access `http://<host>:8080/health` to verify the service status.

---

## B. Manual / Binary Installation

Use this method if you prefer to run IcingaAlertForge as a native system service.

### Prerequisites
- Go 1.24 or newer.
- A writeable directory for logs and configuration.

### 1. Build from Source

```bash
git clone https://github.com/dzaczek/IcingaAlertingForge.git
cd IcingaAlertingForge
go build -o icinga-alert-forge .
```

### 2. Configure Environment

Copy the example and edit it:

```bash
cp .env.example /opt/iaf/.env
```

Minimum required settings:

```env
ICINGA2_HOST=https://icinga2:5665
ICINGA2_USER=icinga-alertforge
ICINGA2_PASS=secret-api-pass
ICINGA2_HOST_AUTO_CREATE=true
ADMIN_USER=admin
ADMIN_PASS=secure-admin-pass

IAF_TARGET_TEAM_A_HOST_NAME=a-dummy-dev
IAF_TARGET_TEAM_A_HOST_DISPLAY=Team A Alerts
IAF_TARGET_TEAM_A_API_KEYS=replace-with-long-random-key
IAF_TARGET_TEAM_A_NOTIFICATION_GROUPS=sms-admins
IAF_TARGET_TEAM_A_NOTIFICATION_SERVICE_STATES=critical,warning
```

Each `IAF_TARGET_<ID>_*` block creates one logical webhook destination. See [Configuration](../docs/guides/configuration.md) for the full reference.

### 3. Running as a Systemd Service

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
ExecStart=/opt/iaf/icinga-alert-forge
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
```

### 4. Enable and Start

```bash
# Create user and set permissions
sudo useradd -r -s /bin/false iaf
sudo mkdir -p /opt/iaf /var/log/webhook-bridge
sudo cp icinga-alert-forge /opt/iaf/
sudo cp .env.example /opt/iaf/.env
sudo chown -R iaf:iaf /opt/iaf /var/log/webhook-bridge

# Edit /opt/iaf/.env with your settings, then:
sudo systemctl daemon-reload
sudo systemctl enable icinga-alertforge
sudo systemctl start icinga-alertforge
```

### 5. Verification

Monitor the systemd logs:

```bash
journalctl -u icinga-alertforge -f
```

Check health:

```bash
curl -s http://localhost:8080/health
```

---

## Next Steps

- [Grafana Integration Setup](Grafana-Setup) — connect Grafana contact points
- [Configuration reference](../docs/guides/configuration.md) — all environment variables
- [Fast Track Deployment](../docs/guides/fast-track-deployment.md) — quick smoke test

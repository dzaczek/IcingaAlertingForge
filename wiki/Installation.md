# Installation Guide

This guide covers the two main ways to deploy IcingaAlertForge: using Docker (recommended) or as a standalone binary.

## A. Docker Deployment (Recommended)

Docker is the easiest way to deploy IcingaAlertForge with all its dependencies and proper isolation.

### Prerequisites
- Docker and Docker Compose installed.
- Access to an Icinga2 REST API.

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
      - ICINGA2_TLS_SKIP_VERIFY=true
      - ICINGA2_HOST_AUTO_CREATE=true

      # --- Admin Credentials ---
      - ADMIN_USER=admin
      - ADMIN_PASS=change-me-immediately

      # --- Configuration Mode ---
      - CONFIG_IN_DASHBOARD=true
      - CONFIG_FILE_PATH=/var/lib/iaf/config.json
      - CONFIG_ENCRYPTION_KEY=my-secure-key-32-chars-long-!!!

      # --- Persistence Paths ---
      - HISTORY_FILE=/var/lib/iaf/history.jsonl
      - RETRY_QUEUE_FILE=/var/lib/iaf/retry-queue.json
      - AUDIT_LOG_FILE=/var/lib/iaf/audit.log
    volumes:
      - iaf_data:/var/lib/iaf

volumes:
  iaf_data:
```

### Deployment Steps
1.  **Configure environment:** Edit the `environment` section in `docker-compose.yml` with your actual Icinga2 details and secure passwords.
2.  **Start the container:**
    ```bash
    docker-compose up -d
    ```
3.  **Verify:** Check the logs to ensure the bridge connected to Icinga2 successfully:
    ```bash
    docker logs -f icinga-alertforge
    ```
4.  **Health Check:** Access `http://<host>:8080/health` to verify the service status.

---

## B. Manual / Binary Installation

Use this method if you prefer to run IcingaAlertForge as a native system service.

### Prerequisites
- Go 1.21 or newer (if building from source).
- A writeable directory for logs and configuration.

### 1. Build from Source
```bash
git clone https://github.com/your-repo/icinga-webhook-bridge.git
cd icinga-webhook-bridge
go build -o icinga-alertforge .
```

### 2. Configure Environment
Create a `.env` file in the same directory as the binary:
```bash
ICINGA2_HOST=https://icinga2:5665
ICINGA2_USER=icinga-alertforge
ICINGA2_PASS=secret-api-pass
ADMIN_PASS=secure-admin-pass
ICINGA2_HOST_NAME=my-dummy-host
WEBHOOK_KEY_TEAM_A=secret-key-a
```

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
ExecStart=/opt/iaf/icinga-alertforge
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
```

### 4. Enable and Start
```bash
# Create user and set permissions
sudo useradd -r -s /bin/false iaf
sudo chown -R iaf:iaf /opt/iaf

# Start service
sudo systemctl daemon-reload
sudo systemctl enable icinga-alertforge
sudo systemctl start icinga-alertforge
```

### 5. Verification
Monitor the systemd logs:
```bash
journalctl -u icinga-alertforge -f
```

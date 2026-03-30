# IcingaAlertForge — User Guide

> **Webhook-to-Icinga2 bridge** — receives alerts from Grafana, Prometheus Alertmanager, or any HTTP source and forwards them to Icinga2 as passive check results. One binary, one direction: webhooks in, Icinga2 passive checks out.

---

## Table of Contents

1. [What Does It Do?](#what-does-it-do)
2. [How It Works — The Big Picture](#how-it-works--the-big-picture)
3. [Key Concepts](#key-concepts)
4. [Getting Started — Step by Step](#getting-started--step-by-step)
5. [Connecting Grafana](#connecting-grafana)
6. [Understanding Targets](#understanding-targets)
7. [Custom Webhooks (Non-Grafana Sources)](#custom-webhooks-non-grafana-sources)
8. [The Dashboard](#the-dashboard)
9. [Users and Permissions](#users-and-permissions)
10. [Test Mode — Safe Experimentation](#test-mode--safe-experimentation)
11. [What Happens When Things Go Wrong](#what-happens-when-things-go-wrong)
12. [FAQ](#faq)

---

## What Does It Do?

IcingaAlertForge is a **bridge that transfers alerts into Icinga2 via webhooks**. The most common source is Grafana, but it also accepts Prometheus Alertmanager payloads and a universal JSON format — so any tool that can send an HTTP POST can push alerts through.

```
Grafana / Alertmanager / curl  ──►  IcingaAlertForge  ──►  Icinga2 Service
      "CPU is high"                   (translates)         "CPU is high" ✓
```

When an alert fires, the bridge creates or updates a passive service in Icinga2. When the alert resolves in the source, the Icinga2 service goes back to OK automatically. No agents, no plugins — just webhooks in, passive checks out.

---

## How It Works — The Big Picture

```
┌─────────────┐         ┌──────────────────────┐         ┌─────────────┐
│   Grafana    │         │   IcingaAlertForge    │         │   Icinga2   │
│              │  HTTP   │                       │  API    │             │
│  Alert fires ├────────►│ 1. Check API key      ├────────►│ Create or   │
│              │ webhook │ 2. Find target        │         │ update      │
│              │         │ 3. Map severity       │         │ service     │
│              │         │ 4. Send to Icinga2    │         │             │
└─────────────┘         └──────────────────────┘         └─────────────┘
```

1. **Grafana** detects a problem and sends a webhook (HTTP POST with alert details)
2. **IcingaAlertForge** checks if the API key is valid
3. It looks up which **target** (Icinga2 host) this key belongs to
4. It translates the alert into an Icinga2 passive check result and sends it
5. **Icinga2** shows the alert on its dashboard

When Grafana sends a "resolved" status, IcingaAlertForge tells Icinga2 the service is OK again.

---

## Key Concepts

### Targets

A **target** represents a destination in Icinga2 — a dummy host that collects alerts from one source. Think of it as a mailbox: all alerts from one team, one application, or one device land in the same mailbox.

**Examples of how you might use targets:**

| Target | Purpose | Who gets notified |
|--------|---------|-------------------|
| `team-devops` | All DevOps alerts (servers, containers, CI/CD) | DevOps on-call team |
| `team-database` | Database alerts (PostgreSQL, MySQL, Redis) | DBA team |
| `device-factory-floor` | Industrial sensors and PLCs | Factory operations team |
| `app-webshop` | Webshop application alerts | Application support |

Each target has:
- A **host name** in Icinga2 (the dummy host where services appear)
- One or more **API keys** (used by Grafana to authenticate)
- Optional **notification settings** (who gets notified in Icinga2)

### API Keys

Every target has at least one API key. When Grafana sends a webhook, it includes the API key in the request header. IcingaAlertForge uses this key to determine which target should receive the alert.

You can have **multiple API keys per target** — for example, one key for production alerts and one for staging. You can also rotate keys without downtime.

### Services

A **service** in Icinga2 is an individual alert. When Grafana sends an alert called "High CPU on web-01", IcingaAlertForge creates a service called "High CPU on web-01" under the target's host. Each unique alert name becomes its own service.

---

## Getting Started — Step by Step

### Prerequisites

- **Docker** and **Docker Compose** installed on your server
- Access to an **Icinga2** instance with the API enabled
- Access to **Grafana** with Unified Alerting enabled (Grafana 9+)

### Step 1: Create the configuration file

Create a file called `.env` with your settings:

```env
# ── Server ──
SERVER_PORT=8080
SERVER_HOST=0.0.0.0

# ── Icinga2 Connection ──
# The URL of your Icinga2 API (usually port 5665, HTTPS)
ICINGA2_HOST=https://your-icinga2-server:5665
ICINGA2_USER=apiuser
ICINGA2_PASS=your-api-password
ICINGA2_TLS_SKIP_VERIFY=true
ICINGA2_HOST_AUTO_CREATE=true

# ── Your First Target ──
# This creates a target called "team-a"
# All alerts sent with the API key "my-secret-key-123" will land here
IAF_TARGET_TEAM_A_HOST_NAME=team-a-alerts
IAF_TARGET_TEAM_A_HOST_DISPLAY=Team A Alerts
IAF_TARGET_TEAM_A_API_KEYS=my-secret-key-123
IAF_TARGET_TEAM_A_NOTIFICATION_USERS=oncall-user

# ── Admin Dashboard ──
ADMIN_USER=admin
ADMIN_PASS=choose-a-strong-password

# ── Dashboard Config Mode (recommended) ──
# Lets you manage settings from the web dashboard instead of editing env vars
CONFIG_IN_DASHBOARD=true

# ── Logging ──
LOG_LEVEL=info
HISTORY_FILE=/var/log/webhook-bridge/history.jsonl
HISTORY_MAX_ENTRIES=10000
```

### Step 2: Create the Docker Compose file

Create `docker-compose.yml`:

```yaml
services:
  webhook-bridge:
    image: icingaalertforge:latest
    # Or build from source:
    # build: .
    ports:
      - "8080:8080"
    env_file:
      - .env
    volumes:
      - webhook_data:/var/log/webhook-bridge
    restart: unless-stopped
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:8080/health"]
      interval: 10s
      timeout: 5s
      retries: 5

volumes:
  webhook_data:
```

### Step 3: Start the service

```bash
docker-compose up -d
```

### Step 4: Verify it's running

Open your browser and go to:

```
http://your-server:8080/status/beauty
```

You should see the LCARS dashboard (Star Trek themed!). The public view shows basic statistics. To access the admin panel, add `?admin=1` to the URL and log in with your admin credentials.

### Step 5: Connect Grafana (see next section)

---

## Connecting Grafana

### Step-by-step: Add a Contact Point in Grafana

1. In Grafana, go to **Alerting → Contact points**
2. Click **+ Add contact point**
3. Fill in the form:

| Field | Value |
|-------|-------|
| **Name** | `IcingaAlertForge` (or any name you like) |
| **Integration** | `Webhook` |
| **URL** | `http://your-server:8080/webhook` |
| **HTTP Method** | `POST` |

4. Under **Optional webhook settings**:
   - Set **Authorization Header** — Credentials to: `my-secret-key-123` (your API key)

   **OR** add a custom header:
   - Header name: `X-API-Key`
   - Header value: `my-secret-key-123`

5. Click **Test** to send a test notification, then **Save**

### Step-by-step: Create a Notification Policy

1. Go to **Alerting → Notification policies**
2. Either edit the default policy or add a new one
3. Set the **Contact point** to `IcingaAlertForge`
4. Save

Now all matching alerts will be forwarded to Icinga2 through IcingaAlertForge.

### Alert Labels That Matter

Grafana sends labels with each alert. IcingaAlertForge uses these labels to decide how to handle the alert:

| Label | Values | What it does |
|-------|--------|-------------|
| `alertname` | any string | **Required.** Becomes the service name in Icinga2 |
| `severity` | `critical`, `warning` | Sets the alert level. Default is `critical` if not set |
| `mode` | `test` | Puts the alert in test mode (no real impact in Icinga2) |

**Example:** If Grafana sends an alert with `alertname=HighCPU` and `severity=warning`, IcingaAlertForge creates a service called "HighCPU" with WARNING status in Icinga2.

### What Happens When an Alert Fires and Resolves

```
Grafana: "HighCPU" fires with severity=critical
  └──► IcingaAlertForge creates service "HighCPU" → Icinga2 status: CRITICAL (red)

Grafana: "HighCPU" resolves
  └──► IcingaAlertForge updates service "HighCPU" → Icinga2 status: OK (green)
```

---

## Understanding Targets

### Multiple Teams Example

Suppose you have two teams — DevOps and Database. Each team has their own Grafana alerts and their own Icinga2 notification contacts.

```env
# ── DevOps Team ──
IAF_TARGET_DEVOPS_HOST_NAME=devops-alerts
IAF_TARGET_DEVOPS_HOST_DISPLAY=DevOps Team Alerts
IAF_TARGET_DEVOPS_API_KEYS=devops-key-abc123
IAF_TARGET_DEVOPS_NOTIFICATION_USERS=alice,bob
IAF_TARGET_DEVOPS_NOTIFICATION_GROUPS=devops-oncall

# ── Database Team ──
IAF_TARGET_DBA_HOST_NAME=dba-alerts
IAF_TARGET_DBA_HOST_DISPLAY=Database Team Alerts
IAF_TARGET_DBA_API_KEYS=dba-key-xyz789
IAF_TARGET_DBA_NOTIFICATION_USERS=charlie
IAF_TARGET_DBA_NOTIFICATION_GROUPS=dba-oncall
```

In Grafana, each team configures their contact point with their own API key:
- DevOps alerts → contact point with key `devops-key-abc123`
- DBA alerts → contact point with key `dba-key-xyz789`

The result in Icinga2:

```
Host: devops-alerts
  ├── Service: HighCPU on web-01        (CRITICAL)
  ├── Service: Disk Full on storage-03  (WARNING)
  └── Service: Container OOM            (OK - resolved)

Host: dba-alerts
  ├── Service: Replication Lag          (WARNING)
  └── Service: Connection Pool Full     (CRITICAL)
```

### Targets for Devices

Targets don't have to represent teams. You can use them for physical devices, locations, or applications:

```env
# ── Factory Floor Sensors ──
IAF_TARGET_FACTORY_HOST_NAME=factory-floor
IAF_TARGET_FACTORY_API_KEYS=factory-sensor-key
IAF_TARGET_FACTORY_NOTIFICATION_GROUPS=factory-ops

# ── Office Building ──
IAF_TARGET_OFFICE_HOST_NAME=office-monitoring
IAF_TARGET_OFFICE_API_KEYS=office-key
IAF_TARGET_OFFICE_NOTIFICATION_USERS=facilities-team
```

### Multiple API Keys Per Target

You can assign multiple API keys to one target. This is useful when:
- Different Grafana instances send to the same target
- You want to rotate keys without downtime
- You want separate keys for production and staging

```env
IAF_TARGET_DEVOPS_API_KEYS=prod-key-111,staging-key-222,backup-key-333
```

All three keys route alerts to the same `devops-alerts` host in Icinga2.

### Managing Targets from the Dashboard

If you enabled `CONFIG_IN_DASHBOARD=true`, you can manage targets from the web UI without restarting the service:

1. Go to `http://your-server:8080/status/beauty?admin=1`
2. Log in with your admin credentials
3. Click **Settings** in the sidebar
4. In the **Targets** section you can:
   - Add new targets
   - Generate new API keys
   - Delete targets
   - Update notification settings

---

## Custom Webhooks (Non-Grafana Sources)

IcingaAlertForge is not limited to Grafana. Any system that can send HTTP POST requests can use it. The service auto-detects three webhook formats:

### 1. Grafana Format (automatic)

Used by Grafana Unified Alerting. No special configuration needed — Grafana sends this format by default.

### 2. Prometheus Alertmanager Format (automatic)

If you use Alertmanager directly (without Grafana), configure a webhook receiver:

```yaml
# alertmanager.yml
receivers:
  - name: 'icinga'
    webhook_configs:
      - url: 'http://your-server:8080/webhook'
        http_config:
          authorization:
            credentials: 'your-api-key'
```

### 3. Universal Format (for custom scripts and tools)

For your own scripts, CI/CD pipelines, IoT devices, or any custom integration, send a simple JSON payload:

```bash
curl -X POST http://your-server:8080/webhook \
  -H "X-API-Key: your-api-key" \
  -H "Content-Type: application/json" \
  -d '{
    "alerts": [
      {
        "name": "Backup Failed",
        "status": "firing",
        "severity": "critical",
        "message": "Nightly backup of production DB failed at 03:00"
      }
    ]
  }'
```

To resolve the alert later:

```bash
curl -X POST http://your-server:8080/webhook \
  -H "X-API-Key: your-api-key" \
  -H "Content-Type: application/json" \
  -d '{
    "alerts": [
      {
        "name": "Backup Failed",
        "status": "resolved",
        "message": "Backup completed successfully"
      }
    ]
  }'
```

### Universal Format Fields

| Field | Required | Values | Description |
|-------|----------|--------|-------------|
| `name` | Yes | any string | The alert name — becomes the Icinga2 service name |
| `status` | Yes | `firing` or `resolved` | Whether the problem is active or fixed |
| `severity` | No | `critical`, `warning` | Alert level. Default: `critical` |
| `message` | No | any string | Human-readable description of the problem |
| `labels` | No | key-value pairs | Extra metadata (for your reference) |
| `annotations` | No | key-value pairs | Additional notes (e.g., runbook links) |

### Practical Examples

**IoT sensor alert:**
```json
{
  "alerts": [{
    "name": "Temperature Sensor Room-B",
    "status": "firing",
    "severity": "warning",
    "message": "Temperature 38°C exceeds threshold of 35°C"
  }]
}
```

**CI/CD pipeline failure:**
```json
{
  "alerts": [{
    "name": "Deploy Pipeline main",
    "status": "firing",
    "severity": "critical",
    "message": "Production deploy failed: exit code 1 in step 'migration'"
  }]
}
```

**Batch job monitoring:**
```json
{
  "alerts": [
    {
      "name": "ETL Daily Import",
      "status": "firing",
      "severity": "warning",
      "message": "Import took 45 min (threshold: 30 min)"
    },
    {
      "name": "ETL Data Validation",
      "status": "firing",
      "severity": "critical",
      "message": "1,247 rows failed validation checks"
    }
  ]
}
```

You can send multiple alerts in a single request. Each one becomes a separate service in Icinga2.

---

## The Dashboard

The dashboard is available at `/status/beauty`. It has a Star Trek LCARS theme and provides a complete overview of the system.

### Public View (no login required)

Visit `http://your-server:8080/status/beauty` to see:
- System uptime and version
- Total alerts processed
- Alert counts by severity (OK, Warning, Critical)
- Error count

### Admin View

Visit `http://your-server:8080/status/beauty?admin=1` and log in to access:

| Section | What it shows |
|---------|--------------|
| **Overview** | Live statistics, alert counters, severity breakdown |
| **System** | Memory usage, CPU, goroutines, request rates |
| **Alerts** | Last 20 transmissions with status, host, service, source, and timing |
| **Errors** | Last 10 failed alerts — helps troubleshoot connectivity issues |
| **Services** | All services currently in Icinga2 with live status and management actions |
| **Security** | Failed login attempts, IP tracking, API key usage per source |
| **Icinga Mgmt** | Service status management — change status, delete, bulk operations |
| **Settings** | Configuration panel (if `CONFIG_IN_DASHBOARD=true`) |
| **Dev Panel** | Real-time debug stream showing every API call to Icinga2 |
| **About** | Setup guide and version info |

### Live Updates

The dashboard updates in real-time. When a new alert arrives:
- The top panel shows animated indicators
- Counters increment automatically
- The Alerts table refreshes with the latest entries

No need to manually refresh the page.

---

## Users and Permissions

IcingaAlertForge has role-based access control (RBAC) with three roles:

### Roles

| Role | What they can do |
|------|-----------------|
| **Viewer** | See the dashboard, view history, view service status |
| **Operator** | Everything a Viewer can + change service status, flush retry queue, clear history, toggle debug mode |
| **Admin** | Everything an Operator can + delete services, manage settings, manage users |

### The Primary Admin

The primary admin account is defined in the environment variables (`ADMIN_USER` and `ADMIN_PASS`). This account:
- Always has full admin permissions
- Cannot be deleted from the dashboard
- Credentials are only changed by updating the environment variables and restarting

### Adding More Users

Admins can create additional users from the dashboard:

1. Go to `http://your-server:8080/status/beauty?admin=1`
2. Navigate to the **Security** section
3. In the **User Management** panel:
   - Enter a username and password
   - Select a role (Viewer, Operator, or Admin)
   - Click **Add User**

These users can log in to the admin dashboard. Their credentials are stored securely (encrypted) and persist across restarts.

### Example: Team Setup

| User | Role | Purpose |
|------|------|---------|
| `admin` | Admin | Primary admin (env-based, cannot be deleted) |
| `ops-lead` | Admin | Second admin for backup |
| `oncall-1` | Operator | On-call engineer — can change statuses and manage queue |
| `oncall-2` | Operator | Another on-call engineer |
| `manager` | Viewer | Team lead — read-only overview of alert status |

---

## Test Mode — Safe Experimentation

Test mode lets you verify your setup without affecting real Icinga2 monitoring.

### How to Use Test Mode

In Grafana, add these labels to your alert rule:
- `mode`: `test`
- `test_action`: `create` (to create a test service) or `delete` (to remove it)

Or via curl:

```bash
# Create a test service
curl -X POST http://your-server:8080/webhook \
  -H "X-API-Key: your-api-key" \
  -H "Content-Type: application/json" \
  -d '{
    "alerts": [{
      "labels": {
        "alertname": "TestAlert",
        "mode": "test",
        "test_action": "create"
      },
      "status": "firing",
      "annotations": {"summary": "Testing the connection"}
    }]
  }'

# Delete the test service
curl -X POST http://your-server:8080/webhook \
  -H "X-API-Key: your-api-key" \
  -H "Content-Type: application/json" \
  -d '{
    "alerts": [{
      "labels": {
        "alertname": "TestAlert",
        "mode": "test",
        "test_action": "delete"
      },
      "status": "firing"
    }]
  }'
```

Test events are logged separately in the transmission history (marked as "TEST"), so you can easily distinguish them from real alerts.

---

## What Happens When Things Go Wrong

### Icinga2 is Unreachable

If IcingaAlertForge cannot reach Icinga2:
- The alert is placed in a **retry queue**
- The queue retries with increasing delays (5s, 10s, 20s, ... up to 5 minutes)
- Up to 1,000 alerts can be queued
- The queue is saved to disk — alerts survive a restart
- You can see queue status on the dashboard
- You can force a flush (retry all immediately) from the dashboard

### Invalid API Key

If a webhook arrives with an unknown API key:
- The request is rejected with HTTP 401
- The failed attempt is logged in the Security section of the dashboard
- No data reaches Icinga2

### Health Monitoring

IcingaAlertForge monitors its own health:
- It periodically checks connectivity to Icinga2
- After 3 consecutive failures, it marks itself as unhealthy
- It can register itself as a service in Icinga2 — so if the bridge goes down, Icinga2 will alert you
- The `/health` endpoint shows the current health status

---

## FAQ

### Can I use this without Grafana?

Yes! Any tool that can send HTTP POST requests can use IcingaAlertForge. Use the [Universal Format](#3-universal-format-for-custom-scripts-and-tools) to send alerts from scripts, CI/CD pipelines, IoT devices, or any custom application.

### Can one Grafana alert go to multiple targets?

Not directly with a single API key — each key routes to one target. But you can:
- Create multiple contact points in Grafana, each with a different API key
- Use Grafana notification policies to route different alerts to different contact points

### Do I need to create hosts in Icinga2 manually?

No. Set `ICINGA2_HOST_AUTO_CREATE=true` and IcingaAlertForge will create dummy hosts automatically when the first alert for a target arrives.

### Do I need to create services in Icinga2 manually?

No. Services are created automatically when alerts arrive. If Grafana sends an alert called "High CPU", a service called "High CPU" appears in Icinga2 under the target host.

### What happens to services when alerts resolve?

The service stays in Icinga2 but changes to OK (green) status. It is not deleted — this way you can see the history of all alerts that have ever fired.

### How do I delete old services?

From the admin dashboard:
1. Go to the **Icinga Mgmt** or **Services** section
2. Select the services you want to remove
3. Click **Delete** or use **Bulk Delete**

### Can I change the admin password?

The primary admin password is set via the `ADMIN_PASS` environment variable. To change it:
1. Update the value in your `.env` file
2. Restart the container: `docker-compose restart webhook-bridge`

### How do I back up the configuration?

If you use `CONFIG_IN_DASHBOARD=true`:
1. Go to **Settings** in the admin dashboard
2. Click **Export Configuration**
3. Save the JSON file

To restore: click **Import Configuration** and upload the file.

### Where is data stored?

| File | Location | Content |
|------|----------|---------|
| `config.json` | `/var/log/webhook-bridge/` | Dashboard configuration, targets, RBAC users |
| `history.jsonl` | `/var/log/webhook-bridge/` | Transmission history (all processed alerts) |
| `retry-queue.json` | `/var/log/webhook-bridge/` | Pending retries (failed Icinga2 calls) |

All files are in the Docker volume. They persist across container restarts.

### How do I see what API calls are being made to Icinga2?

Open the **Dev Panel** in the admin dashboard. It shows a real-time stream of every API call with request/response details.

---

## Quick Reference: Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `SERVER_PORT` | `8080` | HTTP port |
| `ICINGA2_HOST` | — | Icinga2 API URL (e.g., `https://icinga:5665`) |
| `ICINGA2_USER` | — | Icinga2 API username |
| `ICINGA2_PASS` | — | Icinga2 API password |
| `ICINGA2_HOST_AUTO_CREATE` | `false` | Auto-create missing Icinga2 hosts |
| `ICINGA2_TLS_SKIP_VERIFY` | `false` | Skip HTTPS certificate verification |
| `ADMIN_USER` | `admin` | Admin username for the dashboard |
| `ADMIN_PASS` | — | Admin password (empty = admin panel disabled) |
| `CONFIG_IN_DASHBOARD` | `false` | Enable web-based configuration |
| `LOG_LEVEL` | `info` | Log verbosity: `debug`, `info`, `warn`, `error` |
| `HISTORY_MAX_ENTRIES` | `10000` | Max history entries before rotation |
| `IAF_TARGET_{ID}_HOST_NAME` | — | Icinga2 host name for this target |
| `IAF_TARGET_{ID}_API_KEYS` | — | Comma-separated API keys |
| `IAF_TARGET_{ID}_NOTIFICATION_USERS` | — | Icinga2 notification users |
| `IAF_TARGET_{ID}_NOTIFICATION_GROUPS` | — | Icinga2 notification groups |

For the complete list of all environment variables, see the [Configuration Reference](guides/configuration.md).

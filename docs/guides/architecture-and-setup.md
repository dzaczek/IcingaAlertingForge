# Architecture and Setup

Back to the [Documentation Index](../README.md)

## Overview

IcingaAlertForge sits between Grafana and Icinga2.

Grafana is still the place where alerts are easy to create and change. Icinga stays focused on the alerts that are important enough to deserve a place in a stricter monitoring system. The bridge receives webhooks from Grafana, decides which Icinga dummy host should receive them, makes sure the required host or service exists, and then sends passive check results to Icinga2.

This direction matters:

```text
Grafana -> Icinga2
```

The bridge does not try to make Icinga the source of truth for those alerts. Grafana remains the source. Icinga is the destination used for presentation, state tracking, and notification handling.

## Architecture

```mermaid
flowchart LR
    subgraph Grafana
        A["Unified Alerting"]
        B["Contact Point: Team A"]
        C["Contact Point: Team B"]
    end

    subgraph Bridge["IcingaAlertForge"]
        WH["POST /webhook"]
        AUTH["API key validation"]
        ROUTE["Route key -> target"]
        WORK["Work mode"]
        TEST["Test mode"]
        CACHE[("Per-host service cache")]
        HIST[("history.jsonl")]
        PANEL["/status/beauty"]
        ADMIN["/admin/services"]
    end

    subgraph Icinga
        API["Icinga2 REST API"]
        HOSTA["Host: a-dummy-dev"]
        HOSTB["Host: b-dummy-device"]
    end

    A --> B --> WH
    A --> C --> WH
    WH --> AUTH --> ROUTE
    ROUTE --> WORK
    ROUTE --> TEST
    WORK --> API
    TEST --> API
    API --> HOSTA
    API --> HOSTB
    WH --> CACHE
    WH --> HIST
    CACHE --> PANEL
    HIST --> PANEL
    API --> ADMIN
```

```mermaid
flowchart TD
    W["Webhook with API key"] --> K{"Key valid?"}
    K -->|No| U["401 unauthorized"]
    K -->|Yes| T["Resolve target"]
    T --> H["Target host from config"]
    H --> M{"mode == test?"}
    M -->|No| WM["Map firing/resolved -> passive check result"]
    M -->|Yes| TM["Create or delete service"]
    WM --> S["Ensure service exists on target host"]
    S --> P["Send process-check-result"]
    TM --> C["Create/Delete service on target host"]
    P --> R["History + metrics + cache"]
    C --> R
```

## What The Service Contains

- one Icinga2 API client
- routing for several configured hosts
- one or more API keys for each host
- a cache keyed by `host + service`
- a JSONL history log
- a public and admin status panel
- admin endpoints for listing and deleting managed services

## Features

- routing for several teams and dummy hosts
- more than one API key for the same host or team
<!-- LANG: hyphenation -->
- host-specific notification settings
- dynamic dummy host creation on startup
- dynamic service creation in work mode
- test mode for manual create and delete actions
- JSONL history with filters
- a service cache with TTL and cleanup
- an admin API and a live panel
<!-- CHANGED: added SSE broker, debug ring buffer, and metrics features -->
- SSE broker for real-time event streaming to the dashboard
- debug ring buffer for API traffic inspection
- metrics collector with brute-force detection
- security headers (X-Content-Type-Options, X-Frame-Options, X-XSS-Protection, Referrer-Policy, Permissions-Policy)
- XSS input validation on target creation
- dashboard-based configuration with AES-256-GCM encrypted secrets at rest
- config export/import backup with full secret restore
- hot-reload on configuration changes without restart
- a ready-to-use lab in `testenv`

## Requirements

- Go 1.24+
- Icinga2 with the REST API enabled on port `5665`
- Grafana Unified Alerting
- Docker and Compose if you want to use the bundled test environment

## Icinga2 API Permissions

The API user must be allowed to do the following:

```conf
permissions = [
  "actions/process-check-result",
  "objects/query/Service",
  "objects/create/Service",
  "objects/delete/Service",
  "objects/query/Host",
  "objects/create/Host"
]
```

If host auto creation is enabled, `objects/query/Host` and `objects/create/Host` are required as well.

## Installation

### From Source

```bash
git clone https://github.com/dzaczek/IcingaAlertingForge.git
cd IcingaAlertingForge

cp .env.example .env
# edit .env

go build -o icinga-alert-forge .
./icinga-alert-forge
```

### Docker

```bash
docker build -t icinga-alert-forge .

docker run -d \
  --name icinga-alert-forge \
  -p 8080:8080 \
  --env-file .env \
  -v webhook-logs:/var/log/webhook-bridge \
  icinga-alert-forge
```

### Docker Compose

```bash
docker compose up -d --build
```

If your Docker installation still uses the older `docker-compose` binary, that works too.

## Next Step

Continue with [Configuration](configuration.md).

## Architecture Diagrams

### System Overview

```mermaid
graph LR
    A[Grafana] -->|webhook POST| B[IcingaAlertForge]
    C[Alertmanager] -->|webhook POST| B
    D[custom/curl] -->|universal JSON| B
    B -->|passive check| E[Icinga2]
    B -->|metrics| F[Prometheus]
    E -->|notifications| G[Email/Slack/PagerDuty]
```

### Webhook Flow

```mermaid
sequenceDiagram
    participant G as Grafana
    participant B as Bridge
    participant I as Icinga2 API
    participant Q as Retry Queue

    G->>B: POST /webhook (JSON)
    B->>B: Validate API key
    B->>B: Parse alert(s)
    B->>I: process-check-result
    alt Success
        I-->>B: 200 OK
    else Failure
        I-->>B: Error
        B->>Q: Enqueue retry
    end
    B-->>G: 200 OK
```

### Component Map

| Component | Package | Responsibility |
|-----------|---------|---------------|
| HTTP Server | `main.go` | Router, middleware, shutdown |
| Auth | `auth/` | API key validation |
| Webhook | `handler/webhook.go` | Alert ingestion |
| Admin API | `handler/admin.go` | Dashboard REST API |
| Icinga2 Client | `icinga/` | REST API client |
| Models | `models/` | Grafana/Alertmanager types |
| Cache | `cache/` | Service state, freeze |
| Retry Queue | `queue/` | Exponential backoff |
| History | `history/` | JSONL alert history |
| Audit | `audit/` | JSON/CEF audit log |
| RBAC | `rbac/` | Role-based access |
| Metrics | `metrics/` | Prometheus collector |
| Health | `health/` | Reverse health checker |

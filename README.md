# IcingaAlertForge

![IcingaAlertForge header](docs/img/header.png)

**A webhook-to-Icinga2 bridge** — receives alerts from Grafana, Alertmanager, or any HTTP source and forwards them to Icinga2 as passive check results.

```
Grafana / Alertmanager / curl  ──►  IcingaAlertForge  ──►  Icinga2
        (webhook POST)                (bridge)              (passive checks)
```

The alert flow is **one-way**: sources send webhooks in, IcingaAlertForge translates them, and Icinga2 takes over presentation, tracking, and notifications. No agents, no plugins — just a single binary that connects your alerting tools to Icinga2.

## Why

Grafana is great for creating and adjusting alerts. Icinga2 is great for operations — structured dashboards, notification routing, escalation. This bridge lets you keep both and connect them without migrating anything.

This is a hobby project, developed mainly on weekends.

## What It Does

- receives webhooks from **Grafana Unified Alerting**, **Prometheus Alertmanager**, or any tool that sends HTTP POST (universal JSON format)
- authenticates requests with API keys
- routes alerts to the right target host in Icinga2 based on the API key
- auto-creates dummy hosts and services in Icinga2 when needed
- writes passive check results (OK / WARNING / CRITICAL)
- when an alert resolves in the source, the Icinga2 service goes back to OK automatically
- keeps transmission history, retry queue, and admin dashboard

## Key Features

- **multi-source**: Grafana, Alertmanager, custom scripts, CI/CD, IoT — anything that can POST JSON
- **multi-target**: route alerts from different teams or sources to separate Icinga2 hosts
- **multiple API keys** per target (rotation, staging/prod separation)
- **retry queue** with exponential backoff when Icinga2 is unreachable
- **RBAC** with viewer / operator / admin roles
- **LCARS dashboard** (Star Trek themed) with real-time updates via SSE
- **test mode** for safe experimentation without affecting production monitoring
- bundled **test environment** with Grafana, Prometheus, Icinga2, and Icinga Web 2

## Documentation

The full documentation now lives in [docs/README.md](docs/README.md).

Start here:

- [Documentation Index](docs/README.md)
- [Fast Track Deployment](docs/guides/fast-track-deployment.md)
- [Architecture and Setup](docs/guides/architecture-and-setup.md)
- [Configuration](docs/guides/configuration.md)
- [Usage and API](docs/guides/usage-and-api.md)
- [Icinga Integration](docs/guides/icinga-integration.md)
- [Beauty Panel](docs/guides/beauty-panel.md)
- [Test Environment](docs/guides/test-environment.md)
- [Development and Operations](docs/guides/development-and-operations.md)

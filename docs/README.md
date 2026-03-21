# Documentation Index

This folder holds the full documentation for IcingaAlertForge. The top level [README](../README.md) only gives a short project introduction. Everything else lives here.

## Reading Order

If you are new to the project, this order usually works best:

1. [Fast Track Deployment](guides/fast-track-deployment.md)
2. [Architecture and Setup](guides/architecture-and-setup.md)
3. [Configuration](guides/configuration.md)
4. [Usage and API](guides/usage-and-api.md)
5. [Icinga Integration](guides/icinga-integration.md)
6. [Test Environment](guides/test-environment.md)

## Guides

- [Fast Track Deployment](guides/fast-track-deployment.md)
  The shortest path to a working deployment with a minimal `.env`, Docker run command, smoke test, and first Grafana contact point.

- [Architecture and Setup](guides/architecture-and-setup.md)
  What the service does, how the pieces fit together, what it needs, and how to run it.

- [Configuration](guides/configuration.md)
  Environment variables, the multi host routing model, naming rules, migration from the old single host setup, and what the bridge writes into Icinga.

- [Usage and API](guides/usage-and-api.md)
  Work mode, test mode, Grafana contact points, request authentication, and the HTTP endpoints.

- [Icinga Integration](guides/icinga-integration.md)
  Dynamic host creation, service creation, notification variables, cache behaviour, history, and ghost cleanup.

- [Beauty Panel](guides/beauty-panel.md)
  Public and admin views, multi host behaviour, and panel navigation.

- [Test Environment](guides/test-environment.md)
  The bundled lab stack, endpoints, synthetic alerts, notification rules, resets, cleanup, and screenshots.

- [Development and Operations](guides/development-and-operations.md)
  Project structure, test commands, manual checks, and the practical limits you should keep in mind.

## Reference

- [Load Test Results](reference/load-test-results.md)
- [Environment Example](../.env.example)

## Quick Links

- [Project README](../README.md)
- [Test Environment Compose File](../testenv/docker-compose.yml)
- [Synthetic Alert Generator](../testenv/scripts/generate_flapping_alert_rules.sh)
- [Managed Service Cleanup Script](../testenv/scripts/purge_host_services.sh)

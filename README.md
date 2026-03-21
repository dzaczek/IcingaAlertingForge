# IcingaAlertForge

![IcingaAlertForge header](docs/img/header.png)

IcingaAlertForge is a small Go service that takes webhook alerts from Grafana and forwards them to Icinga2 as passive checks.

It is a one way alert bridge from Grafana to Icinga2. The idea is simple: alerts are defined in Grafana, and Icinga is used as the place where those alerts are presented, tracked, and notified further.

This project started out of a fairly noble kind of laziness. I did not want to migrate everything into one monitoring system, and I did not want Icinga to carry every personal or experimental alert that lives in Grafana at home. Grafana stays flexible. Anyone in the house can create their own alerts there. Icinga stays focused on the handful of things that are truly critical. This bridge is the small piece in the middle that connects the two.

In practice, that means Grafana remains the place where alerts are easy to create and change, while Icinga becomes the place that watches the important problems that should not be missed. In other words, this project exists to push Grafana alerts into Icinga2, not the other way around.

This is a hobby project. It is developed mainly on weekends, so fixes and larger changes tend to land in batches rather than on a strict release schedule.

## What It Does

- receives Grafana Unified Alerting webhooks
- authenticates requests with API keys
- routes alerts to the right dummy host in Icinga
- creates missing hosts and services when needed
- writes passive check results into Icinga2
- lets Icinga handle alert presentation and notification logic for alerts that started in Grafana
- keeps history, cache state, and admin views in one place

## What It Supports

- more than one team or alert source
- more than one API key for the same host or team
- host specific notification settings in Icinga
- a test environment with Grafana, Prometheus, Icinga2, and Icinga Web 2
- an admin and status panel for live inspection

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

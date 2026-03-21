# Configuration

Back to the [Documentation Index](../README.md)

## Overview

All configuration comes from environment variables or a local `.env` file. The full example lives in [`.env.example`](../../.env.example).

The current model is built around managed dummy hosts. Each configured host gets its own routing rules, its own API keys, and its own notification settings.

## Core Settings

| Variable | Required | Default | Description |
|---|---|---|---|
| `SERVER_PORT` | No | `8080` | HTTP port |
| `SERVER_HOST` | No | `0.0.0.0` | HTTP bind address |
| `ICINGA2_HOST` | Yes | — | Icinga2 API base URL, for example `https://icinga2.example.com:5665` |
| `ICINGA2_USER` | Yes | — | Icinga2 API user |
| `ICINGA2_PASS` | Yes | — | Icinga2 API password |
| `ICINGA2_HOST_AUTO_CREATE` | No | `false` | Create configured dummy hosts if they do not exist |
| `ICINGA2_TLS_SKIP_VERIFY` | No | `false` | Skip TLS verification |
| `HISTORY_FILE` | No | `/var/log/webhook-bridge/history.jsonl` | JSONL history file |
| `HISTORY_MAX_ENTRIES` | No | `10000` | Rotation limit for history |
| `CACHE_TTL_MINUTES` | No | `60` | TTL for the in memory service cache |
| `LOG_LEVEL` | No | `info` | `debug`, `info`, `warn`, `error` |
| `LOG_FORMAT` | No | `json` | `json` or `text` |
| `ADMIN_USER` | No | `admin` | Admin username |
| `ADMIN_PASS` | No | empty | Admin password. If empty, admin APIs are disabled |
| `RATELIMIT_MUTATE_MAX` | No | `5` | Concurrent create and delete operations |
| `RATELIMIT_STATUS_MAX` | No | `20` | Concurrent status update operations |
| `RATELIMIT_MAX_QUEUE` | No | `100` | Maximum queued status jobs |

## Host Routing Model

Each target block in the environment describes one managed dummy host in Icinga, plus the API keys and notification settings that belong to it.

The target ID is derived from the variable name:

- `IAF_TARGET_TEAM_A_*` becomes target ID `team-a`
- `IAF_TARGET_B_DUMMY_DEVICE_*` becomes target ID `b-dummy-device`

The target ID and `SOURCE` do not need to match.

Example:

```env
IAF_TARGET_TEAM_A_HOST_NAME=a-dummy-dev
IAF_TARGET_TEAM_A_SOURCE=alerts-dev-a
```

This means:

- target ID = `team-a`
- source stored in logs and history = `alerts-dev-a`

If `SOURCE` is missing, the bridge falls back to the normalized target ID.

## Target Variables

| Variable | Required | Default | Description |
|---|---|---|---|
| `IAF_TARGET_<ID>_HOST_NAME` | Yes | — | Icinga host name created or used for this target |
| `IAF_TARGET_<ID>_HOST_DISPLAY` | No | `<HOST_NAME>` | Host display name |
| `IAF_TARGET_<ID>_HOST_ADDRESS` | No | empty | Optional metadata stored as `vars.iaf_host_address` |
| `IAF_TARGET_<ID>_API_KEYS` | Yes | — | Comma separated webhook keys for this host |
| `IAF_TARGET_<ID>_SOURCE` | No | normalized target ID | Source label stored in logs and history |
| `IAF_TARGET_<ID>_NOTIFICATION_USERS` | No | empty | Comma separated Icinga users |
| `IAF_TARGET_<ID>_NOTIFICATION_GROUPS` | No | empty | Comma separated Icinga groups and user groups |
| `IAF_TARGET_<ID>_NOTIFICATION_SERVICE_STATES` | No | empty | Comma separated service states such as `critical,warning` |
| `IAF_TARGET_<ID>_NOTIFICATION_HOST_STATES` | No | empty | Comma separated host states such as `down` |

Important details:

- API keys must be unique across the whole deployment
- more than one key can point to the same host
- one incoming key always resolves to exactly one host
- `NOTIFICATION_GROUPS` is written into both `groups` and `user_groups`

## Naming Rules

There are four names that are easy to mix up:

| Concept | Example | Where it comes from | What it is for |
|---|---|---|---|
| Target ID | `team-a` | `IAF_TARGET_TEAM_A_*` | internal routing key |
| Source | `alerts-dev-a` | `IAF_TARGET_TEAM_A_SOURCE` | logs, history, API output |
| Host name | `a-dummy-dev` | `IAF_TARGET_TEAM_A_HOST_NAME` | real Icinga host object |
| API key | `key-a-1` | `IAF_TARGET_TEAM_A_API_KEYS` | request authentication and routing |

A sensible rule is to keep the target ID, `SOURCE`, and host name close to each other unless you need compatibility with an older setup.

Example:

```env
IAF_TARGET_KONEKTS_A_SOURCE=alerts-dev-a
IAF_TARGET_KONEKTS_A_HOST_NAME=a-dummy-dev
IAF_TARGET_KONEKTS_A_API_KEYS=key-a-1,key-a-2
IAF_TARGET_KONEKTS_A_NOTIFICATION_GROUPS=sms-alfa,sms-omega
```

This means:

- target ID = `konekts-a`
- source in logs = `alerts-dev-a`
- Icinga host object = `a-dummy-dev`
- both API keys go to the same host and the same notification settings

## Two Team Example

```env
SERVER_PORT=8080
SERVER_HOST=0.0.0.0

ICINGA2_HOST=https://icinga2.example.com:5665
ICINGA2_USER=apiuser
ICINGA2_PASS=supersecret
ICINGA2_HOST_AUTO_CREATE=true
ICINGA2_TLS_SKIP_VERIFY=false

IAF_TARGET_TEAM_A_SOURCE=team-a
IAF_TARGET_TEAM_A_HOST_NAME=a-dummy-dev
IAF_TARGET_TEAM_A_HOST_DISPLAY=Team A Dummy Device
IAF_TARGET_TEAM_A_API_KEYS=key-a-1,key-a-2
IAF_TARGET_TEAM_A_NOTIFICATION_GROUPS=sms-alfa,sms-omega
IAF_TARGET_TEAM_A_NOTIFICATION_SERVICE_STATES=critical
IAF_TARGET_TEAM_A_NOTIFICATION_HOST_STATES=down

IAF_TARGET_TEAM_B_SOURCE=team-b
IAF_TARGET_TEAM_B_HOST_NAME=b-dummy-device
IAF_TARGET_TEAM_B_HOST_DISPLAY=Team B Dummy Device
IAF_TARGET_TEAM_B_API_KEYS=key-b-1,key-b-2
IAF_TARGET_TEAM_B_NOTIFICATION_GROUPS=sms-beta,sms-ceta
IAF_TARGET_TEAM_B_NOTIFICATION_SERVICE_STATES=critical
IAF_TARGET_TEAM_B_NOTIFICATION_HOST_STATES=down

ADMIN_USER=admin
ADMIN_PASS=change-me
```

If you prefer direct users instead of groups:

```env
IAF_TARGET_TEAM_A_NOTIFICATION_USERS=alpha,omega
IAF_TARGET_TEAM_B_NOTIFICATION_USERS=beta,ceta
```

## Legacy Single Host Mode

The old setup still works:

```env
WEBHOOK_KEY_GRAFANA_PROD=secret-prod
WEBHOOK_KEY_GRAFANA_DEV=secret-dev

ICINGA2_HOST_NAME=grafana-alerts
ICINGA2_HOST_DISPLAY=Grafana Alerts
ICINGA2_HOST_ADDRESS=
```

Rules in legacy mode:

- at least one `WEBHOOK_KEY_*` is required
- all keys go to one shared host
- `WEBHOOK_KEY_<NAME>` still becomes a source name by lowercasing and replacing `_` with `-`

If any `IAF_TARGET_*` variables exist, the bridge switches to the new host based routing model.

## Migrating From The Old Setup

Use this path when moving from one shared host to several managed dummy hosts:

1. Pick one target block for each team or notification domain.
2. Create one `IAF_TARGET_<ID>_HOST_NAME` for each dummy host.
3. Move the old webhook secrets into the new `IAF_TARGET_<ID>_API_KEYS` values.
4. Decide whether each host should use `NOTIFICATION_USERS` or `NOTIFICATION_GROUPS`.
5. Turn on `ICINGA2_HOST_AUTO_CREATE=true` if the bridge should create missing hosts for you.
6. Remove or comment the old `WEBHOOK_KEY_*` values after the senders have been moved.

Migration example:

```env
# old
WEBHOOK_KEY_GRAFANA_A=legacy-a
WEBHOOK_KEY_GRAFANA_B=legacy-b
ICINGA2_HOST_NAME=grafana-alerts

# new
IAF_TARGET_TEAM_A_HOST_NAME=a-dummy-dev
IAF_TARGET_TEAM_A_API_KEYS=legacy-a
IAF_TARGET_TEAM_A_NOTIFICATION_GROUPS=sms-alfa,sms-omega

IAF_TARGET_TEAM_B_HOST_NAME=b-dummy-device
IAF_TARGET_TEAM_B_API_KEYS=legacy-b
IAF_TARGET_TEAM_B_NOTIFICATION_GROUPS=sms-beta,sms-ceta
```

Important notes:

- the presence of any `IAF_TARGET_*` variable switches the bridge into the new routing model
- duplicate API keys across targets are rejected on startup
- old services on the historical shared host are not moved automatically
- if you care about old source names, set `IAF_TARGET_<ID>_SOURCE` explicitly

## What The Bridge Writes Into Icinga

When a host is created automatically, the bridge writes:

```text
vars.managed_by = "IcingaAlertingForge"
vars.iaf_managed = true
vars.iaf_component = "IcingaAlertingForge"
vars.iaf_created_at = "<RFC3339>"
vars.iaf_host_address = "<HOST_ADDRESS if configured>"
```

Notification variables are written in a neutral form:

```text
vars.notification.users
vars.notification.groups
vars.notification.user_groups
vars.notification.service_states
vars.notification.host_states
```

Alias trees are also written for convenience:

```text
vars.notification.mail.*
vars.notification.sms.*
```

Services created by the bridge carry the following markers and metadata:

```text
vars.managed_by = "IcingaAlertingForge"
vars.iaf_managed = true
vars.iaf_component = "IcingaAlertingForge"
vars.iaf_host = "<host>"
vars.iaf_created_at = "<RFC3339>"
vars.bridge_host = "<host>"
vars.bridge_created_at = "<RFC3339>"
vars.grafana_label_<name> = "<value>"
vars.grafana_annotation_<name> = "<value>"
```

## Next Step

Continue with [Usage and API](usage-and-api.md).

# Configuration

Back to the [Documentation Index](../README.md)

## Overview

Configuration can come from two sources:

1. **Environment variables** (default) — all settings come from env vars or a `.env` file
2. **Dashboard mode** — set `CONFIG_IN_DASHBOARD=true` to manage configuration through the Beauty Panel's Settings section

The current model is built around managed dummy hosts. Each configured host gets its own routing rules, its own API keys, and its own notification settings.

## Dashboard Configuration Mode

When `CONFIG_IN_DASHBOARD=true`, the bridge stores configuration in a JSON file on a persistent volume.

| Variable | Default | Description |
|---|---|---|
| `CONFIG_IN_DASHBOARD` | `false` | Enable dashboard-based configuration |
| `CONFIG_ENCRYPTION_KEY` | auto-generated | Encryption key for secrets at rest |
| `CONFIG_FILE_PATH` | `/var/log/webhook-bridge/config.json` | Path to the JSON config file |

On first start, the bridge performs a one-time migration from environment variables to the JSON file. Subsequent starts load from JSON. Secrets (Icinga2 password, admin password, API keys) are encrypted at rest using AES-256-GCM. An encryption key is auto-generated at `/var/log/webhook-bridge/.config.key` if not provided.

Changes made through the Settings panel are hot-reloaded without restart.

## One URL, Many Logical Webhooks

The bridge exposes one physical endpoint:

```text
POST /webhook
```

But you can define many logical webhook destinations with environment variables alone.

Each block of variables like:

```env
IAF_TARGET_TEAM_A_HOST_NAME=a-dummy-dev
IAF_TARGET_TEAM_A_API_KEYS=key-a-1,key-a-2
```

creates one logical destination behind the same `/webhook` URL.

That means:

- `/webhook` + `key-a-1` can route to `a-dummy-dev`
- `/webhook` + `key-a-2` can route to `a-dummy-dev`
- `/webhook` + another key can route to a different host

So you do not create separate HTTP paths for each team. You keep one URL and let the API key choose the host, source label, and notification settings.

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
<!-- LANG: hyphenation -->
| `CACHE_TTL_MINUTES` | No | `60` | TTL for the in-memory service cache |
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
| `IAF_TARGET_<ID>_API_KEYS` | Yes | — | Comma-separated webhook keys for this host |
| `IAF_TARGET_<ID>_SOURCE` | No | normalized target ID | Source label stored in logs and history |
| `IAF_TARGET_<ID>_NOTIFICATION_USERS` | No | empty | Comma-separated Icinga users |
| `IAF_TARGET_<ID>_NOTIFICATION_GROUPS` | No | empty | Comma-separated Icinga groups and user groups |
| `IAF_TARGET_<ID>_NOTIFICATION_SERVICE_STATES` | No | empty | Comma-separated service states such as `critical,warning` |
| `IAF_TARGET_<ID>_NOTIFICATION_HOST_STATES` | No | empty | Comma-separated host states such as `down` |

Important details:

- API keys must be unique across the whole deployment
- more than one key can point to the same host
- one incoming key always resolves to exactly one host
<!-- LANG: clarified wording -->
- `NOTIFICATION_GROUPS` is written into both `groups` and `user_groups` in Icinga

## How The Dynamic Variables Work

The `IAF_TARGET_<ID>_...` variables are dynamic in the sense that the bridge discovers them at startup by scanning the environment.

Examples:

- `IAF_TARGET_HOME_CRITICAL_HOST_NAME`
- `IAF_TARGET_HOME_CRITICAL_API_KEYS`
- `IAF_TARGET_HOME_CRITICAL_NOTIFICATION_HOST_STATES`

From that prefix, the bridge derives:

- target ID: `home-critical`
- routing block: everything that belongs to `IAF_TARGET_HOME_CRITICAL_*`

This gives you a flexible way to add new logical webhook destinations without changing code:

1. add a new `IAF_TARGET_<ID>_*` block
2. restart the bridge
3. use one of that block's API keys in Grafana

No extra route needs to be added in the application.

## How `IAF_TARGET_<ID>_NOTIFICATION_HOST_STATES` Works

Example:

```env
IAF_TARGET_HOME_CRITICAL_NOTIFICATION_HOST_STATES=down
```

This is the host notification state filter for one target block.

What the parts mean:

<!-- LANG: hyphenation -->
- `IAF_TARGET_` means it belongs to the target-based config model
- `HOME_CRITICAL` is the variable prefix that becomes target ID `home-critical`
- `NOTIFICATION_HOST_STATES` means host notification states, not service notification states
- `down` is the value that will be parsed as a comma-separated list

You can also write:

```env
IAF_TARGET_HOME_CRITICAL_NOTIFICATION_HOST_STATES=up,down
```

What happens internally:

1. the bridge reads the variable at startup
2. it parses the value as CSV
3. it stores the result in the target notification config
4. when it creates the host in Icinga, it writes:
   `vars.notification.host_states`
5. it also writes the same values into:
   `vars.notification.mail.host_states`
   `vars.notification.sms.host_states`

Important distinction:

- `..._NOTIFICATION_HOST_STATES` controls host notifications
- `..._NOTIFICATION_SERVICE_STATES` controls service notifications

In practice, alerts coming from Grafana usually become service results in Icinga, so `..._NOTIFICATION_SERVICE_STATES` is the setting you will notice most often.

`..._NOTIFICATION_HOST_STATES` matters when your Icinga host notification rules use `host.vars.notification.host_states`.

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

<!-- LANG: hyphenation -->
If any `IAF_TARGET_*` variables exist, the bridge switches to the new host-based routing model.

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

<!-- LANG: naming clarification -->
> *Note: the marker value is `IcingaAlertingForge` (with "Alerting") for historical reasons — this differs from the project name `IcingaAlertForge`.*

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

<!-- LANG: naming clarification -->
> *Note: the marker value is `IcingaAlertingForge` (with "Alerting") for historical reasons — this differs from the project name `IcingaAlertForge`.*

## Next Step

Continue with [Usage and API](usage-and-api.md).

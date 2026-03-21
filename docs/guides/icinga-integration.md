# Icinga Integration

Back to the [Documentation Index](../README.md)

## Dynamic Host Creation

On startup, the bridge checks every configured dummy host.

There are four basic cases:

1. the host exists and is already managed by IcingaAlertForge
2. the host exists but belongs to something else
3. the host does not exist and auto creation is enabled
4. the host does not exist and auto creation is disabled

When the bridge creates a host, it is intentionally passive:

- `check_command = "dummy"`
- `enable_active_checks = false`
- `max_check_attempts = 1`
- no real `address` attribute is written

The configured address is kept only as metadata in `vars.iaf_host_address`. That avoids accidental `ping4` or `ssh` services created by generic Icinga apply rules.

## Service Creation

Services created by the bridge use:

- `check_command = "dummy"`
- `enable_active_checks = false`
- `enable_passive_checks = true`
- `max_check_attempts = 1`

The bridge also stores webhook context on the service:

- labels as `vars.grafana_label_*`
- annotations as `vars.grafana_annotation_*`
- ownership markers such as `managed_by`, `iaf_created_at`, and `bridge_created_at`

## Notification Variables

The bridge does not decide whether you use mail, SMS, or any other transport. It only writes the variables that your Icinga rules can read.

Simple example:

```icinga2
apply Notification "sms-service" to Service {
  import "sms-service-notification"

  if (host.vars.notification.user_groups) {
    user_groups = host.vars.notification.user_groups
  }

  assign where host.vars.notification && host.vars.notification.user_groups
}
```

If your setup separates mail and SMS more explicitly, you can also use:

- `host.vars.notification.sms.user_groups`
- `host.vars.notification.mail.user_groups`

The bundled `testenv` uses the neutral `host.vars.notification.*` tree.

Full SMS by group example:

```icinga2
apply Notification "iaf-sms-service" to Service {
  import "sms-service-notification"

  interval = 0s

  if (host.vars.notification.user_groups) {
    user_groups = host.vars.notification.user_groups
  }

  if (host.vars.notification.service_states) {
    var service_states = []
    if ("ok" in host.vars.notification.service_states) {
      service_states += [ OK ]
    }
    if ("warning" in host.vars.notification.service_states) {
      service_states += [ Warning ]
    }
    if ("critical" in host.vars.notification.service_states) {
      service_states += [ Critical ]
    }
    if ("unknown" in host.vars.notification.service_states) {
      service_states += [ Unknown ]
    }
    if (len(service_states) > 0) {
      states = service_states
    }
  }

  assign where host.vars.notification && host.vars.notification.user_groups
}
```

In short:

- the bridge decides which host receives the alert
- the host carries the notification metadata
- your Icinga rules decide who gets notified and how

## Managed Markers

The bridge marks both hosts and services so it can tell its own objects apart from everything else.

Host markers:

```text
vars.managed_by = "IcingaAlertingForge"
vars.iaf_managed = true
vars.iaf_component = "IcingaAlertingForge"
```

Service markers:

```text
vars.managed_by = "IcingaAlertingForge"
vars.iaf_managed = true
vars.iaf_component = "IcingaAlertingForge"
vars.bridge_created_at = "<RFC3339>"
```

<!-- LANG: naming clarification -->
> *Note: the marker value is `IcingaAlertingForge` (with "Alerting") for historical reasons — this differs from the project name `IcingaAlertForge`.*

<!-- LANG: cross-reference -->
For the complete list of Icinga2 object variables, see [Configuration — What the Bridge Writes](configuration.md#what-the-bridge-writes-into-icinga).

These markers matter because they let the bridge:

- recognise managed dummy hosts on startup
- list its own services cleanly in the admin API
- clean up old objects without guessing from display names

## Ghost Cleanup

For a truly clean lab reset, use:

```bash
docker-compose -f testenv/docker-compose.yml down -v
docker-compose -f testenv/docker-compose.yml up -d --build
```

If you only want to remove managed services, use the cleanup script:

```bash
testenv/scripts/purge_host_services.sh
testenv/scripts/purge_host_services.sh --regex '^Synthetic Device'
testenv/scripts/purge_host_services.sh --apply --managed
```

Production advice:

- delete by managed marker, not just by age
- use regex cleanup only for known synthetic lab patterns
- avoid `--all` outside disposable test environments

## Cache Behaviour

The cache is keyed by `host + service`, not just by service name. That means the same alert name can exist on different hosts without collisions.

Cache states:

| State | Meaning |
|---|---|
| `not_found` | not cached or expired |
| `pending` | service creation in progress |
| `ready` | service exists and can be used |
| `pending_delete` | deletion in progress |

Important behaviour:

- a failed service create does not poison the cache
- expired entries are cleaned up by maintenance
- startup restores managed services into the cache for every configured host

## History and Logging

Every processed alert writes one JSONL entry.

Example:

```json
{
  "timestamp": "2026-03-21T09:11:53Z",
  "request_id": "0328524d-7edf-4102-bef7-ac216ca112f9",
  "source_key": "team-b",
  "host_name": "b-dummy-device",
  "mode": "work",
  "action": "firing",
  "service_name": "Team B Manual Check 2",
  "severity": "critical",
  "exit_status": 2,
  "message": "CRITICAL: Manual Team B routing test 2",
  "icinga_ok": true,
  "duration_ms": 5
}
```

Structured logs include:

- source
- target ID
- host
- request ID
- duration
- forwarding errors from Icinga

That makes it possible to see which key sent an alert and which host in Icinga was touched.

## Next Step

Continue with [Beauty Panel](beauty-panel.md) or [Test Environment](test-environment.md).

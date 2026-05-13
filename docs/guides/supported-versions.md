# Supported Alert Source Versions

## Grafana

| Version | Test Fixtures | Status |
|---------|---------------|--------|
| v9 | `testdata/webhooks/grafana/v9/` | Supported |
| v10 | `testdata/webhooks/grafana/v10/` | Supported |
| v11 | `testdata/webhooks/grafana/v11/` | Supported (protobuf-like payloads) |

Grafana Unified Alerting webhook format is stable across versions. Changes are tested against real-world payload samples.

## Prometheus Alertmanager

| Version | Test Fixtures | Status |
|---------|---------------|--------|
| v0.25 | `testdata/webhooks/alertmanager/v0.25/` | Supported |
| v0.27 | `testdata/webhooks/alertmanager/v0.27/` | Supported |
| v0.28 | `testdata/webhooks/alertmanager/v0.28/` | Supported |

Alertmanager webhook format follows the OpenAPI spec. Version drift is minimal.

## Universal Format

Any HTTP client can send alerts using the simplified universal format:

```json
{
  "alerts": [
    {
      "name": "AlertName",
      "status": "firing",
      "severity": "critical",
      "message": "Description of the alert",
      "labels": {"host": "server-01"},
      "annotations": {"runbook": "https://..."}
    }
  ]
}
```

See `testdata/` for more examples.

## Testing

```bash
make fuzz           # fuzz test webhook parsers (5 min)
go test ./models/   # run fixture validation
```

# Development and Operations

Back to the [Documentation Index](../README.md)

## Project Structure

```text
IcingaAlertForge/
├── main.go
├── README.md
├── docs/
├── .env.example
├── Dockerfile
├── go.mod
├── go.sum
├── auth/
├── cache/
├── config/
├── handler/
├── history/
├── icinga/
├── metrics/
├── models/
└── testenv/
```

Important folders:

- `auth/` handles API key validation
- `cache/` stores the in memory service registry
- `config/` parses environment variables
- `handler/` holds webhook, admin, status, and panel logic
- `history/` stores and serves the JSONL history
- `icinga/` contains the Icinga API client and rate limiting
- `testenv/` is the bundled local lab

## Test Commands

```bash
go test ./...
go test -race ./...
go vet ./...
```

## What The Tests Cover

- config parsing for the multi host setup and the legacy mode
- API key routing
- host aware cache behaviour
- webhook routing to different hosts
- history filtering by host
- Icinga host creation payloads
- status queries for service names with spaces

## Manual End To End Check

Team B example:

```bash
curl -s -X POST http://localhost:9080/webhook \
  -H "Content-Type: application/json" \
  -H "X-API-Key: test-key-script-dev" \
  -d '{
    "status": "firing",
    "alerts": [{
      "status": "firing",
      "labels": {
        "alertname": "Team B Manual Check",
        "severity": "critical"
      },
      "annotations": {
        "summary": "Manual Team B routing test"
      }
    }]
  }'
```

Expected result:

- the response contains `host = "b-dummy-device"`
- history contains `source_key = "team-b"`
- the service appears in `/admin/services?host=b-dummy-device`

## Operational Notes

- the bridge does not keep a durable outbox on disk
- if Icinga stays unavailable long enough and the sender stops retrying, alerts can still be lost
- host auto creation runs for every configured host, not just one
- the bridge writes routing variables only
- your Icinga apply rules must consume those variables
- the cleanest long term production model is API key based routing plus group based notifications in Icinga

## Load Test Reference

The historical load test report lives in [Load Test Results](../reference/load-test-results.md).

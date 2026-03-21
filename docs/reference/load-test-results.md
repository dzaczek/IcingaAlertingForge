# Load Test Results

Back to the [Documentation Index](../README.md)

<!-- LANG: hyphenation -->
> Note: these measurements were captured before the multi-host refactor, when the lab used one shared target host named `test-host`. The current `testenv` uses multiple dynamic hosts (`a-dummy-dev`, `b-dummy-device`), host-aware cache keys, and host-scoped routing. The load test script now targets `b-dummy-device` unless overridden, so treat the numbers below as a historical baseline rather than a benchmark for the current setup.

**Date:** 2026-03-20

**Environment:** Docker Compose (`testenv`)

**Concurrency:** 15 parallel requests

**Host:** macOS Darwin 25.4.0

**Rate Limits:** mutate=`20`, status=`50`, queue=`2000`

## Test Configuration

| Parameter | Value |
|---|---|
| Webhook Bridge | `http://localhost:9080/webhook` |
| Icinga2 API | `https://localhost:5665` |
| Target Host | historical single host setup: `test-host` |
| Alert Types | mixed `critical` and `warning` |
| Service Auto Create | `true` |
| TLS Skip Verify | `true` |

## Results Summary

| Series | Alerts Sent | HTTP 200 | HTTP Errors | Error Rate | Send Phase | Avg Response | Icinga2 Avg Latency | Icinga2 Min Latency | Icinga2 Max Latency | Total Duration | Throughput |
|---|---|---|---|---|---|---|---|---|---|---|---|
| 1 | 1 | 1 | 0 | 0.0% | 358ms | 58ms | 514ms | 514ms | 514ms | 2.3s | 0.4 alerts/s |
| 2 | 10 | 10 | 0 | 0.0% | 511ms | 151ms | 520ms | 495ms | 552ms | 9.4s | 1.1 alerts/s |
| 3 | 100 | 100 | 0 | 0.0% | 1,842ms | 176ms | 512ms | 495ms | 528ms | 35.3s | 2.8 alerts/s |
| 4 | 500 | 500 | 0 | 0.0% | 8,648ms | 199ms | 548ms | 525ms | 565ms | 153.1s | 3.3 alerts/s |
| 5 | 1,000 | 1,000 | 0 | 0.0% | 18,461ms | 217ms | 562ms | 464ms | 609ms | 301.6s | 3.3 alerts/s |

## What Stood Out

### Reliability

- all 1,611 alerts returned HTTP 200
- all tested services were created in Icinga2
- no rate limit rejections appeared during the run

### Webhook Response Time

| Series | Average Response Time |
|---|---|
| 1 alert | 58ms |
| 10 alerts | 151ms |
| 100 alerts | 176ms |
| 500 alerts | 199ms |
| 1,000 alerts | 217ms |

Response time scaled reasonably well. The jump from 1 alert to 1,000 alerts was noticeable, but still controlled.

### Icinga2 Propagation Latency

| Series | Average | Min | Max |
|---|---|---|---|
| 1 alert | 514ms | 514ms | 514ms |
| 10 alerts | 520ms | 495ms | 552ms |
| 100 alerts | 512ms | 495ms | 528ms |
| 500 alerts | 548ms | 525ms | 565ms |
| 1,000 alerts | 562ms | 464ms | 609ms |

Icinga2 stayed fairly stable at roughly half a second per operation. The bigger bottleneck was the rate limited flow of API calls, not wild latency spikes.

### Throughput

| Series | Effective Throughput |
|---|---|
| 1 alert | 0.4 alerts/s |
| 10 alerts | 1.1 alerts/s |
| 100 alerts | 2.8 alerts/s |
| 500 alerts | 3.3 alerts/s |
| 1,000 alerts | 3.3 alerts/s |

The test levelled off around `3.3 alerts/s`. That was expected because the rate limits were tuned to protect Icinga2 rather than squeeze out the highest raw throughput.

### 1,000 Alert Run Breakdown

| Phase | Duration |
|---|---|
| Sending to the webhook | 18.5s |
| Icinga2 processing and propagation | about 283s |
| Total end to end time | 301.6s |

## Conclusion

<!-- LANG: hyphenation -->
These numbers are useful as a historical baseline for the older single-host lab. They should not be read as a benchmark for the current multi-host setup.

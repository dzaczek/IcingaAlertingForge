# Observability (`health`, `metrics`, `cache`)

IcingaAlertForge is designed to be fully observable, offering extensive telemetry, internal health checking (reverse-monitoring), and an optimized state cache.

## `health` Package

The `health` package provides a "reverse health checker." Instead of Icinga polling the bridge, the bridge actively probes Icinga and reports its own health back to Icinga as a passive service.

### `Checker`
*   **Fast Track:** Periodically tests the Icinga2 API connection and self-reports status.
*   **Deep Dive:**
    *   **Self-Registration:** On startup, if `HEALTH_CHECK_REGISTER=true`, it automatically creates a service (default: `IcingaAlertForge-Health`) on the target dummy host.
    *   **Probing Loop:** Every `HEALTH_CHECK_INTERVAL_SEC` (default 60s), it calls `GetHostInfo`. If this fails 3 consecutive times, it marks the bridge as degraded.
    *   **Reporting:** It immediately attempts to send a `SendCheckResult` to its own service in Icinga. If Icinga is down, this obviously fails, but as soon as Icinga recovers, the bridge immediately reports "OK" (or "CRITICAL" if it recovered but the bridge is still failing internal checks).

## `metrics` Package

The `metrics` package collects Go runtime statistics, application throughput, and security events for display on the LCARS dashboard.

### `Collector`
*   **Fast Track:** In-memory metrics aggregator.
*   **Deep Dive:**
    *   Uses `atomic.Int64` for high-performance lock-free counting of `totalRequests`, `totalErrors`, and `totalLatencyMs`.
    *   **Security Tracking:** `RecordAuthFailure(ip, key)` tracks failed authentication attempts. It hashes the attempted key (logging only the first 12 chars of the SHA256 hash) to prevent logging sensitive secrets while still allowing administrators to identify if a compromised key is being brute-forced.
    *   **Snapshotting:** The `Snapshot()` method reads `runtime.MemStats` to gather heap, stack, and GC pause times. It also calculates `ErrorRate` and `AvgLatencyMs` on the fly. It aggregates recent failed logins to detect `BruteForceIPs` (IPs with 3+ failures in the last hour).

## `cache` Package

The `cache` package prevents the bridge from bombarding the Icinga2 API with "Create Service" requests for every single webhook.

### `ServiceCache`
*   **Fast Track:** A thread-safe, TTL-based map of known Icinga2 services.
*   **Deep Dive:**
    *   **Key Format:** Uses a non-printable separator (`host + "\x1f" + service`) to guarantee unique map keys.
    *   **States:** Services can be `StateReady` (confirmed existing in Icinga), `StatePending` (creation API call in flight), or `StatePendingDelete` (deletion API call in flight).
    *   **Maintenance:** To prevent unbounded memory growth in long-running processes, `StartMaintenance` runs a background goroutine that periodically calls `EvictExpired()`, purging entries older than the configured TTL (default 60 minutes). This ensures that if a service is deleted manually in Icinga, the bridge will eventually "forget" it and recreate it upon the next alert.
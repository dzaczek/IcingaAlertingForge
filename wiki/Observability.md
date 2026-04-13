# Observability (`health`, `metrics`, `cache`)

IcingaAlertForge is designed to be fully observable, offering extensive telemetry, internal health checking (reverse-monitoring), and an optimized state cache.

## `health` Package

The `health` package provides a "reverse health checker." Instead of Icinga polling the bridge, the bridge actively probes Icinga and reports its own health back to Icinga as a passive service.

### `health.Checker` (Struct)
*   **Fast Track:** Periodically tests the Icinga2 API connection and self-reports status.
*   **Deep Dive:**
    - **Methods:**
        - `Start(ctx)`: Begins the periodic probing loop.
        - `GetStatus()`: Returns the current `health.Status`.
    - **Self-Registration:** On startup, if `HEALTH_CHECK_REGISTER=true`, it automatically creates a service (default: `IcingaAlertForge-Health`) on the target dummy host.
    - **Probing Logic:** Every `HEALTH_CHECK_INTERVAL_SEC` (default 60s), it calls `GetHostInfo`. If this fails 3 consecutive times, it marks the bridge as degraded (`Healthy=false`).
    - **Reporting:** It attempts to send a `SendCheckResult` to its own service in Icinga. This allows Icinga2 to notify administrators if the bridge becomes disconnected or crashes.

---

## `metrics` Package

The `metrics` package collects application throughput, latency, and security events for real-time visualization.

### `metrics.Collector` (Struct)
*   **Fast Track:** In-memory metrics aggregator.
*   **Deep Dive:**
    - **Methods:**
        - `RecordRequest(latencyMs)`: Increments request count and adds to total latency.
        - `RecordError()`: Increments the error counter.
        - `RecordAuthFailure(ip, keyUsed)`: Tracks failed login attempts and hashes key prefixes for security tracing.
        - `Snapshot()`: Generates a `SystemStats` object containing both app-level metrics and Go runtime stats.
    - **Security Tracking:** Aggregates recent failed logins to detect `BruteForceIPs` (IPs with 3+ failures in the last hour).

### `metrics.SystemStats` (Struct)
*   **Fast Track:** A point-in-time snapshot of application health.
*   **Deep Dive:** Includes Go runtime metrics (`GoRoutines`, `MemAllocMB`, `GCRuns`), application throughput (`TotalRequests`, `ErrorRate`, `AvgLatencyMs`), and security telemetry (`FailedAuthTotal`, `BruteForceIPs`).

---

## `cache` Package

The `cache` package prevents the bridge from redundant "Create Service" calls, improving performance and reducing Icinga2 API load.

### `cache.ServiceCache` (Struct)
*   **Fast Track:** A thread-safe, TTL-based map of known Icinga2 services.
*   **Deep Dive:**
    - **Methods:**
        - `Register(host, service)`: Marks a service as existing in Icinga2.
        - `GetState(host, service)`: Returns `StateReady`, `StatePending`, or `StateNotFound`.
        - `Freeze(host, service, until)`: Temporarily suppresses alerts for a service (Maintenance Mode).
        - `IsFrozen(host, service)`: Checks if a service is currently in maintenance.
        - `EvictExpired()`: Manually triggers a purge of entries older than `CACHE_TTL_MINUTES`.
        - `AllEntries()`: Returns a sorted list of all active cache entries for the dashboard.
    - **Maintenance:** A background goroutine periodically calls `EvictExpired()` to prevent memory leaks and ensure the bridge periodically re-validates service existence in Icinga2.
    - **Key Format:** Uses a non-printable separator (`host + "\x1f" + service`) to guarantee unique map keys.

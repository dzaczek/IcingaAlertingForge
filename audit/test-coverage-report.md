# Test Coverage Audit Report

**Branch:** `chore/test-audit-and-ci`  
**Date:** 2026-05-13  
**Scope:** full  

---

## Coverage Snapshot (before remediation)

| Package | Coverage | Notes |
|---|---|---|
| `icinga-webhook-bridge` (root) | 4.9% | main.go wiring only |
| `audit` | 74.6% | |
| `auth` | 100.0% | |
| `cache` | 65.1% | Freeze/Unfreeze/IsFrozen/GetFreezeInfo/AllFrozen at 0% |
| `config` | 74.6% | `envTokenFromTargetID` at 0% |
| `configstore` | 56.1% | Exists/SetUsers/GetUsers/MigrateFromEnv/ToConfig/Export at 0% |
| `handler` | 36.5% | Many handlers at 0% (RateLimitStats, SetServiceStatus, Queue*, Users, Freeze, SSE, Dashboard) |
| `health` | 91.9% | |
| `history` | 59.2% | |
| `httputil` | 0.0% | No test file at all |
| `icinga` | 73.6% | |
| `metrics` | 74.9% | |
| `models` | 90.0% | |
| `queue` | 92.5% | |
| `rbac` | 86.3% | `SetOnSave` at 0% |
| **Total** | **51.7%** | |

---

## Packages with Zero Test Files

None — all 14 packages have at least one `*_test.go`, except `httputil` which had no test file.

---

## Frontend (JS/HTML)

The LCARS dashboard HTML is in `wiki/` and embedded Go templates in `handler/dashboard.go`.  
The JavaScript in the dashboard is cosmetic/templating only — no SSE-client-side logic, no RBAC gating in JS, no form validators.  
**Decision: no frontend JS test suite required.** Documented here, no Vitest setup added.

---

## CI Workflow Analysis (before remediation)

File: `.github/workflows/ci.yml`

| Check | State |
|---|---|
| Go version matrix | Single version `1.24` only — no matrix |
| `go vet` | Runs inside golangci-lint |
| `golangci-lint` | Present, uses `version: latest` (fragile) |
| `go test` with `-race` | Present |
| Coverage upload | Artifact only — no Codecov |
| Build | Present |
| Separate lint job | No — all in one job |
| Separate coverage job | No |
| `actions/checkout` version | `@v6` (non-existent, should be `@v4`) |
| `actions/upload-artifact` | `@v7` (non-existent, should be `@v4`) |
| `actions/setup-go` | `@v6` (non-existent, should be `@v5`) |

---

## Remediation Actions

1. Add `httputil/json_test.go` — `WriteJSON` happy path + headers check
2. Add `handler/sse_test.go` — SSEBroker Subscribe/Publish/Unsubscribe/ServeHTTP
3. Extend `handler/admin_test.go` → new file `handler/admin_extra_test.go` — RateLimitStats, SetServiceStatus, QueueStats, QueueFlush, ListUsers, CreateUser, DeleteUser, FreezeService, ListFrozen
4. Extend `configstore/store_test.go` → new file `configstore/store_extra_test.go` — Exists, SetUsers, GetUsers, MigrateFromEnv, ToConfig, Export
5. Extend `cache/services_test.go` → new file `cache/freeze_test.go` — Freeze, Unfreeze, IsFrozen, GetFreezeInfo, AllFrozen
6. Rewrite `.github/workflows/ci.yml` — fix action versions, split into matrix test + lint + coverage jobs
7. Add `codecov.yml`
8. Update `README.md` — add Codecov badge
9. Update `Makefile` — add `test-coverage` and `ci` targets aligned with spec

---

## Coverage Gaps Not Addressed

- `handler/dashboard.go:ServeHTTP` — renders full HTML with complex template dependencies (live API, embedded HTML, health checker). Skipped to avoid excessive mocking complexity.
- `main.go` — application wiring; would require full integration test. The existing `main_test.go` tests the signal-handler bootstrap.
- `configstore/store.go:getLegacyHostName`, `firstNonEmpty` — covered indirectly by `ToConfig` tests added.

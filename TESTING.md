# TESTING.md — IcingaAlertForge

## Przepływ danych testowych

```
Webhook POST /webhook
  │
  ├─ 1. Auth (X-API-Key / Authorization header)
  ├─ 2. Parse JSON (Grafana / Alertmanager / Universal format)
  ├─ 3. Validate (alertname, status, severity)
  ├─ 4. Transform (GrafanaAlert → Icinga2 exit status)
  │     critical → 2, warning → 1, resolved → 0, unknown → 2
  ├─ 5. Rate-limit (semaphore: max 20 concurrent status updates)
  ├─ 6. Auto-create service in Icinga2 if missing (PUT /v1/objects/services)
  ├─ 7. Forward check result to Icinga2 (POST /v1/actions/process-check-result)
  ├─ 8. Log to history (JSONL file)
  ├─ 9. Publish SSE event (dashboard real-time updates)
  └─ 10. Return JSON response
```

## Warstwy testów

| Warstwa | Co testuje | Komenda | Gdzie |
|---|---|---|---|
| **Unit** | Pojedyncze funkcje, parsowanie, transformacje | `make test` lub `go test ./...` | 22 plików `*_test.go` |
| **Smoke** | Pełny przepływ danych z mock Icinga2 | `make smoke` lub `./scripts/smoke-data-flow.sh` | `scripts/smoke-data-flow.sh` |
| **E2E** | Pełny stack Docker (MariaDB, Icinga2, Grafana, Prometheus) | `./scripts/run-ci-local.sh --e2e` | `testenv/` |

## Lokalne uruchamianie

```bash
# Szybki test jednostkowy
make test

# Test jednostkowy z race detectorem
make test-unit

# Lint
make lint

# Pełny pipeline CI (to samo co GitHub Actions)
make ci

# Tylko smoke test
make smoke

# E2E z Dockerem
./scripts/run-ci-local.sh --e2e
```

## Główny test decyzyjny — smoke-data-flow.sh

To najważniejszy test. Sprawdza cały przepływ:

1. **Uruchomienie** — startuje mock Icinga2 API + bridge
2. **Health check** — `/health` → 200 OK
3. **Auth** — 401 bez klucza, 401 zły klucz
4. **Walidacja** — 400 bad JSON, 400 puste alerty, 405 GET
5. **Przetwarzanie** — CRITICAL (exit=2), WARNING (exit=1), RESOLVED (exit=0)
6. **Błędne dane** — missing alertname → error, unknown status → handled
7. **Forwarding** — mock Icinga2 otrzymał `process-check-result` z poprawnymi danymi
8. **Historia** — wpisy w `/history`
9. **Test mode** — create + delete dummy service
10. **Concurrent** — 10 równoczesnych alertów

## Raport

Raport generowany jest automatycznie przez `scripts/run-ci-local.sh` do pliku `merge-report.md`.

### Jak interpretować MERGE READY

- **YES** — wszystkie krytyczne testy przeszły, merge bezpieczny
- **NO** — co najmniej jeden krytyczny test nie przeszedł:
  - Środowisko się nie uruchomiło
  - Test przepływu danych (smoke) nie przeszedł
  - Build się nie powiódł
  - Unit testy mają błędy

### Przykład raportu

```
# Merge Validation Report

**Branch:** `main` (e4e5ce2)
**Date:** 2026-05-07 19:50:00 UTC

| Check | Status | Notes |
|---|---|---|
| Install dependencies | PASS | Go go1.24.0 |
| Lint (go vet) | PASS | No issues |
| Unit tests | PASS | All packages pass |
| Build | PASS | Binary: webhook-bridge (v1.0.0-beta.161) |
| End-to-end data flow | PASS | Full pipeline verified: input → process → forward → output |
| Docker build | PASS | Image: webhook-bridge:ci-test |

**Final decision:**

**MERGE READY: YES**
```

## GitHub Actions

Workflow: `.github/workflows/ci.yml`

Uruchamiany dla:
- `pull_request` na `main`
- `push` na `main`
- `workflow_dispatch` (ręcznie)

Pipeline:
1. Checkout + Setup Go 1.24
2. go mod download
3. go vet (lint)
4. go test (unit tests z coverage)
5. go build
6. smoke-data-flow.sh (test przepływu danych)
7. Generowanie raportu → step summary + artifact + PR comment

Raport dostępny jako:
- GitHub Actions **step summary** (widoczny w UI)
- **Artifact** `merge-report` (do pobrania, 7 dni retencji)
- **Komentarz** w Pull Request (tylko dla PR)

## Jak dodać nowe przypadki testowe

### Unit test
Dodaj plik `*_test.go` w odpowiednim pakiecie.

### Smoke test
W `scripts/smoke-data-flow.sh`:
```bash
check "Opis testu" warunek_lub_funkcja
```
Funkcja `check` przyjmuje opis i komendę — zwraca PASS/FAIL.

### E2E test
W `testenv/scripts/run_all_tests.sh`:
```bash
run_test "test_mojego_testu" \
  "Opis testu" \
  "curl ... | jq ..."
```

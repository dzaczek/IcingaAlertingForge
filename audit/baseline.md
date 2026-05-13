# Phase 0 — Baseline Measurements

**Date:** 2026-05-13
**Branch:** `chore/phase-0-bootstrap`
**Go version:** go1.26.3 darwin/arm64
**Module:** icinga-webhook-bridge (Go 1.24.0)

## Test Coverage (per package)

| Package | Coverage | Status |
|---------|----------|--------|
| `icinga-webhook-bridge` (main) | 4.9% | wiring only |
| `audit` | 74.6% | |
| `auth` | 100.0% | |
| `cache` | 65.1% | |
| `config` | 74.6% | |
| `configstore` | 56.1% | |
| `handler` | 36.5% | largest gap |
| `health` | 91.9% | |
| `history` | 59.2% | |
| `httputil` | 0.0% | no test file |
| `icinga` | 73.6% | |
| `metrics` | 74.9% | |
| `models` | 90.0% | |
| `queue` | 92.5% | |
| `rbac` | 86.3% | |
| **Total** | **51.7%** | |

## Static Analysis

- **go vet:** clean (0 issues)
- **golangci-lint:** clean (0 issues)
- **golangci-lint version:** v2.2.0

## Build Metrics

| Metric | Value |
|--------|-------|
| Go source lines | 16,606 |
| Binary size (stripped) | 8.7 MB (9,082,066 bytes) |
| Binary size (go build) | 17.3 MB (17,314,882 bytes) |
| Docker image | Not available (daemon not running) |
| Go packages | 14 + main |
| Test files | 20 `*_test.go` files |

## Dependencies

- **Direct:** 5 (kingpin, godotenv, httprouter, prometheus, testify)
- **Total (go.sum):** 42 entries
- **Key deps:** prometheus/client_golang v1.23.2, testify v1.11.1

## Code Inventory

### Go Packages (14 + main)

| Package | Files | Purpose |
|---------|-------|---------|
| `audit` | 2 | Structured audit/CEF logging |
| `auth` | 2 | API key authentication |
| `cache` | 3 | In-memory service state cache |
| `config` | 2 | Environment/config loading |
| `configstore` | 2 | Persistent JSON config with AEAD encryption |
| `handler` | 16 | HTTP handlers (admin, webhook, dashboard, SSE, etc.) |
| `health` | 2 | Reverse health checker for Icinga2 |
| `history` | 3 | Alert history persistence |
| `httputil` | 1 | JSON response helper |
| `icinga` | 4 | Icinga2 API client |
| `metrics` | 5 | Prometheus metrics + per-key tracking |
| `models` | 4 | Webhook payload models (Grafana, Alertmanager) |
| `queue` | 2 | Retry queue for failed deliveries |
| `rbac` | 2 | Role-based access control |
| `main` | 2 | Application entry point + test |

### Frontend

- No standalone JS/CSS files found
- LCARS dashboard rendered server-side via Go templates in `handler/dashboard.go`
- `wiki/` contains 13 Markdown documentation files (no JS/HTML)

### CI/CD

| File | Purpose |
|------|---------|
| `.github/workflows/ci.yml` | Main CI (lint, test, build, coverage) |
| `.github/workflows/codeql.yml` | GitHub CodeQL security analysis |
| `.github/workflows/sync-wiki.yml` | Wiki sync automation |
| `.github/dependabot.yml` | Dependency update automation |
| `Dockerfile` (36 lines) | Multi-stage Docker build |

### Makefile Targets

build, docker, run, version, tag, release, test, test-unit, lint, vulncheck, coverage, smoke, ci, outdated, clean

## Security Posture (pre-audit)

| Check | State |
|-------|-------|
| Dependabot | Configured (gomod, docker, github-actions) |
| CodeQL | Enabled in CI |
| Secret scanning | Not configured |
| Vulnerability scanning | Not configured |
| SAST (gosec) | Not configured |
| Docker image scanning | Not configured |
| Supply-chain (SLSA/SBOM) | Not configured |
| Signed releases | Not configured |

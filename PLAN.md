

PLAN.md (the agent creates this in Phase 0)
Phase 0 — Bootstrap & Baseline (1 PR, low risk)
Goal: Establish baseline metrics and project infrastructure before changing anything.
Tasks:

Create audit/ directory with progress.md, baseline.md, blockers.md
Run and record: go test ./... -cover, go vet ./..., golangci-lint run, docker build, image size, binary size
Inventory: Go packages + exported symbols, frontend JS files, existing workflows, existing dependencies (go list -m all)
Create PLAN.md with all phases below
Add .github/ISSUE_TEMPLATE/bug_report.yml, feature_request.yml, PULL_REQUEST_TEMPLATE.md
Add SECURITY.md with vulnerability disclosure policy (email or GitHub Security Advisories)
Add CONTRIBUTING.md with dev setup, test commands, commit conventions
Add CODE_OF_CONDUCT.md (Contributor Covenant)

Deliverable PR: chore(phase-0): bootstrap audit infrastructure and community files

Phase 1 — Security Foundation (1 PR, low risk, high value)
Goal: Plug the obvious security holes before doing anything else.
Tasks:

Add .github/dependabot.yml covering: gomod, github-actions, docker (weekly schedule, grouped minor/patch)
Add .github/workflows/security.yml running on push, PR, and weekly cron:

gitleaks/gitleaks-action — secret scanning
golang/govulncheck-action — CVE check against go.sum
securego/gosec — static security analysis
aquasecurity/trivy-action — Docker image vulnerability scan
github/codeql-action — GitHub native SAST


Add .gitleaks.toml allowlist for known false positives (test fixtures)
Run all scanners locally first; fix any HIGH/CRITICAL findings
For MEDIUM findings: open GitHub issues tagged security, do NOT fix in this PR
Add security badge to README

Deliverable PR: ci(phase-1): add security scanning pipeline

Phase 2 — Test Coverage Audit & Backend Tests (1 PR per package group, 3-5 PRs)
Goal: Bring backend coverage from current baseline to ≥70% per package.
Sub-phases (one PR each):

2a — auth/, rbac/, audit/ (security-critical packages, target 90%)
2b — handler/, httputil/ (HTTP layer, use httptest)
2c — icinga/, queue/ (integration points, use interfaces + fakes)
2d — config/, configstore/, models/, cache/ (data layer, use temp dirs)
2e — health/, history/, metrics/ (remaining packages)

For each sub-phase:

Identify uncovered branches with go tool cover -html
Add table-driven tests for happy path + error cases
Add testdata/ fixtures where input is structured (especially webhook payloads — see Phase 6)
Use testify/require and testify/assert only if already a dependency; otherwise stdlib only
If a test reveals a real bug, mark with t.Skip("FIXME: see #N") and open issue
Coverage gate: each package must report ≥70% before PR is opened (security-critical: ≥90%)

Deliverable PRs: test(phase-2x): add unit tests for <packages>

Phase 3 — Frontend Tests (1 PR, medium risk)
Goal: Test the LCARS dashboard's JS logic.
Tasks:

Inventory JS files (likely embedded in handler/ via //go:embed)
Classify each JS file: cosmetic (skip), logic-bearing (test)
For logic-bearing files: extract testable functions into modules if needed
Add Vitest config (vitest.config.js, minimal package.json)
Write tests for: SSE event parsing, RBAC UI gating, form validators, alert state rendering
Add npm test script
Add .github/workflows/frontend.yml running Vitest
Update Makefile with frontend-test target
If JS turns out to be purely cosmetic, document this in audit/frontend-decision.md and skip the workflow

Deliverable PR: test(phase-3): add frontend unit test suite

Phase 4 — CI Pipeline Overhaul (1 PR, medium risk)
Goal: Replace ad-hoc CI with a proper pipeline.
Tasks:

Create .github/workflows/ci.yml with jobs:

lint — single Go version (from go.mod), golangci-lint-action@v6, go vet
test — matrix on Go versions (go.mod version + current stable), -race -coverprofile, upload coverage artifact
build — multi-platform build verification (GOOS=linux,darwin GOARCH=amd64,arm64)
coverage — needs: test, downloads artifact, uploads to Codecov with flags: backend
frontend-test — if Phase 3 succeeded


Add codecov.yml with project target auto, patch target 70%, ignore testenv/, scripts/, docs/, wiki/
Add Codecov + CI badges to README
Update Makefile with test, test-race, test-coverage, lint, ci targets
Add .github/branch-protection.md documenting recommended required checks for the repo owner to configure manually
Document CODECOV_TOKEN setup in PR description

Deliverable PR: ci(phase-4): production-grade CI pipeline with matrix testing and coverage

Phase 5 — Pre-commit & Developer Experience (1 PR, low risk)
Goal: Catch errors before they reach CI.
Tasks:

Add .pre-commit-config.yaml with hooks: go-fmt, go-imports, golangci-lint, gitleaks, trailing-whitespace, end-of-file-fixer, check-yaml, check-merge-conflict, markdownlint
Add .devcontainer/devcontainer.json with Go, Docker, Node, common tools — one-click setup in Codespaces/VS Code
Add .editorconfig for consistent whitespace
Update CONTRIBUTING.md with pre-commit install instructions
Add make setup-dev target installing pre-commit + dependencies

Deliverable PR: chore(phase-5): developer experience improvements

Phase 6 — Webhook Payload Fixtures & Parser Hardening (1 PR, medium risk)
Goal: Bulletproof the webhook ingestion layer against version drift.
Tasks:

Create testdata/webhooks/grafana/{v9,v10,v11}/ with real-world payload samples (firing, resolved, edge cases like missing fields, oversized payloads)
Create testdata/webhooks/alertmanager/{v0.25,v0.27,v0.28}/ similarly
Refactor parser tests to load from fixtures instead of inline JSON
Add fuzz tests: func FuzzWebhookParse(f *testing.F) for each parser
Run go test -fuzz=. -fuzztime=5m and commit any crashing inputs to testdata/fuzz/
Document supported source versions in docs/guides/supported-versions.md
Add a make fuzz target

Deliverable PR: test(phase-6): webhook payload fixtures and fuzz tests

Phase 7 — Observability of the Bridge Itself (1 PR, medium risk, touches production code — authorized)
Goal: The bridge should be observable like any production service.
Tasks:

Audit existing metrics/ package; add missing Prometheus metrics:

icingaalertforge_webhooks_received_total{source,target}
icingaalertforge_webhooks_processed_total{source,target,result}
icingaalertforge_icinga_push_duration_seconds{target} (histogram)
icingaalertforge_retry_queue_depth{target} (gauge)
icingaalertforge_auth_failures_total{reason}
icingaalertforge_alerts_by_severity{severity}


Ensure /metrics endpoint is exposed and documented
Migrate logs to log/slog (Go 1.21+) with JSON handler, configurable level
Add request ID middleware that propagates through logs
Create docs/grafana/icingaalertforge-dashboard.json — Grafana dashboard for the bridge itself
Document metrics in docs/guides/observability.md
Add Prometheus scrape config example to testenv/

Deliverable PR: feat(phase-7): production-grade observability (metrics, structured logging)

Phase 8 — Integration & Load Test Automation (1 PR, medium risk)
Goal: Use the existing testenv/ stack for real CI integration tests.
Tasks:

Add .github/workflows/integration.yml running on PR + nightly cron:

Spins up testenv/ via docker compose
Runs smoke tests: send Grafana webhook → verify Icinga2 service state → send resolve → verify back to OK
Tests RBAC: viewer cannot POST, operator can, admin can manage keys
Tests retry queue: stop Icinga2, send webhook, restart Icinga2, verify delivery
Tears down stack


Add .github/workflows/load-test.yml weekly cron with k6 or vegeta:

1000 webhooks/sec for 5 minutes
Capture p50/p95/p99 latency, error rate, memory/CPU
Append results to LOAD_TEST_RESULTS.md with timestamp + commit SHA
Open issue if p95 latency regresses >20% vs baseline


Document how to run integration tests locally in TESTING.md

Deliverable PR: ci(phase-8): integration and load test automation

Phase 9 — Release Automation (1 PR, medium risk)
Goal: Zero-touch releases with provenance.
Tasks:

Add .goreleaser.yml:

Build matrix: linux/darwin/windows × amd64/arm64
Archive formats: tar.gz (unix), zip (windows)
Generate checksums (sha256)
Generate SBOM with syft
Sign artifacts with cosign (keyless, OIDC)
Generate changelog grouped by Conventional Commit type
Publish GitHub Release


Add .github/workflows/release.yml triggered on tag push (v*):

Run full test suite first
Run goreleaser release --clean
Build and push multi-arch Docker image to ghcr.io/dzaczek/icingaalertingforge with tags: latest, vX.Y.Z, vX.Y, vX
Sign Docker image with cosign


Add release-please or git-cliff for automatic CHANGELOG.md maintenance
Add commitlint action enforcing Conventional Commits on PRs
Document release process in docs/guides/releases.md
Add cosign verification instructions to README

Deliverable PR: build(phase-9): automated multi-arch releases with SBOM and signing

Phase 10 — Hardening & Production Readiness (1 PR, higher risk, touches production code — authorized)
Goal: Make the bridge safe to run in hostile environments.
Tasks:

Add rate limiting per API key (token bucket, configurable)
Add request size limits (reject oversized webhooks before parsing)
Add icingaalertforge validate-config CLI subcommand (dry-run config)
Add graceful shutdown: drain retry queue on SIGTERM with configurable timeout
Add health check endpoints distinction: /livez (process alive) vs /readyz (can reach Icinga2)
Add Docker image hardening: non-root user, distroless or alpine base, no shell, read-only filesystem compatibility
Add docker-compose.production.yml example with resource limits, restart policies, secrets via files (not env)
Add Kubernetes manifests in deploy/kubernetes/ (Deployment, Service, ServiceMonitor, NetworkPolicy)
Add Helm chart in deploy/helm/icingaalertforge/
Document production deployment in docs/guides/production-deployment.md

Deliverable PR: feat(phase-10): production hardening and deployment manifests

Phase 11 — Documentation & Polish (1 PR, low risk)
Goal: Documentation matches the new reality.
Tasks:

Add markdownlint-cli2 and lychee link checker to CI
Fix all markdown lint warnings and broken links in docs/ and wiki/
Add architecture diagram (Mermaid, embedded in docs/guides/architecture-and-setup.md)
Add sequence diagram for webhook flow (Grafana → bridge → Icinga2)
Update README with: new badges, quick-start with Docker, link to production guide
Add docs/migrations/ with breaking changes between major versions (review last ~10 tags)
Add docs/troubleshooting.md with common issues from GitHub Issues history
Add docs/api-reference.md with all HTTP endpoints, request/response schemas, error codes
Generate OpenAPI spec if endpoints are stable; embed Swagger UI at /docs (optional)

Deliverable PR: docs(phase-11): comprehensive documentation refresh

Phase 12 — Final Report (1 PR, no code)
Goal: Summarize the entire effort.
Tasks:

Generate audit/final-report.md:

Coverage: baseline vs final per package
Security findings: opened, fixed, remaining
CI pipeline: jobs added, average duration, success rate
Docker image: size reduction, vulnerabilities before/after
Binary size and benchmark results
Documentation: pages added, links fixed
Open issues categorized by priority
Recommended next steps (mutation testing, OpenTelemetry tracing, etc.)


Update README.md "About" section to reflect production readiness
Archive audit/progress.md as audit/journal-YYYY-MM-DD.md

Deliverable PR: docs(phase-12): hardening project final report

Phase Dependency Graph
0 ─► 1 ─► 2(a-e) ─┬─► 4 ─► 5
                  │
                  ├─► 3 ─► 4
                  │
                  └─► 6
4 ─► 7 ─► 8
4 ─► 9
7 + 8 + 9 ─► 10 ─► 11 ─► 12
Phases 2a-2e can run in parallel after Phase 1 merges. Phases 3, 6, 7 can run in parallel after Phase 2 completes. Phase 10 requires 7, 8, 9.

Estimated Timeline
PhaseRiskWall-clock estimatePR size0Low1 daySmall1Low2 daysSmall2a–2eMedium1-2 weeks totalMedium each3Medium3 daysMedium4Medium2 daysMedium5Low1 daySmall6Medium3 daysMedium7Medium1 weekLarge8Medium4 daysMedium9Medium4 daysMedium10Higher1-2 weeksLarge11Low3 daysMedium12Low1 daySmall
Total: 8–12 weeks of agent work, ~15 PRs, fully reviewable in isolation.

Hard Stop Conditions
The agent must STOP and request human review if:

A phase touches more than 500 files
A phase changes more than 5000 lines of production code
A security scanner reports a CRITICAL finding in existing code (open issue, don't fix silently)
Tests reveal a behavioral bug affecting webhook delivery semantics
Any cryptographic code needs modification
Any change to authentication or authorization beyond what Phase 10 authorizes
Wall-clock time on a single phase exceeds 2 weeks
More than 3 blockers accumulate in audit/blockers.md

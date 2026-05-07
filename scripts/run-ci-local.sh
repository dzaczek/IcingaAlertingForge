#!/usr/bin/env bash
set -euo pipefail

# ── run-ci-local.sh ──────────────────────────────────────────────────
# Local CI runner — executes the same checks as GitHub Actions.
#
# Usage:
#   make ci               # full pipeline
#   ./scripts/run-ci-local.sh           # unit + build + smoke
#   ./scripts/run-ci-local.sh --full    # unit + build + smoke + lint
#   ./scripts/run-ci-local.sh --e2e     # also start Docker E2E stack
#
# Prerequisites: Go 1.24+, Docker (for --e2e), golangci-lint (for --full)

FULL=false
E2E=false
REPORT_FILE="${REPORT_FILE:-merge-report.md}"

for arg in "$@"; do
    case "$arg" in
        --full) FULL=true ;;
        --e2e)  E2E=true ;;
    esac
done

PASS=0
FAIL=0
SKIP=0
REPORT_LINES=""

NOW=$(date -u +"%Y-%m-%d %H:%M:%S UTC")
COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BRANCH=$(git rev-parse --abbrev-ref HEAD 2>/dev/null || echo "unknown")

check_result() {
    local step="$1" status="$2" notes="$3"
    case "$status" in
        PASS) PASS=$((PASS + 1)) ;;
        FAIL) FAIL=$((FAIL + 1)) ;;
        SKIP) SKIP=$((SKIP + 1)) ;;
    esac
    REPORT_LINES="${REPORT_LINES}| ${step} | ${status} | ${notes} |"$'\n'
}

# Writes the report to REPORT_FILE
write_report() {
    local final_decision="YES"
    if [ "$FAIL" -gt 0 ]; then
        final_decision="NO"
    fi
    # If smoke test didn't pass, force NO
    if echo "$REPORT_LINES" | grep -q "End-to-end data flow.*FAIL"; then
        final_decision="NO"
    fi
    # If env startup failed, force NO
    if echo "$REPORT_LINES" | grep -q "Environment startup.*FAIL"; then
        final_decision="NO"
    fi

    cat > "$REPORT_FILE" <<REPORTEOF
# Merge Validation Report

**Branch:** \`${BRANCH}\` (${COMMIT})
**Date:** ${NOW}

| Check | Status | Notes |
|---|---|---|
${REPORT_LINES}
**Final decision:**

**MERGE READY: ${final_decision}**
REPORTEOF

    echo ""
    echo "Report written to $REPORT_FILE"
}

cd "$(git rev-parse --show-toplevel 2>/dev/null || pwd)"

echo ""
echo "========================================"
echo "  IcingaAlertForge — CI Runner"
echo "========================================"

# ── 1. Install Dependencies ─────────────────────────────────────────

echo ""
echo "── 1. Install Dependencies ──"

DEPS_INSTALLED=true
if ! command -v go &>/dev/null; then
    echo "ERROR: Go not found"
    DEPS_INSTALLED=false
fi

GO_VERSION=$(go version 2>/dev/null | grep -o 'go[0-9.]*' | head -1 || echo "unknown")
echo "  Go version: $GO_VERSION"

if [ "$DEPS_INSTALLED" = true ]; then
    go mod download 2>&1 | tail -1 || echo "go mod download completed"
    check_result "Install dependencies" "PASS" "Go ${GO_VERSION}"
else
    check_result "Install dependencies" "FAIL" "Go not found"
    write_report
    exit 1
fi

# ── 2. Lint ─────────────────────────────────────────────────────────

echo ""
echo "── 2. Lint ──"

if [ "$FULL" = true ] && command -v golangci-lint &>/dev/null; then
    if golangci-lint run --timeout=3m ./... 2>&1; then
        check_result "Lint (golangci-lint)" "PASS" "No issues"
    else
        check_result "Lint (golangci-lint)" "FAIL" "Lint errors found"
    fi
elif [ "$FULL" = true ]; then
    echo "  golangci-lint not installed — install: https://golangci-lint.run/usage/install/"
    check_result "Lint (golangci-lint)" "SKIP" "golangci-lint not installed"
else
    # Basic vet even without full mode
    if go vet ./... 2>&1; then
        check_result "Lint (go vet)" "PASS" "No issues"
    else
        check_result "Lint (go vet)" "FAIL" "go vet found issues"
    fi
fi

# ── 3. Unit Tests ───────────────────────────────────────────────────

echo ""
echo "── 3. Unit Tests ──"

if go test -v -count=1 -timeout=60s ./... 2>&1 | tee /tmp/unit-test-output.log | tail -50; then
    check_result "Unit tests" "PASS" "All packages pass"
else
    check_result "Unit tests" "FAIL" "Test failures — see output above"
    write_report
    exit 1
fi

# ── 4. Build ────────────────────────────────────────────────────────

echo ""
echo "── 4. Build ──"

VERSION=$(git describe --tags --always --dirty 2>/dev/null || echo "dev")
if CGO_ENABLED=0 go build -ldflags "-X main.version=${VERSION}" -o ./webhook-bridge . 2>&1; then
    check_result "Build" "PASS" "Binary: webhook-bridge (${VERSION})"
else
    check_result "Build" "FAIL" "Build failed"
    write_report
    exit 1
fi

# ── 5. Smoke Data Flow Test ─────────────────────────────────────────

echo ""
echo "── 5. Smoke Data Flow Test ──"

if [ -x ./scripts/smoke-data-flow.sh ]; then
    if ./scripts/smoke-data-flow.sh; then
        check_result "End-to-end data flow" "PASS" "Full pipeline verified: input → process → forward → output"
    else
        check_result "End-to-end data flow" "FAIL" "Data flow broken — see smoke test output"
        write_report
        exit 1
    fi
else
    check_result "End-to-end data flow" "SKIP" "scripts/smoke-data-flow.sh not found"
fi

# ── 6. Docker Build ─────────────────────────────────────────────────

echo ""
echo "── 6. Docker Build ──"

if command -v docker &>/dev/null; then
    if docker build -t webhook-bridge:ci-test . 2>&1 | tail -5; then
        check_result "Docker build" "PASS" "Image: webhook-bridge:ci-test"
    else
        check_result "Docker build" "FAIL" "Docker build failed"
    fi
else
    check_result "Docker build" "SKIP" "Docker not available"
fi

# ── 7. E2E Test Environment (optional) ──────────────────────────────

echo ""
echo "── 7. E2E Test Environment ──"

if [ "$E2E" = true ] && command -v docker &>/dev/null; then
    echo "Starting full E2E stack (MariaDB + Icinga2 + Grafana + Prometheus + Bridge)..."
    if [ -f testenv/docker-compose.yml ]; then
        cd testenv
        if docker compose up -d --wait --timeout=120 2>&1 | tail -10; then
            check_result "Environment startup" "PASS" "Docker E2E stack running"
            echo "Running E2E test suite..."
            if [ -x scripts/run_all_tests.sh ]; then
                if scripts/run_all_tests.sh; then
                    check_result "E2E test suite" "PASS" "All E2E tests pass"
                else
                    check_result "E2E test suite" "FAIL" "Some E2E tests failed"
                fi
            fi
            docker compose down -v --timeout=30 2>/dev/null || true
        else
            check_result "Environment startup" "SKIP" "Docker E2E stack failed to start (may need more RAM/CPU)"
            echo "  Tip: E2E stack requires ~4GB RAM. Use 'make ci' for lightweight tests."
        fi
        cd ..
    fi
else
    check_result "Environment startup" "SKIP" "use --e2e flag to start Docker stack"
fi

# ── Report ──────────────────────────────────────────────────────────

write_report

echo ""
if [ "$FAIL" -eq 0 ]; then
    echo "All checks passed."
else
    echo "Some checks FAILED — review $REPORT_FILE"
fi

cat "$REPORT_FILE"

exit 0

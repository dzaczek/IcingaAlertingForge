# IcingaAlertForge — version-aware build targets
# Version is always derived from git tags (single source of truth).

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
BINARY  := webhook-bridge
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build docker run version tag release clean test test-unit lint ci smoke outdated vulncheck coverage

## build — compile binary with version from git tag
build:
	CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o $(BINARY) .

## docker — build Docker image with version from git tag
docker:
	docker build --build-arg VERSION=$(VERSION) -t icinga-alert-forge:$(VERSION) .

## run — build and run locally
run: build
	./$(BINARY)

## version — print current version derived from git
version:
	@echo $(VERSION)

## tag — create a new git tag (usage: make tag v=1.2.3)
tag:
	@if [ -z "$(v)" ]; then echo "Usage: make tag v=1.2.3"; exit 1; fi
	git tag -a "v$(v)" -m "Release v$(v)"
	@echo "Tagged v$(v). Run 'git push origin v$(v)' to publish."

## release — tag + push tag + build docker image
release:
	@if [ -z "$(v)" ]; then echo "Usage: make release v=1.2.3"; exit 1; fi
	git tag -a "v$(v)" -m "Release v$(v)"
	git push origin "v$(v)"
	$(MAKE) docker VERSION=v$(v)
	@echo "Released v$(v)"

## test — run unit tests with race detector and coverage
test:
	go test -v -count=1 -race -timeout=120s -coverprofile=coverage.out ./...

## test-unit — run unit tests with coverage only (no race, faster)
test-unit:
	go test -v -count=1 -timeout=120s -coverprofile=coverage.out ./...

## lint — go vet + golangci-lint (same as CI)
lint:
	go vet ./...
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run --timeout=3m ./...; \
	else \
		echo "golangci-lint not installed — install: https://golangci-lint.run/usage/install/"; \
	fi

## vulncheck — scan for known vulnerabilities in dependencies
vulncheck:
	go run golang.org/x/vuln/cmd/govulncheck@latest ./...

## coverage — show per-function coverage report
coverage: test
	go tool cover -func=coverage.out

## smoke — run end-to-end smoke data flow test
smoke:
	@if [ ! -x scripts/smoke-data-flow.sh ]; then \
		echo "scripts/smoke-data-flow.sh not found or not executable"; \
		exit 1; \
	fi
	./scripts/smoke-data-flow.sh

## ci — run full CI pipeline locally (same checks as GitHub Actions)
ci:
	@if [ ! -x scripts/run-ci-local.sh ]; then \
		echo "scripts/run-ci-local.sh not found or not executable"; \
		exit 1; \
	fi
	./scripts/run-ci-local.sh --full

## outdated — check for outdated direct dependencies
outdated:
	@echo "Direct dependencies:"
	@go list -u -m -json all 2>/dev/null | \
		jq -r 'select(.Indirect==false and .Update != null) | "  \(.Path): \(.Version) → \(.Update.Version)"' 2>/dev/null || \
		go list -u -m all 2>/dev/null | grep '\[.*\]'
	@echo ""
	@echo "Run 'go get -u ./...' to update, then 'go mod tidy'."

## clean — remove binary
clean:
	rm -f $(BINARY) webhook-bridge coverage.out merge-report.md

# IcingaAlertForge — version-aware build targets
# Version is always derived from git tags (single source of truth).

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
BINARY  := webhook-bridge
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build docker run version tag release clean

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

## clean — remove binary
clean:
	rm -f $(BINARY)

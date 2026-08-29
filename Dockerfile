# ── Builder stage ────────────────────────────────────────────────
FROM golang:1.27-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG VERSION=dev
RUN if [ "$VERSION" = "dev" ] && command -v git >/dev/null 2>&1 && git describe --tags >/dev/null 2>&1; then \
      VERSION=$(git describe --tags --always --dirty); \
    fi && \
    CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w -X main.version=${VERSION}" -o webhook-bridge .

# ── Runtime stage ────────────────────────────────────────────────
FROM alpine:3.24

# Upgrade pre-installed base-image packages (notably libssl3) to pick up
# security patches published after this alpine:3.24 layer was built.
RUN apk update && apk upgrade --no-cache
RUN apk add --no-cache ca-certificates curl tzdata

RUN adduser -D -u 1000 appuser
RUN mkdir -p /var/log/webhook-bridge && chown appuser:appuser /var/log/webhook-bridge

WORKDIR /app
COPY --from=builder /build/webhook-bridge .

USER appuser

EXPOSE 8080

HEALTHCHECK --interval=10s --timeout=5s --retries=5 \
  CMD curl -f http://localhost:8080/health || exit 1

ENTRYPOINT ["./webhook-bridge"]

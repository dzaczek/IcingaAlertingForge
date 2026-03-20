package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"icinga-webhook-bridge/auth"
	"icinga-webhook-bridge/cache"
	"icinga-webhook-bridge/history"
	"icinga-webhook-bridge/icinga"
	"icinga-webhook-bridge/metrics"
	"icinga-webhook-bridge/models"
)

// WebhookHandler is the main HTTP handler for incoming Grafana webhooks.
// It authenticates the request, parses the payload, and routes to the
// appropriate mode handler (test or work).
type WebhookHandler struct {
	KeyStore    *auth.KeyStore
	Cache       *cache.ServiceCache
	API         *icinga.APIClient
	History     *history.Logger
	HostName    string
	Limiter     *icinga.RateLimiter
	Metrics     *metrics.Collector
	HostExists  bool // set during startup after host validation
}

// ServeHTTP handles POST /webhook requests from Grafana.
func (h *WebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	// ── Authentication ──────────────────────────────────────────────
	// Check X-API-Key header first, then fall back to Authorization header
	// (Grafana webhook sends: "Authorization: <scheme> <credentials>")
	apiKey := r.Header.Get("X-API-Key")
	if apiKey == "" {
		if auth := r.Header.Get("Authorization"); auth != "" {
			// Strip scheme prefix (e.g. "ApiKey abc123" -> "abc123")
			if i := strings.Index(auth, " "); i >= 0 {
				apiKey = auth[i+1:]
			} else {
				apiKey = auth
			}
		}
	}
	source, ok := h.KeyStore.ValidateKey(apiKey)
	if !ok {
		slog.Warn("Unauthorized webhook request", "remote_addr", r.RemoteAddr)
		if h.Metrics != nil {
			h.Metrics.RecordAuthFailure(r.RemoteAddr, apiKey)
		}
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	// ── Parse payload (with body size limit) ────────────────────────
	const maxWebhookBody = 1 << 20 // 1 MiB
	r.Body = http.MaxBytesReader(w, r.Body, maxWebhookBody)

	var payload models.GrafanaPayload
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&payload); err != nil {
		slog.Error("Failed to decode webhook payload", "error", err, "source", source)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON payload"})
		return
	}

	if len(payload.Alerts) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no alerts in payload"})
		return
	}

	requestID := uuid.New().String()
	slog.Info("Webhook received",
		"request_id", requestID,
		"source", source,
		"status", payload.Status,
		"alert_count", len(payload.Alerts),
	)

	// ── Process each alert ──────────────────────────────────────────
	requestStart := time.Now()
	var results []map[string]any
	hasErrors := false
	for _, alert := range payload.Alerts {
		start := time.Now()
		result := h.processAlert(requestID, source, alert)
		result["duration_ms"] = time.Since(start).Milliseconds()
		if result["status"] == "error" {
			hasErrors = true
		}
		results = append(results, result)
	}

	// Record metrics
	if h.Metrics != nil {
		h.Metrics.RecordRequest(time.Since(requestStart).Milliseconds())
		if hasErrors {
			h.Metrics.RecordError()
		}
	}

	statusCode := http.StatusOK

	writeJSON(w, statusCode, map[string]any{
		"request_id": requestID,
		"source":     source,
		"results":    results,
	})
}

// processAlert routes a single alert to the appropriate handler based on mode.
func (h *WebhookHandler) processAlert(requestID, source string, alert models.GrafanaAlert) map[string]any {
	if alert.AlertName() == "" {
		return map[string]any{
			"error":  "missing alertname label",
			"status": "error",
		}
	}

	if alert.IsTestMode() {
		return h.handleTestMode(requestID, source, alert)
	}
	return h.handleWorkMode(requestID, source, alert)
}

// writeJSON writes a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, statusCode int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(data)
}

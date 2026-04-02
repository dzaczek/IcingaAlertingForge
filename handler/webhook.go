package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"icinga-webhook-bridge/audit"
	"icinga-webhook-bridge/auth"
	"icinga-webhook-bridge/cache"
	"icinga-webhook-bridge/config"
	"icinga-webhook-bridge/history"
	"icinga-webhook-bridge/icinga"
	"icinga-webhook-bridge/metrics"
	"icinga-webhook-bridge/models"
	"icinga-webhook-bridge/queue"
)

// WebhookHandler is the main HTTP handler for incoming Grafana webhooks.
// It authenticates the request, parses the payload, and routes to the
// appropriate mode handler (test or work).
type WebhookHandler struct {
	KeyStore   *auth.KeyStore
	Cache      *cache.ServiceCache
	API        *icinga.APIClient
	History    *history.Logger
	Targets    map[string]config.TargetConfig
	Limiter    *icinga.RateLimiter
	Metrics    *metrics.Collector
	SSE        *SSEBroker
	DebugRing  *icinga.DebugRing
	RetryQueue *queue.Queue
	Audit      *audit.Logger
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
	route, ok := h.KeyStore.ValidateKey(apiKey)
	if !ok {
		slog.Warn("Unauthorized webhook request", "remote_addr", r.RemoteAddr)
		if h.Metrics != nil {
			h.Metrics.RecordAuthFailure(r.RemoteAddr, apiKey)
		}
		if h.Audit != nil {
			h.Audit.Log(audit.Event{
				EventType:  audit.EventAuthFailure,
				Severity:   audit.SevHigh,
				RemoteAddr: r.RemoteAddr,
				Action:     "webhook.auth",
				Outcome:    "failure",
			})
		}
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	target, ok := h.Targets[route.TargetID]
	if !ok {
		slog.Error("Webhook route points to unknown target",
			"target_id", route.TargetID,
			"remote_addr", r.RemoteAddr)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "webhook route misconfigured"})
		return
	}

	// ── Parse payload (with body size limit) ────────────────────────
	const maxWebhookBody = 1 << 20 // 1 MiB
	r.Body = http.MaxBytesReader(w, r.Body, maxWebhookBody)

	rawBody, err := io.ReadAll(r.Body)
	if err != nil {
		slog.Error("Failed to read webhook body", "error", err, "source", route.Source)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "failed to read body"})
		return
	}

	payload, format, err := parseWebhookPayload(rawBody)
	if err != nil {
		slog.Error("Failed to decode webhook payload", "error", err, "source", route.Source, "host", target.HostName)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON payload"})
		return
	}

	if len(payload.Alerts) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no alerts in payload"})
		return
	}

	requestID := uuid.New().String()

	// Record inbound webhook in debug ring
	if h.DebugRing != nil {
		h.DebugRing.Push(icinga.DebugEntry{
			Timestamp:   time.Now(),
			Direction:   "inbound",
			Method:      r.Method,
			URL:         r.URL.String(),
			RequestBody: string(rawBody),
			Source:      route.Source,
			RemoteAddr:  r.RemoteAddr,
		})
	}
	slog.Info("Webhook received",
		"request_id", requestID,
		"source", route.Source,
		"target_id", target.ID,
		"host", target.HostName,
		"status", payload.Status,
		"alert_count", len(payload.Alerts),
		"format", format,
	)

	// ── Process each alert ──────────────────────────────────────────
	requestStart := time.Now()
	var results []map[string]any
	hasErrors := false
	for _, alert := range payload.Alerts {
		start := time.Now()
		result := h.processAlert(requestID, route.Source, target, alert, r.RemoteAddr)
		result["duration_ms"] = time.Since(start).Milliseconds()
		if resultHasError(result) {
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

	// Audit log
	if h.Audit != nil {
		outcome := "success"
		if hasErrors {
			outcome = "failure"
		}
		h.Audit.Log(audit.Event{
			EventType:  audit.EventWebhook,
			Severity:   audit.SevInfo,
			Source:     route.Source,
			RemoteAddr: r.RemoteAddr,
			Resource:   target.HostName,
			Action:     "webhook.process",
			Outcome:    outcome,
			RequestID:  requestID,
			Details: map[string]string{
				"alert_count": fmt.Sprintf("%d", len(payload.Alerts)),
				"format":      format,
			},
		})
	}

	statusCode := http.StatusOK
	if hasErrors {
		statusCode = http.StatusBadGateway
	}

	writeJSON(w, statusCode, map[string]any{
		"request_id": requestID,
		"source":     route.Source,
		"target_id":  target.ID,
		"host":       target.HostName,
		"results":    results,
	})
}

func resultHasError(result map[string]any) bool {
	if status, ok := result["status"].(string); ok && status == "error" {
		return true
	}
	if icingaOK, ok := result["icinga_ok"].(bool); ok && !icingaOK {
		return true
	}
	if errMsg, ok := result["error"].(string); ok && errMsg != "" {
		return true
	}
	return false
}

// processAlert routes a single alert to the appropriate handler based on mode.
func (h *WebhookHandler) processAlert(requestID, source string, target config.TargetConfig, alert models.GrafanaAlert, remoteAddr string) map[string]any {
	if alert.AlertName() == "" {
		return map[string]any{
			"error":  "missing alertname label",
			"status": "error",
		}
	}

	if alert.IsTestMode() {
		return h.handleTestMode(requestID, source, target, alert, remoteAddr)
	}
	return h.handleWorkMode(requestID, source, target, alert, remoteAddr)
}

// parseWebhookPayload auto-detects the webhook format and converts it
// to the internal GrafanaPayload. Supported formats:
//   - grafana: native Grafana Unified Alerting
//   - alertmanager: Prometheus Alertmanager
//   - universal: simplified IcingaAlertForge format
func parseWebhookPayload(rawBody []byte) (models.GrafanaPayload, string, error) {
	// Try to detect format from top-level fields
	var probe struct {
		// Alertmanager-specific fields
		Version  string `json:"version"`
		GroupKey string `json:"groupKey"`
		Receiver string `json:"receiver"`
		// Grafana/common fields
		Status string          `json:"status"`
		Alerts json.RawMessage `json:"alerts"`
	}
	if err := json.Unmarshal(rawBody, &probe); err != nil {
		return models.GrafanaPayload{}, "", err
	}

	// Alertmanager: has "version", "groupKey", or "receiver" fields
	if probe.Version != "" || probe.GroupKey != "" || probe.Receiver != "" {
		var amPayload models.AlertmanagerPayload
		if err := json.Unmarshal(rawBody, &amPayload); err != nil {
			return models.GrafanaPayload{}, "", err
		}
		return amPayload.ToGrafanaPayload(), "alertmanager", nil
	}

	// Grafana native: has "status" at top level
	if probe.Status != "" {
		var payload models.GrafanaPayload
		if err := json.Unmarshal(rawBody, &payload); err != nil {
			return models.GrafanaPayload{}, "", err
		}
		return payload, "grafana", nil
	}

	// Universal format: has "alerts" but no "status" at top level
	if probe.Alerts != nil {
		var up models.UniversalPayload
		if err := json.Unmarshal(rawBody, &up); err != nil {
			return models.GrafanaPayload{}, "", err
		}
		return up.ToGrafanaPayload(), "universal", nil
	}

	// Fallback: try Grafana format
	var payload models.GrafanaPayload
	if err := json.Unmarshal(rawBody, &payload); err != nil {
		return models.GrafanaPayload{}, "", err
	}
	return payload, "grafana", nil
}

// writeJSON writes a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, statusCode int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		slog.Debug("writeJSON encode error", "error", err)
	}
}

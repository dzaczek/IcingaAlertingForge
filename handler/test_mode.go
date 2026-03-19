package handler

import (
	"context"
	"log/slog"
	"time"

	"icinga-webhook-bridge/models"
)

// handleTestMode processes test mode alerts (create/delete dummy services).
func (h *WebhookHandler) handleTestMode(requestID, source string, alert models.GrafanaAlert) map[string]any {
	serviceName := alert.AlertName()
	action := alert.TestAction()

	switch action {
	case "create":
		return h.handleTestCreate(requestID, source, serviceName, alert)
	case "delete":
		return h.handleTestDelete(requestID, source, serviceName, alert)
	default:
		slog.Warn("Unknown test action", "action", action, "service", serviceName)
		return map[string]any{
			"error":   "unknown test_action: " + action,
			"status":  "error",
			"service": serviceName,
		}
	}
}

// handleTestCreate creates a dummy passive service in Icinga2 via the REST API.
func (h *WebhookHandler) handleTestCreate(requestID, source, serviceName string, alert models.GrafanaAlert) map[string]any {
	// Check cache — avoid duplicate creation
	if h.Cache.Exists(serviceName) {
		slog.Info("Service already exists in cache, skipping creation",
			"service", serviceName, "request_id", requestID)

		h.logHistory(requestID, source, "test", "create", serviceName, "", 0,
			"Service already exists (cached)", true, 0)

		return map[string]any{
			"status":  "already_exists",
			"service": serviceName,
		}
	}

	// Rate limit: acquire mutation slot (blocks until available, max 5 concurrent)
	if h.Limiter != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := h.Limiter.AcquireMutate(ctx); err != nil {
			slog.Warn("Rate limit timeout for service creation",
				"service", serviceName, "request_id", requestID)
			return map[string]any{
				"error":   "rate limit: too many concurrent operations",
				"status":  "error",
				"service": serviceName,
			}
		}
		defer h.Limiter.ReleaseMutate()
	}

	// Create via Icinga2 REST API (immediate, no deploy needed)
	h.Cache.SetPending(serviceName)

	start := time.Now()
	if err := h.API.CreateService(h.HostName, serviceName, alert.Labels, alert.Annotations); err != nil {
		h.Cache.Remove(serviceName)
		slog.Error("Failed to create service via Icinga2 API",
			"service", serviceName, "error", err, "request_id", requestID)

		h.logHistory(requestID, source, "test", "create", serviceName, "", 0,
			"Failed: "+err.Error(), false, time.Since(start).Milliseconds())

		return map[string]any{
			"error":   err.Error(),
			"status":  "error",
			"service": serviceName,
		}
	}

	durationMs := time.Since(start).Milliseconds()

	// REST API changes are immediate — mark as ready
	h.Cache.Register(serviceName)
	slog.Info("Service created and ready",
		"service", serviceName, "request_id", requestID, "duration_ms", durationMs)

	h.logHistory(requestID, source, "test", "create", serviceName, "", 0,
		"Service created", true, durationMs)

	return map[string]any{
		"status":  "created",
		"service": serviceName,
	}
}

// handleTestDelete removes a service from Icinga2 via the REST API.
func (h *WebhookHandler) handleTestDelete(requestID, source, serviceName string, alert models.GrafanaAlert) map[string]any {
	// Rate limit: acquire mutation slot
	if h.Limiter != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := h.Limiter.AcquireMutate(ctx); err != nil {
			slog.Warn("Rate limit timeout for service deletion",
				"service", serviceName, "request_id", requestID)
			return map[string]any{
				"error":   "rate limit: too many concurrent operations",
				"status":  "error",
				"service": serviceName,
			}
		}
		defer h.Limiter.ReleaseMutate()
	}

	start := time.Now()
	if err := h.API.DeleteService(h.HostName, serviceName); err != nil {
		slog.Error("Failed to delete service via Icinga2 API",
			"service", serviceName, "error", err, "request_id", requestID)

		h.logHistory(requestID, source, "test", "delete", serviceName, "", 0,
			"Failed: "+err.Error(), false, time.Since(start).Milliseconds())

		return map[string]any{
			"error":   err.Error(),
			"status":  "error",
			"service": serviceName,
		}
	}

	durationMs := time.Since(start).Milliseconds()

	h.Cache.Remove(serviceName)
	slog.Info("Service deleted",
		"service", serviceName, "request_id", requestID, "duration_ms", durationMs)

	h.logHistory(requestID, source, "test", "delete", serviceName, "", 0,
		"Service deleted", true, durationMs)

	return map[string]any{
		"status":  "deleted",
		"service": serviceName,
	}
}

package handler

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"icinga-webhook-bridge/models"
)

// isAlreadyExistsError checks if an Icinga2 API error indicates the object
// already exists (HTTP 409 / "already exists" in response).
func isAlreadyExistsError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "already exists") || strings.Contains(msg, "status 409")
}

// mapSeverityToExitStatus converts Grafana severity to Icinga2 exit status.
//
//	critical → 2 (CRITICAL)
//	warning  → 1 (WARNING)
//	default  → 2 (CRITICAL) — treat unknown severity as critical
func mapSeverityToExitStatus(severity string) int {
	switch severity {
	case "warning":
		return 1
	case "critical":
		return 2
	default:
		return 2
	}
}

// exitStatusLabel returns a human-readable label for the Icinga2 exit status.
func exitStatusLabel(exitStatus int) string {
	switch exitStatus {
	case 0:
		return "OK"
	case 1:
		return "WARNING"
	case 2:
		return "CRITICAL"
	default:
		return "UNKNOWN"
	}
}

// handleWorkMode processes production alerts (firing/resolved).
func (h *WebhookHandler) handleWorkMode(requestID, source string, alert models.GrafanaAlert) map[string]any {
	serviceName := alert.AlertName()
	severity := alert.Severity()
	summary := alert.Summary()

	var exitStatus int
	var action string
	var message string

	switch alert.Status {
	case "resolved":
		exitStatus = 0
		action = "resolved"
		message = fmt.Sprintf("OK: %s", summary)
		if message == "OK: " {
			message = "OK: Alert resolved"
		}

	case "firing":
		exitStatus = mapSeverityToExitStatus(severity)
		action = "firing"
		message = fmt.Sprintf("%s: %s", exitStatusLabel(exitStatus), summary)
		if summary == "" {
			message = fmt.Sprintf("%s: Alert firing", exitStatusLabel(exitStatus))
		}

	default:
		slog.Warn("Unknown alert status", "status", alert.Status, "service", serviceName)
		return map[string]any{
			"error":   "unknown alert status: " + alert.Status,
			"status":  "error",
			"service": serviceName,
		}
	}

	// Rate limit: acquire status slot (queue up to 100 concurrent updates)
	if h.Limiter != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		acquired, err := h.Limiter.AcquireStatus(ctx)
		if err != nil || !acquired {
			slog.Warn("Rate limit: status queue full, rejecting",
				"service", serviceName, "request_id", requestID)
			return map[string]any{
				"error":   "rate limit: status update queue full",
				"status":  "error",
				"service": serviceName,
			}
		}
		defer h.Limiter.ReleaseStatus()
	}

	// Auto-create service if it doesn't exist in cache.
	// Use mutate semaphore to prevent thundering herd on Icinga2 API.
	// Mark cache as pending before calling CreateService to prevent
	// concurrent requests from racing into multiple create calls.
	if !h.Cache.Exists(serviceName) {
		h.Cache.SetPending(serviceName)

		if h.Limiter != nil {
			mutCtx, mutCancel := context.WithTimeout(context.Background(), 30*time.Second)
			if err := h.Limiter.AcquireMutate(mutCtx); err != nil {
				mutCancel()
				slog.Warn("Rate limit: mutate slot unavailable for auto-create",
					"service", serviceName, "request_id", requestID)
				// Still proceed — service may already exist
			} else {
				mutCancel()
				defer h.Limiter.ReleaseMutate()
			}
		}

		slog.Info("Service not in cache, auto-creating",
			"service", serviceName, "request_id", requestID)
		if err := h.API.CreateService(h.HostName, serviceName, alert.Labels, alert.Annotations); err != nil {
			if !isAlreadyExistsError(err) {
				slog.Error("Failed to auto-create service",
					"service", serviceName, "error", err, "request_id", requestID)
			}
		} else {
			slog.Info("Service auto-created", "service", serviceName, "request_id", requestID)
		}
		h.Cache.Register(serviceName)
	}

	// Send check result to Icinga2
	start := time.Now()
	err := h.API.SendCheckResult(h.HostName, serviceName, exitStatus, message)
	durationMs := time.Since(start).Milliseconds()
	icingaOK := err == nil

	if err != nil {
		slog.Error("Failed to send check result to Icinga2",
			"service", serviceName, "exit_status", exitStatus,
			"error", err, "request_id", requestID)
	} else {
		slog.Info("Check result sent to Icinga2",
			"service", serviceName, "exit_status", exitStatus,
			"label", exitStatusLabel(exitStatus), "request_id", requestID)
	}

	errMsg := ""
	if err != nil {
		errMsg = err.Error()
	}

	h.logHistory(requestID, source, "work", action, serviceName, severity,
		exitStatus, message, icingaOK, durationMs)

	result := map[string]any{
		"status":      "processed",
		"service":     serviceName,
		"exit_status": exitStatus,
		"label":       exitStatusLabel(exitStatus),
		"icinga_ok":   icingaOK,
	}
	if errMsg != "" {
		result["error"] = errMsg
	}
	return result
}

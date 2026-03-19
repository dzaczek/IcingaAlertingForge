package handler

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"icinga-webhook-bridge/models"
)

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

	// Auto-create service if it doesn't exist in cache
	if !h.Cache.Exists(serviceName) {
		slog.Info("Service not in cache, auto-creating",
			"service", serviceName, "request_id", requestID)
		if err := h.API.CreateService(h.HostName, serviceName, alert.Labels, alert.Annotations); err != nil {
			// Ignore "already exists" errors (409) — just means another request created it
			if !strings.Contains(err.Error(), "already exists") && !strings.Contains(err.Error(), "409") {
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

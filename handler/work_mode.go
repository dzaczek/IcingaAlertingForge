package handler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"icinga-webhook-bridge/cache"
	"icinga-webhook-bridge/config"
	"icinga-webhook-bridge/icinga"
	"icinga-webhook-bridge/models"
	"icinga-webhook-bridge/queue"
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

// ensureServiceExists creates the service in Icinga2 when it is missing from
// the local cache. Cache state is updated only after a successful create or a
// confirmed "already exists" response so transient API failures do not poison
// the cache for the full TTL window.
func (h *WebhookHandler) ensureServiceExists(requestID string, target config.TargetConfig, alert models.GrafanaAlert) {
	serviceName := alert.AlertName()
	state := h.Cache.GetState(target.HostName, serviceName)
	if state != cache.StateNotFound {
		return
	}

	if h.Limiter != nil {
		mutCtx, mutCancel := context.WithTimeout(context.Background(), 30*time.Second)
		if err := h.Limiter.AcquireMutate(mutCtx); err != nil {
			mutCancel()
			slog.Warn("Rate limit: mutate slot unavailable for auto-create",
				"service", serviceName, "request_id", requestID, "error", err)
			return
		}
		mutCancel()
		defer h.Limiter.ReleaseMutate()
	}

	// Another request may have created the service while we were waiting.
	if h.Cache.GetState(target.HostName, serviceName) != cache.StateNotFound {
		return
	}

	h.Cache.SetPending(target.HostName, serviceName)
	slog.Info("Service not in cache, auto-creating",
		"host", target.HostName, "service", serviceName, "request_id", requestID)

	err := h.API.CreateService(target.HostName, serviceName, alert.Labels, alert.Annotations)
	var conflict *icinga.ErrConflict
	switch {
	case err == nil:
		h.Cache.Register(target.HostName, serviceName)
		slog.Info("Service auto-created", "host", target.HostName, "service", serviceName, "request_id", requestID)
	case isAlreadyExistsError(err):
		h.Cache.Register(target.HostName, serviceName)
		slog.Info("Service already exists in Icinga2, cache repaired",
			"host", target.HostName, "service", serviceName, "request_id", requestID)
	case errors.As(err, &conflict):
		// For auto-create, if it's a conflict (policy skip/fail), we treat it as "ready" so we can still
		// send the check result (Icinga will take care of the actual permission check).
		h.Cache.Register(target.HostName, serviceName)
		slog.Warn("Service auto-create refused (conflict policy)",
			"host", target.HostName, "service", serviceName, "error", err, "request_id", requestID)
	default:
		h.Cache.Remove(target.HostName, serviceName)
		slog.Error("Failed to auto-create service",
			"host", target.HostName, "service", serviceName, "error", err, "request_id", requestID)
	}
}

// handleWorkMode processes production alerts (firing/resolved).
func (h *WebhookHandler) handleWorkMode(requestID, source string, target config.TargetConfig, alert models.GrafanaAlert, remoteAddr string) map[string]any {
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
			"host":    target.HostName,
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
				"host":    target.HostName,
				"service": serviceName,
			}
		}
		defer h.Limiter.ReleaseStatus()
	}

	h.ensureServiceExists(requestID, target, alert)

	// Skip forwarding to Icinga2 if the service is frozen
	if h.Cache.IsFrozen(target.HostName, serviceName) {
		slog.Info("Service is frozen — skipping Icinga2 forward",
			"host", target.HostName, "service", serviceName, "request_id", requestID)
		h.logHistory(requestID, source, target.HostName, "work", "frozen_skip", serviceName,
			severity, exitStatus, message, true, 0, "", remoteAddr)
		return map[string]any{
			"status":  "frozen",
			"host":    target.HostName,
			"service": serviceName,
		}
	}

	// Send check result to Icinga2
	start := time.Now()
	err := h.API.SendCheckResult(target.HostName, serviceName, exitStatus, message)
	durationMs := time.Since(start).Milliseconds()
	icingaOK := err == nil

	queued := false
	if err != nil {
		slog.Error("Failed to send check result to Icinga2",
			"host", target.HostName, "service", serviceName, "exit_status", exitStatus,
			"error", err, "request_id", requestID)

		if h.RetryQueue != nil {
			_ = h.RetryQueue.Enqueue(queue.Item{
				ID:         fmt.Sprintf("%s-%s-%d", requestID, serviceName, time.Now().UnixNano()),
				Host:       target.HostName,
				Service:    serviceName,
				ExitStatus: exitStatus,
				Message:    message,
				Source:     source,
				RequestID:  requestID,
			})
			queued = true
		}
	} else {
		slog.Info("Check result sent to Icinga2",
			"host", target.HostName, "service", serviceName, "exit_status", exitStatus,
			"label", exitStatusLabel(exitStatus), "request_id", requestID)
	}

	errMsg := ""
	if err != nil {
		errMsg = err.Error()
	}

	h.logHistory(requestID, source, target.HostName, "work", action, serviceName, severity,
		exitStatus, message, icingaOK, durationMs, errMsg, remoteAddr)

	if h.SSE != nil {
		sseStatus := "ok"
		if exitStatus == 1 {
			sseStatus = "warning"
		}
		if exitStatus == 2 {
			sseStatus = "critical"
		}
		h.SSE.Publish(SSEEvent{Status: sseStatus, ServiceName: serviceName, Source: source, Mode: "work", RemoteAddr: remoteAddr})
	}

	result := map[string]any{
		"status":      "processed",
		"host":        target.HostName,
		"service":     serviceName,
		"exit_status": exitStatus,
		"label":       exitStatusLabel(exitStatus),
		"icinga_ok":   icingaOK,
	}
	if queued {
		result["queued"] = true
	}
	if errMsg != "" {
		result["error"] = errMsg
	}
	return result
}

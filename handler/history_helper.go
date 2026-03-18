package handler

import (
	"log/slog"
	"time"

	"icinga-webhook-bridge/models"
)

// logHistory is a convenience method to record a webhook event in the history.
func (h *WebhookHandler) logHistory(
	requestID, source, mode, action, serviceName, severity string,
	exitStatus int, message string, icingaOK bool, durationMs int64,
) {
	entry := models.HistoryEntry{
		Timestamp:   time.Now().UTC(),
		RequestID:   requestID,
		SourceKey:   source,
		Mode:        mode,
		Action:      action,
		ServiceName: serviceName,
		Severity:    severity,
		ExitStatus:  exitStatus,
		Message:     message,
		IcingaOK:    icingaOK,
		DurationMs:  durationMs,
	}

	if !icingaOK {
		entry.Error = message
	}

	if err := h.History.Append(entry); err != nil {
		slog.Error("Failed to write history entry",
			"error", err, "request_id", requestID)
	}
}

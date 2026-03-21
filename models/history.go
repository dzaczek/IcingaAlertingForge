package models

import "time"

// HistoryEntry represents a single webhook event recorded in the JSONL history file.
type HistoryEntry struct {
	Timestamp   time.Time `json:"timestamp"`
	RequestID   string    `json:"request_id"`
	SourceKey   string    `json:"source_key"`
	HostName    string    `json:"host_name,omitempty"`
	Mode        string    `json:"mode"`
	Action      string    `json:"action"`
	ServiceName string    `json:"service_name"`
	Severity    string    `json:"severity,omitempty"`
	ExitStatus  int       `json:"exit_status"`
	Message     string    `json:"message"`
	IcingaOK    bool      `json:"icinga_ok"`
	DurationMs  int64     `json:"duration_ms"`
	Error       string    `json:"error,omitempty"`
}

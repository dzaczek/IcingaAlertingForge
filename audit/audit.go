// Package audit provides structured audit logging for security and SIEM
// integration. Events are written in JSON or CEF format for consumption
// by log aggregators (Splunk, ELK, Graylog, etc.).
package audit

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// EventType classifies audit events.
type EventType string

const (
	EventAuthSuccess   EventType = "auth.success"
	EventAuthFailure   EventType = "auth.failure"
	EventWebhook       EventType = "webhook.received"
	EventStatusChange  EventType = "status.change"
	EventServiceCreate EventType = "service.create"
	EventServiceDelete EventType = "service.delete"
	EventAdminAction   EventType = "admin.action"
	EventQueueRetry    EventType = "queue.retry"
	EventHealthCheck   EventType = "health.check"
	EventConfigChange  EventType = "config.change"
)

// Severity levels aligned with CEF format.
type Severity int

const (
	SevInfo     Severity = 1
	SevLow      Severity = 3
	SevMedium   Severity = 5
	SevHigh     Severity = 7
	SevCritical Severity = 9
)

// Event represents a single audit log entry.
type Event struct {
	Timestamp  time.Time         `json:"timestamp"`
	EventType  EventType         `json:"event_type"`
	Severity   Severity          `json:"severity"`
	Source     string            `json:"source,omitempty"`
	Actor      string            `json:"actor,omitempty"`
	RemoteAddr string            `json:"remote_addr,omitempty"`
	Resource   string            `json:"resource,omitempty"`
	Action     string            `json:"action,omitempty"`
	Outcome    string            `json:"outcome"` // "success" or "failure"
	Details    map[string]string `json:"details,omitempty"`
	RequestID  string            `json:"request_id,omitempty"`
}

// Logger writes audit events to a file.
type Logger struct {
	mu      sync.Mutex
	file    *os.File
	format  string // "json" or "cef"
	enabled bool
}

// Config holds audit logger configuration.
type Config struct {
	Enabled bool
	File    string
	Format  string // "json" or "cef"
}

// New creates a new audit logger. Returns a no-op logger if disabled.
func New(cfg Config) (*Logger, error) {
	l := &Logger{
		format:  cfg.Format,
		enabled: cfg.Enabled,
	}

	if !cfg.Enabled {
		return l, nil
	}

	f, err := os.OpenFile(cfg.File, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0640)
	if err != nil {
		return nil, fmt.Errorf("audit: open file %s: %w", cfg.File, err)
	}
	l.file = f

	slog.Info("Audit logger initialized", "file", cfg.File, "format", cfg.Format)
	return l, nil
}

// Log writes an audit event.
func (l *Logger) Log(event Event) {
	if !l.enabled || l.file == nil {
		return
	}

	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}

	var line []byte
	var err error

	switch l.format {
	case "cef":
		line = []byte(formatCEF(event) + "\n")
	default:
		line, err = json.Marshal(event)
		if err != nil {
			slog.Error("Audit: failed to marshal event", "error", err)
			return
		}
		line = append(line, '\n')
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if _, err := l.file.Write(line); err != nil {
		slog.Error("Audit: failed to write event", "error", err)
	}
}

// Close shuts down the audit logger.
func (l *Logger) Close() {
	if l.file != nil {
		l.file.Close()
	}
}

// Enabled returns true if audit logging is active.
func (l *Logger) Enabled() bool {
	return l.enabled
}

// formatCEF renders an event in Common Event Format (ArcSight/SIEM standard).
// CEF:Version|Device Vendor|Device Product|Device Version|Signature ID|Name|Severity|Extension
func formatCEF(e Event) string {
	// ⚡ Bolt: Use strings.Builder instead of multiple fmt.Sprintf calls to reduce allocation and reflection overhead.
	var b strings.Builder
	b.Grow(256)

	b.WriteString("CEF:0|IcingaAlertForge|WebhookBridge|1.0|")
	b.WriteString(string(e.EventType))
	b.WriteByte('|')
	b.WriteString(e.Action)
	b.WriteByte('|')
	b.WriteString(strconv.Itoa(int(e.Severity)))
	b.WriteByte('|')

	b.WriteString("rt=")
	b.WriteString(e.Timestamp.Format(time.RFC3339))
	b.WriteString(" src=")
	b.WriteString(e.RemoteAddr)
	b.WriteString(" act=")
	b.WriteString(e.Action)
	b.WriteString(" outcome=")
	b.WriteString(e.Outcome)

	if e.Actor != "" {
		b.WriteString(" suser=")
		b.WriteString(e.Actor)
	}
	if e.Resource != "" {
		b.WriteString(" cs1=")
		b.WriteString(e.Resource)
		b.WriteString(" cs1Label=resource")
	}
	if e.RequestID != "" {
		b.WriteString(" cs2=")
		b.WriteString(e.RequestID)
		b.WriteString(" cs2Label=request_id")
	}
	if e.Source != "" {
		b.WriteString(" cs3=")
		b.WriteString(e.Source)
		b.WriteString(" cs3Label=source")
	}
	for k, v := range e.Details {
		b.WriteString(" cs4=")
		b.WriteString(v)
		b.WriteString(" cs4Label=")
		b.WriteString(k)
	}

	return b.String()
}

package audit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNew_FileOpenError(t *testing.T) {
	// Try to open a file in a non-existent directory
	_, err := New(Config{Enabled: true, File: "/nonexistent/audit.log", Format: "json"})
	if err == nil {
		t.Error("expected error for non-existent directory")
	}
}

func TestLogger_CEF_AllFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	logger, err := New(Config{Enabled: true, File: path, Format: "cef"})
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()

	logger.Log(Event{
		Timestamp:  time.Date(2026, 3, 29, 12, 0, 0, 0, time.UTC),
		EventType:  EventWebhook,
		Severity:   SevCritical,
		Actor:      "operator",
		RemoteAddr: "10.0.0.42",
		Resource:   "prod/db",
		RequestID:  "req-abc-123",
		Source:     "grafana",
		Action:     "webhook.process",
		Outcome:    "success",
		Details: map[string]string{
			"host":        "db-prod-01",
			"exit_status": "2",
		},
	})

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	line := strings.TrimSpace(string(data))
	if !strings.HasPrefix(line, "CEF:0|IcingaAlertForge|WebhookBridge|1.0|") {
		t.Errorf("invalid CEF prefix: %s", line)
	}
	if !strings.Contains(line, "suser=operator") {
		t.Error("expected suser=operator")
	}
	if !strings.Contains(line, "cs1=prod/db") {
		t.Error("expected cs1=prod/db")
	}
	if !strings.Contains(line, "cs1Label=resource") {
		t.Error("expected cs1Label=resource")
	}
	if !strings.Contains(line, "cs2=req-abc-123") {
		t.Error("expected cs2=req-abc-123")
	}
	if !strings.Contains(line, "cs3=grafana") {
		t.Error("expected cs3=grafana")
	}
	if !strings.Contains(line, "cs4=db-prod-01") {
		t.Error("expected cs4=db-prod-01 cs4Label=host")
	}
	if !strings.Contains(line, "cs4Label=host") {
		t.Error("expected cs4Label=host")
	}
}

func TestLogger_CEF_Minimal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	logger, err := New(Config{Enabled: true, File: path, Format: "cef"})
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()

	logger.Log(Event{
		Timestamp:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		EventType:  EventHealthCheck,
		Severity:   SevInfo,
		RemoteAddr: "127.0.0.1",
		Action:     "health.check",
		Outcome:    "success",
		// No Actor, Resource, RequestID, Source, Details — all empty
	})

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	line := strings.TrimSpace(string(data))
	if !strings.HasPrefix(line, "CEF:0|IcingaAlertForge|WebhookBridge|1.0|") {
		t.Errorf("invalid CEF prefix: %s", line)
	}
	if !strings.Contains(line, "health.check") {
		t.Error("expected health.check in CEF line")
	}
	if !strings.Contains(line, "outcome=success") {
		t.Error("expected outcome=success")
	}
	// Optional fields should be absent
	if strings.Contains(line, "suser=") {
		t.Error("suser should be absent when Actor is empty")
	}
	if strings.Contains(line, "cs1=") {
		t.Error("cs1 should be absent when Resource is empty")
	}
	if strings.Contains(line, "cs2=") {
		t.Error("cs2 should be absent when RequestID is empty")
	}
	if strings.Contains(line, "cs3=") {
		t.Error("cs3 should be absent when Source is empty")
	}
}

func TestLogger_CEF_SingleDetail(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	logger, err := New(Config{Enabled: true, File: path, Format: "cef"})
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()

	logger.Log(Event{
		Timestamp:  time.Date(2026, 5, 1, 10, 30, 0, 0, time.UTC),
		EventType:  EventConfigChange,
		Severity:   SevLow,
		RemoteAddr: "10.0.0.1",
		Action:     "config.update",
		Outcome:    "success",
		Details:    map[string]string{"key": "rate_limit"},
	})

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	line := strings.TrimSpace(string(data))
	if !strings.Contains(line, "cs4=rate_limit") {
		t.Error("expected cs4=rate_limit")
	}
	if !strings.Contains(line, "cs4Label=key") {
		t.Error("expected cs4Label=key")
	}
}

func TestLogger_Log_TimestampAutoSet(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	logger, err := New(Config{Enabled: true, File: path, Format: "json"})
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()

	// Passing zero time — Log should set it to now
	logger.Log(Event{
		EventType: EventAdminAction,
		Severity:  SevMedium,
		Actor:     "admin",
		Action:    "freeze.service",
		Outcome:   "success",
	})

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Fatal("expected log output, got empty")
	}
}

func TestLogger_MultipleDetails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	logger, err := New(Config{Enabled: true, File: path, Format: "cef"})
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()

	logger.Log(Event{
		Timestamp:  time.Date(2026, 4, 15, 8, 0, 0, 0, time.UTC),
		EventType:  EventServiceCreate,
		Severity:   SevInfo,
		RemoteAddr: "10.0.0.2",
		Action:     "service.create",
		Outcome:    "success",
		Details: map[string]string{
			"host":    "web-01",
			"service": "HTTP Check",
			"source":  "grafana",
		},
	})

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	line := strings.TrimSpace(string(data))
	if !strings.Contains(line, "cs4=web-01") {
		t.Error("expected cs4=web-01 for host detail")
	}
	if !strings.Contains(line, "cs4Label=host") {
		t.Error("expected cs4Label=host")
	}
	if !strings.Contains(line, "cs4=HTTP Check") {
		t.Error("expected cs4=HTTP Check for service detail")
	}
	if !strings.Contains(line, "cs4Label=service") {
		t.Error("expected cs4Label=service")
	}
	// Last detail should have cs4=grafana and cs4Label=source
	if !strings.Contains(line, "cs4=grafana") {
		t.Error("expected cs4=grafana for source detail")
	}
	if !strings.Contains(line, "cs4Label=source") {
		t.Error("expected cs4Label=source")
	}
}

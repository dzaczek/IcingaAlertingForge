package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLogger_JSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	logger, err := New(Config{Enabled: true, File: path, Format: "json"})
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()

	logger.Log(Event{
		EventType:  EventAuthSuccess,
		Severity:   SevInfo,
		Actor:      "admin",
		RemoteAddr: "192.168.1.1",
		Action:     "login",
		Outcome:    "success",
		RequestID:  "req-123",
	})

	logger.Log(Event{
		EventType:  EventWebhook,
		Severity:   SevLow,
		Source:     "grafana",
		RemoteAddr: "10.0.0.1",
		Resource:   "CPU Alert",
		Action:     "webhook.process",
		Outcome:    "success",
		Details:    map[string]string{"host": "server-1", "exit_status": "2"},
	})

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}

	var evt Event
	if err := json.Unmarshal([]byte(lines[0]), &evt); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}
	if evt.EventType != EventAuthSuccess {
		t.Errorf("expected auth.success, got %s", evt.EventType)
	}
	if evt.Actor != "admin" {
		t.Errorf("expected actor admin, got %s", evt.Actor)
	}
	if evt.Timestamp.IsZero() {
		t.Error("expected timestamp to be set")
	}
}

func TestLogger_CEF(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	logger, err := New(Config{Enabled: true, File: path, Format: "cef"})
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()

	logger.Log(Event{
		Timestamp:  time.Date(2026, 3, 29, 12, 0, 0, 0, time.UTC),
		EventType:  EventAuthFailure,
		Severity:   SevHigh,
		Actor:      "unknown",
		RemoteAddr: "192.168.1.99",
		Action:     "login_failed",
		Outcome:    "failure",
	})

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	line := strings.TrimSpace(string(data))
	if !strings.HasPrefix(line, "CEF:0|IcingaAlertForge|WebhookBridge|1.0|") {
		t.Errorf("invalid CEF prefix: %s", line)
	}
	if !strings.Contains(line, "auth.failure") {
		t.Error("expected auth.failure in CEF line")
	}
	if !strings.Contains(line, "suser=unknown") {
		t.Error("expected suser=unknown in CEF extension")
	}
	if !strings.Contains(line, "outcome=failure") {
		t.Error("expected outcome=failure")
	}
}

func TestLogger_Disabled(t *testing.T) {
	logger, err := New(Config{Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()

	if logger.Enabled() {
		t.Error("expected disabled logger")
	}

	// Should not panic
	logger.Log(Event{
		EventType: EventWebhook,
		Action:    "test",
	})
}

func TestLogger_ConcurrentWrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	logger, err := New(Config{Enabled: true, File: path, Format: "json"})
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()

	done := make(chan struct{})
	for i := 0; i < 50; i++ {
		go func() {
			logger.Log(Event{
				EventType: EventWebhook,
				Action:    "concurrent-test",
				Outcome:   "success",
			})
			done <- struct{}{}
		}()
	}

	for i := 0; i < 50; i++ {
		<-done
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 50 {
		t.Errorf("expected 50 lines, got %d", len(lines))
	}
}

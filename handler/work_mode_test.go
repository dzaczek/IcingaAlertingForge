package handler

import (
	"errors"
	"testing"

	"icinga-webhook-bridge/config"
	"icinga-webhook-bridge/models"
)

func TestMapSeverityToExitStatus(t *testing.T) {
	tests := []struct {
		severity string
		want     int
	}{
		{"critical", 2},
		{"warning", 1},
		{"", 2},     // unknown defaults to critical
		{"info", 2}, // unknown defaults to critical
	}

	for _, tt := range tests {
		got := mapSeverityToExitStatus(tt.severity)
		if got != tt.want {
			t.Errorf("mapSeverityToExitStatus(%q) = %d, want %d", tt.severity, got, tt.want)
		}
	}
}

func TestExitStatusLabel(t *testing.T) {
	tests := []struct {
		status int
		want   string
	}{
		{0, "OK"},
		{1, "WARNING"},
		{2, "CRITICAL"},
		{3, "UNKNOWN"},
	}

	for _, tt := range tests {
		got := exitStatusLabel(tt.status)
		if got != tt.want {
			t.Errorf("exitStatusLabel(%d) = %q, want %q", tt.status, got, tt.want)
		}
	}
}
func TestIsAlreadyExistsError(t *testing.T) {
	if isAlreadyExistsError(nil) {
		t.Error("expected false for nil")
	}
	if isAlreadyExistsError(errors.New("already exists")) != true {
		t.Error("expected true for 'already exists'")
	}
	if isAlreadyExistsError(errors.New("status 409")) != true {
		t.Error("expected true for 'status 409'")
	}
	if isAlreadyExistsError(errors.New("some other error")) {
		t.Error("expected false for 'some other error'")
	}
}

func TestHandleWorkModeCoverage(t *testing.T) {
	// Add some coverage for work_mode.go handleWorkMode since it was affected by the string concats.
	// Since tests are covering logic elsewhere we'll just test the default status which returns an error map directly
	h := &WebhookHandler{}

	// Create an alert with an unknown status
	alert := models.GrafanaAlert{
		Status: "unknown_status",
	}

	res := h.handleWorkMode("req-id", "src", config.TargetConfig{HostName: "targetHost"}, alert, "remoteAddr")
	if res["status"] != "error" {
		t.Errorf("Expected status 'error', got %v", res["status"])
	}
}

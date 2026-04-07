package handler

import "testing"

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

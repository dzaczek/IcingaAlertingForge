package models

import "time"

// GrafanaPayload represents the top-level webhook payload sent by Grafana Unified Alerting.
type GrafanaPayload struct {
	Status string         `json:"status"`
	Alerts []GrafanaAlert `json:"alerts"`
}

// GrafanaAlert represents a single alert within the Grafana webhook payload.
type GrafanaAlert struct {
	Status      string            `json:"status"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	StartsAt    time.Time         `json:"startsAt"`
	EndsAt      time.Time         `json:"endsAt"`
}

// AlertName returns the alertname label, which becomes the Icinga2 service name.
func (a GrafanaAlert) AlertName() string {
	return a.Labels["alertname"]
}

// Severity returns the severity label (critical, warning, etc.).
func (a GrafanaAlert) Severity() string {
	return a.Labels["severity"]
}

// Mode returns the mode label (test or empty for work mode).
func (a GrafanaAlert) Mode() string {
	return a.Labels["mode"]
}

// TestAction returns the test_action label (create, delete).
func (a GrafanaAlert) TestAction() string {
	return a.Labels["test_action"]
}

// Summary returns the summary annotation.
func (a GrafanaAlert) Summary() string {
	return a.Annotations["summary"]
}

// IsTestMode returns true if the alert is in test mode.
func (a GrafanaAlert) IsTestMode() bool {
	return a.Mode() == "test"
}

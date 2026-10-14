package models

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
	"unicode/utf8"
)

// Service naming modes control how an alert is mapped to an Icinga2 service.
const (
	// ServiceNameModeAlertnameSummary names the service "<alertname> - <summary>"
	// (or just "<alertname>" when the summary is empty), so every distinct
	// summary becomes its own Icinga2 service. This is the default.
	ServiceNameModeAlertnameSummary = "alertname_summary"
	// ServiceNameModeAlertname names the service after the alertname label only,
	// so all instances of one alert rule share a single Icinga2 service.
	ServiceNameModeAlertname = "alertname"
)

// serviceNameSeparator joins the alertname and summary in the service name.
const serviceNameSeparator = " - "

// maxServiceNameLen caps the generated service name length. Longer names are
// truncated and suffixed with a short hash so they stay unique.
const maxServiceNameLen = 200

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

// Description returns the description annotation.
func (a GrafanaAlert) Description() string {
	return a.Annotations["description"]
}

// ServiceName returns the Icinga2 service name for this alert.
//
// In ServiceNameModeAlertname it is the alertname label. In any other mode
// (default: ServiceNameModeAlertnameSummary) it is "<alertname> - <summary>",
// falling back to the alertname when the summary is empty. The summary should
// therefore only contain stable identifiers (labels); values that change
// between evaluations belong in the description annotation.
func (a GrafanaAlert) ServiceName(mode string) string {
	name := sanitizeServiceName(a.AlertName())
	if mode == ServiceNameModeAlertname {
		return name
	}
	summary := sanitizeServiceName(a.Summary())
	if summary == "" {
		return name
	}
	return truncateServiceName(name + serviceNameSeparator + summary)
}

// sanitizeServiceName makes s safe for use in an Icinga2 service name: "!" is
// the host/service separator in Icinga2 object names, and control characters
// (e.g. newlines from multi-line templates) are collapsed into single spaces.
func sanitizeServiceName(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '!':
			return '_'
		case r < 0x20 || r == 0x7f:
			return ' '
		}
		return r
	}, s)
	return strings.Join(strings.Fields(s), " ")
}

// truncateServiceName shortens names longer than maxServiceNameLen bytes,
// appending a hash of the full name so distinct long names stay distinct.
func truncateServiceName(s string) string {
	if len(s) <= maxServiceNameLen {
		return s
	}
	sum := sha256.Sum256([]byte(s))
	suffix := "~" + hex.EncodeToString(sum[:])[:8]
	cut := maxServiceNameLen - len(suffix)
	// Back off to a rune boundary so the result stays valid UTF-8.
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return strings.TrimSpace(s[:cut]) + suffix
}

// IsTestMode returns true if the alert is in test mode.
func (a GrafanaAlert) IsTestMode() bool {
	return a.Mode() == "test"
}

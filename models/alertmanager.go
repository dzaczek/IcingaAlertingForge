package models

import "time"

// AlertmanagerPayload represents the webhook payload from Prometheus Alertmanager.
// https://prometheus.io/docs/alerting/latest/configuration/#webhook_config
type AlertmanagerPayload struct {
	Version           string              `json:"version"`
	GroupKey          string              `json:"groupKey"`
	TruncatedAlerts   int                 `json:"truncatedAlerts"`
	Status            string              `json:"status"`
	Receiver          string              `json:"receiver"`
	GroupLabels       map[string]string   `json:"groupLabels"`
	CommonLabels      map[string]string   `json:"commonLabels"`
	CommonAnnotations map[string]string   `json:"commonAnnotations"`
	ExternalURL       string              `json:"externalURL"`
	Alerts            []AlertmanagerAlert `json:"alerts"`
}

// AlertmanagerAlert represents a single alert from Alertmanager.
type AlertmanagerAlert struct {
	Status       string            `json:"status"`
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	StartsAt     time.Time         `json:"startsAt"`
	EndsAt       time.Time         `json:"endsAt"`
	GeneratorURL string            `json:"generatorURL"`
	Fingerprint  string            `json:"fingerprint"`
}

// ToGrafanaAlert converts an Alertmanager alert to the internal GrafanaAlert
// format for uniform processing.
func (a AlertmanagerAlert) ToGrafanaAlert() GrafanaAlert {
	return GrafanaAlert{
		Status:      a.Status,
		Labels:      a.Labels,
		Annotations: a.Annotations,
		StartsAt:    a.StartsAt,
		EndsAt:      a.EndsAt,
	}
}

// ToGrafanaPayload converts the full Alertmanager payload to a GrafanaPayload.
func (p AlertmanagerPayload) ToGrafanaPayload() GrafanaPayload {
	alerts := make([]GrafanaAlert, 0, len(p.Alerts))
	for _, a := range p.Alerts {
		alerts = append(alerts, a.ToGrafanaAlert())
	}
	return GrafanaPayload{
		Status: p.Status,
		Alerts: alerts,
	}
}

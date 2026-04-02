package models

import "time"

// UniversalPayload is a simplified webhook format for custom integrations.
// Any system can send alerts using this format without Grafana or Alertmanager.
//
// Example:
//
//	{
//	  "alerts": [
//	    {
//	      "name": "DiskFull",
//	      "status": "firing",
//	      "severity": "critical",
//	      "message": "Disk usage > 90% on /data",
//	      "labels": {"host": "web-01"},
//	      "annotations": {"runbook": "https://..."}
//	    }
//	  ]
//	}
type UniversalPayload struct {
	Alerts []UniversalAlert `json:"alerts"`
}

// UniversalAlert represents a single alert in the universal format.
type UniversalAlert struct {
	Name        string            `json:"name"`
	Status      string            `json:"status"`   // firing, resolved
	Severity    string            `json:"severity"` // critical, warning, ok
	Message     string            `json:"message"`
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// ToGrafanaAlert converts a universal alert to the internal GrafanaAlert format.
func (a UniversalAlert) ToGrafanaAlert() GrafanaAlert {
	labels := make(map[string]string, len(a.Labels)+2)
	for k, v := range a.Labels {
		labels[k] = v
	}
	labels["alertname"] = a.Name
	if a.Severity != "" {
		labels["severity"] = a.Severity
	}

	annotations := make(map[string]string, len(a.Annotations)+1)
	for k, v := range a.Annotations {
		annotations[k] = v
	}
	if a.Message != "" {
		annotations["summary"] = a.Message
	}

	return GrafanaAlert{
		Status:      a.Status,
		Labels:      labels,
		Annotations: annotations,
		StartsAt:    time.Now(),
	}
}

// ToGrafanaPayload converts the universal payload to a GrafanaPayload.
func (p UniversalPayload) ToGrafanaPayload() GrafanaPayload {
	alerts := make([]GrafanaAlert, 0, len(p.Alerts))
	status := "firing"
	for _, a := range p.Alerts {
		alerts = append(alerts, a.ToGrafanaAlert())
		if a.Status == "resolved" {
			status = "resolved"
		}
	}
	return GrafanaPayload{
		Status: status,
		Alerts: alerts,
	}
}

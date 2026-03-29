package models

import (
	"encoding/json"
	"testing"
)

func TestAlertmanagerToGrafana(t *testing.T) {
	raw := `{
		"version": "4",
		"groupKey": "test",
		"status": "firing",
		"receiver": "webhook",
		"alerts": [
			{
				"status": "firing",
				"labels": {"alertname": "HighCPU", "severity": "critical", "instance": "web-01:9090"},
				"annotations": {"summary": "CPU usage is above 90%"},
				"startsAt": "2026-03-29T12:00:00Z",
				"endsAt": "0001-01-01T00:00:00Z",
				"fingerprint": "abc123"
			},
			{
				"status": "resolved",
				"labels": {"alertname": "DiskFull", "severity": "warning"},
				"annotations": {"summary": "Disk usage normalized"},
				"startsAt": "2026-03-29T11:00:00Z",
				"endsAt": "2026-03-29T12:00:00Z"
			}
		]
	}`

	var amPayload AlertmanagerPayload
	if err := json.Unmarshal([]byte(raw), &amPayload); err != nil {
		t.Fatalf("failed to parse alertmanager payload: %v", err)
	}

	gp := amPayload.ToGrafanaPayload()

	if gp.Status != "firing" {
		t.Errorf("expected status firing, got %s", gp.Status)
	}
	if len(gp.Alerts) != 2 {
		t.Fatalf("expected 2 alerts, got %d", len(gp.Alerts))
	}

	// First alert
	a1 := gp.Alerts[0]
	if a1.AlertName() != "HighCPU" {
		t.Errorf("expected alertname HighCPU, got %s", a1.AlertName())
	}
	if a1.Severity() != "critical" {
		t.Errorf("expected severity critical, got %s", a1.Severity())
	}
	if a1.Summary() != "CPU usage is above 90%" {
		t.Errorf("unexpected summary: %s", a1.Summary())
	}
	if a1.Status != "firing" {
		t.Errorf("expected firing, got %s", a1.Status)
	}

	// Second alert
	a2 := gp.Alerts[1]
	if a2.AlertName() != "DiskFull" {
		t.Errorf("expected alertname DiskFull, got %s", a2.AlertName())
	}
	if a2.Status != "resolved" {
		t.Errorf("expected resolved, got %s", a2.Status)
	}
}

func TestUniversalToGrafana(t *testing.T) {
	raw := `{
		"alerts": [
			{
				"name": "ServiceDown",
				"status": "firing",
				"severity": "critical",
				"message": "Service api-gateway is not responding",
				"labels": {"env": "production", "team": "platform"},
				"annotations": {"runbook": "https://wiki.example.com/runbook/api-gw"}
			}
		]
	}`

	var up UniversalPayload
	if err := json.Unmarshal([]byte(raw), &up); err != nil {
		t.Fatalf("failed to parse universal payload: %v", err)
	}

	gp := up.ToGrafanaPayload()

	if len(gp.Alerts) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(gp.Alerts))
	}

	a := gp.Alerts[0]
	if a.AlertName() != "ServiceDown" {
		t.Errorf("expected alertname ServiceDown, got %s", a.AlertName())
	}
	if a.Severity() != "critical" {
		t.Errorf("expected severity critical, got %s", a.Severity())
	}
	if a.Summary() != "Service api-gateway is not responding" {
		t.Errorf("unexpected summary: %s", a.Summary())
	}
	if a.Labels["env"] != "production" {
		t.Errorf("expected env=production label, got %s", a.Labels["env"])
	}
	if a.Annotations["runbook"] != "https://wiki.example.com/runbook/api-gw" {
		t.Error("expected runbook annotation preserved")
	}
}

func TestUniversalResolved(t *testing.T) {
	raw := `{
		"alerts": [
			{"name": "Test", "status": "resolved", "message": "All clear"}
		]
	}`

	var up UniversalPayload
	if err := json.Unmarshal([]byte(raw), &up); err != nil {
		t.Fatal(err)
	}

	gp := up.ToGrafanaPayload()
	if gp.Status != "resolved" {
		t.Errorf("expected resolved status, got %s", gp.Status)
	}
	if gp.Alerts[0].Status != "resolved" {
		t.Errorf("expected alert status resolved, got %s", gp.Alerts[0].Status)
	}
}

package handler

import (
	"icinga-webhook-bridge/config"
	"icinga-webhook-bridge/models"
	"icinga-webhook-bridge/cache"
	"icinga-webhook-bridge/icinga"
	"icinga-webhook-bridge/history"
	"path/filepath"
	"os"
	"testing"
	"net/http"
	"net/http/httptest"
)

func TestHandleWorkMode_Coverage(t *testing.T) {
	dir, _ := os.MkdirTemp("", "test")
	histLogger, _ := history.NewLogger(filepath.Join(dir, "hist.jsonl"), 100)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"results": [{"code": 200}]}`))
	}))
	defer ts.Close()

	apiClient := icinga.NewAPIClient(ts.URL, "user", "pass", false)

	h := &WebhookHandler{
		Cache: cache.NewServiceCache(60),
		History: histLogger,
		API: apiClient,
	}

	alert := models.GrafanaAlert{
		Status: "resolved",
	}
	alert.Labels = map[string]string{"alertname": "Test"}

	target := config.TargetConfig{HostName: "test-host"}

	h.handleWorkMode("req1", "src", target, alert, "1.1.1.1")

	alert.Status = "firing"
	alert.Labels["severity"] = "critical"
	h.handleWorkMode("req1", "src", target, alert, "1.1.1.1")

	alert.Labels["severity"] = "warning"
	alert.Annotations = map[string]string{"summary": "Test Summary"}
	h.handleWorkMode("req1", "src", target, alert, "1.1.1.1")

	alert.Labels["severity"] = "critical"
	alert.Annotations = map[string]string{"summary": "Test Summary"}
	h.handleWorkMode("req1", "src", target, alert, "1.1.1.1")
}

func TestWebhook_ProcessAlert_Coverage(t *testing.T) {
	dir, _ := os.MkdirTemp("", "test")
	histLogger, _ := history.NewLogger(filepath.Join(dir, "hist.jsonl"), 100)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"results": [{"code": 200}]}`))
	}))
	defer ts.Close()

	apiClient := icinga.NewAPIClient(ts.URL, "user", "pass", false)

	h := &WebhookHandler{
		Cache: cache.NewServiceCache(60),
		History: histLogger,
		API: apiClient,
	}

	alert := models.GrafanaAlert{
		Status: "firing",
	}
	alert.Labels = map[string]string{"alertname": "Test", "severity": "critical"}

	target := config.TargetConfig{HostName: "test-host"}

	h.processAlert("req1", "grafana", target, alert, "1.1.1.1")
}

func TestWebhook_ProcessAlert_MissingAlertname(t *testing.T) {
	h := &WebhookHandler{}
	target := config.TargetConfig{HostName: "test-host"}
	alert := models.GrafanaAlert{Status: "firing"}

	res := h.processAlert("req1", "grafana", target, alert, "1.1.1.1")
	if res["status"] != "error" {
		t.Errorf("expected error")
	}
}

func TestWebhook_ParseWebhookPayload_Coverage(t *testing.T) {
	_, _, _ = parseWebhookPayload([]byte(`{"status": "firing"}`))
	_, _, _ = parseWebhookPayload([]byte(`{"version": "4"}`))
	_, _, _ = parseWebhookPayload([]byte(`{"alerts": [{"name": "test"}]}`))
	_, _, _ = parseWebhookPayload([]byte(`{}`))
	_, _, _ = parseWebhookPayload([]byte(`invalid`))
}

func TestWebhook_ResultHasError(t *testing.T) {
	if !resultHasError(map[string]any{"status": "error"}) {
		t.Errorf("expected error")
	}
	if !resultHasError(map[string]any{"icinga_ok": false}) {
		t.Errorf("expected error")
	}
	if !resultHasError(map[string]any{"error": "failed"}) {
		t.Errorf("expected error")
	}
	if resultHasError(map[string]any{"status": "processed", "icinga_ok": true}) {
		t.Errorf("expected no error")
	}
}

package handler

import (
	"net/http"
	"testing"

	"icinga-webhook-bridge/config"
	"icinga-webhook-bridge/models"
)

func TestHandleTestMode_UnknownAction(t *testing.T) {
	h := testWebhookHandler(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"results":[{"code":200}]}`))
	})

	result := h.handleTestMode("req-1", "grafana", config.TargetConfig{HostName: "host-a"}, models.GrafanaAlert{
		Labels: map[string]string{"test_action": "invalid_action", "alertname": "svc-test"},
	}, "127.0.0.1")

	if result["status"] != "error" {
		t.Errorf("expected error status, got %v", result["status"])
	}
	if result["service"] == "" {
		t.Error("expected service name in result")
	}
}

func TestHandleTestCreate_AlreadyExists(t *testing.T) {
	h := testWebhookHandler(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"results":[{"code":200}]}`))
	})

	// Pre-register service in cache
	h.Cache.Register("host-a", "test-service")

	result := h.handleTestCreate("req-1", "grafana", config.TargetConfig{HostName: "host-a"},
		"test-service", models.GrafanaAlert{}, "127.0.0.1")

	if result["status"] != "already_exists" {
		t.Errorf("expected already_exists, got %v", result["status"])
	}
}

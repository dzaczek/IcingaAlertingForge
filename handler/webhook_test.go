package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"icinga-webhook-bridge/auth"
	"icinga-webhook-bridge/cache"
	"icinga-webhook-bridge/history"
	"icinga-webhook-bridge/icinga"
)

// testWebhookHandler creates a WebhookHandler wired to a mock Icinga2 API server.
func testWebhookHandler(t *testing.T, icingaHandler http.HandlerFunc) *WebhookHandler {
	t.Helper()

	// Mock Icinga2 REST API
	icingaServer := httptest.NewTLSServer(icingaHandler)
	t.Cleanup(icingaServer.Close)

	historyPath := filepath.Join(t.TempDir(), "history.jsonl")
	histLogger, err := history.NewLogger(historyPath, 1000)
	if err != nil {
		t.Fatalf("failed to create history logger: %v", err)
	}

	return &WebhookHandler{
		KeyStore: auth.NewKeyStore(map[string]string{
			"valid-key":   "grafana-test",
			"another-key": "grafana-dev",
		}),
		Cache: cache.NewServiceCache(60),
		API: &icinga.APIClient{
			BaseURL:    icingaServer.URL,
			User:       "test",
			Pass:       "test",
			HTTPClient: icingaServer.Client(),
		},
		History:  histLogger,
		HostName: "test-host",
	}
}

func TestWebhook_Unauthorized_NoKey(t *testing.T) {
	h := testWebhookHandler(t, func(w http.ResponseWriter, r *http.Request) {})

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestWebhook_Unauthorized_WrongKey(t *testing.T) {
	h := testWebhookHandler(t, func(w http.ResponseWriter, r *http.Request) {})

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "INVALID-KEY")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestWebhook_BadJSON(t *testing.T) {
	h := testWebhookHandler(t, func(w http.ResponseWriter, r *http.Request) {})

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(`not json`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "valid-key")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

func TestWebhook_EmptyAlerts(t *testing.T) {
	h := testWebhookHandler(t, func(w http.ResponseWriter, r *http.Request) {})

	payload := `{"status":"firing","alerts":[]}`
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "valid-key")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

func TestWebhook_MethodNotAllowed(t *testing.T) {
	h := testWebhookHandler(t, func(w http.ResponseWriter, r *http.Request) {})

	req := httptest.NewRequest(http.MethodGet, "/webhook", nil)
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rr.Code)
	}
}

func TestWebhook_FiringCritical(t *testing.T) {
	h := testWebhookHandler(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"results":[{"code":200}]}`))
	})

	payload := `{
		"status": "firing",
		"alerts": [{
			"status": "firing",
			"labels": {"alertname": "Test Alert", "severity": "critical"},
			"annotations": {"summary": "Something is wrong"}
		}]
	}`
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "valid-key")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}

	var resp map[string]any
	json.NewDecoder(rr.Body).Decode(&resp)
	results := resp["results"].([]any)
	result := results[0].(map[string]any)
	if result["exit_status"].(float64) != 2 {
		t.Errorf("expected exit_status 2, got %v", result["exit_status"])
	}
}

func TestWebhook_FiringWarning(t *testing.T) {
	h := testWebhookHandler(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"results":[{"code":200}]}`))
	})

	payload := `{
		"status": "firing",
		"alerts": [{
			"status": "firing",
			"labels": {"alertname": "Warning Alert", "severity": "warning"},
			"annotations": {"summary": "Minor issue"}
		}]
	}`
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "valid-key")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}

	var resp map[string]any
	json.NewDecoder(rr.Body).Decode(&resp)
	results := resp["results"].([]any)
	result := results[0].(map[string]any)
	if result["exit_status"].(float64) != 1 {
		t.Errorf("expected exit_status 1, got %v", result["exit_status"])
	}
}

func TestWebhook_Resolved(t *testing.T) {
	h := testWebhookHandler(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"results":[{"code":200}]}`))
	})

	payload := `{
		"status": "resolved",
		"alerts": [{
			"status": "resolved",
			"labels": {"alertname": "Resolved Alert", "severity": "critical"},
			"annotations": {"summary": "All good now"}
		}]
	}`
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "valid-key")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}

	var resp map[string]any
	json.NewDecoder(rr.Body).Decode(&resp)
	results := resp["results"].([]any)
	result := results[0].(map[string]any)
	if result["exit_status"].(float64) != 0 {
		t.Errorf("expected exit_status 0, got %v", result["exit_status"])
	}
}

func TestWebhook_TestModeCreate(t *testing.T) {
	h := testWebhookHandler(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"results":[{"code":200}]}`))
	})

	payload := `{
		"status": "firing",
		"alerts": [{
			"status": "firing",
			"labels": {"alertname": "Dummy Service", "mode": "test", "test_action": "create"},
			"annotations": {"summary": "Create dummy"}
		}]
	}`
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "valid-key")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}

	var resp map[string]any
	json.NewDecoder(rr.Body).Decode(&resp)
	results := resp["results"].([]any)
	result := results[0].(map[string]any)
	if result["status"] != "created" {
		t.Errorf("expected status created, got %v", result["status"])
	}
}

func TestWebhook_TestModeDelete(t *testing.T) {
	h := testWebhookHandler(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"results":[{"code":200}]}`))
	})

	payload := `{
		"status": "firing",
		"alerts": [{
			"status": "firing",
			"labels": {"alertname": "Dummy Service", "mode": "test", "test_action": "delete"},
			"annotations": {"summary": "Delete dummy"}
		}]
	}`
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "valid-key")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}

	var resp map[string]any
	json.NewDecoder(rr.Body).Decode(&resp)
	results := resp["results"].([]any)
	result := results[0].(map[string]any)
	if result["status"] != "deleted" {
		t.Errorf("expected status deleted, got %v", result["status"])
	}
}

func TestWebhook_CachePreventsDuplicate(t *testing.T) {
	createCallCount := 0
	h := testWebhookHandler(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			createCallCount++
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"results":[{"code":200}]}`))
	})

	payload := `{
		"status": "firing",
		"alerts": [{
			"status": "firing",
			"labels": {"alertname": "Cached Service", "mode": "test", "test_action": "create"},
			"annotations": {"summary": "Create"}
		}]
	}`

	// First request — should call API create
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "valid-key")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	// Second request — should be cached (no API create call)
	req = httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "valid-key")
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if createCallCount != 1 {
		t.Errorf("expected API create to be called once (cached), but was called %d times", createCallCount)
	}
}

func TestWebhook_MultipleKeys(t *testing.T) {
	h := testWebhookHandler(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"results":[{"code":200}]}`))
	})

	payload := `{
		"status": "firing",
		"alerts": [{
			"status": "firing",
			"labels": {"alertname": "Multi Key Test", "severity": "critical"},
			"annotations": {"summary": "Test"}
		}]
	}`

	for _, key := range []string{"valid-key", "another-key"} {
		req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-API-Key", key)
		rr := httptest.NewRecorder()

		h.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("key %s: expected 200, got %d", key, rr.Code)
		}

		var resp map[string]any
		json.NewDecoder(rr.Body).Decode(&resp)
		if resp["source"] == "" {
			t.Errorf("key %s: expected non-empty source", key)
		}
	}
}

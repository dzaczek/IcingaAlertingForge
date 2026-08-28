package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"icinga-webhook-bridge/audit"
	"icinga-webhook-bridge/auth"
	"icinga-webhook-bridge/cache"
	"icinga-webhook-bridge/config"
	"icinga-webhook-bridge/history"
	"icinga-webhook-bridge/icinga"
	"icinga-webhook-bridge/metrics"
	"icinga-webhook-bridge/ratelimit"
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
		KeyStore: auth.NewKeyStore(map[string]config.WebhookRoute{
			"valid-key": {
				Source:   "grafana-test",
				TargetID: "team-a",
			},
			"another-key": {
				Source:   "grafana-dev",
				TargetID: "team-b",
			},
		}),
		Cache: cache.NewServiceCache(60),
		API: &icinga.APIClient{
			BaseURL:        icingaServer.URL,
			User:           "test",
			Pass:           "test",
			HTTPClient:     icingaServer.Client(),
			ConflictPolicy: icinga.ConflictPolicyWarn,
		},
		History: histLogger,
		Targets: map[string]config.TargetConfig{
			"team-a": {
				ID:       "team-a",
				Source:   "grafana-test",
				HostName: "team-a-host",
			},
			"team-b": {
				ID:       "team-b",
				Source:   "grafana-dev",
				HostName: "team-b-host",
			},
		},
	}
}

func TestWebhook_RateLimit_Returns429AfterBurst(t *testing.T) {
	h := testWebhookHandler(t, func(w http.ResponseWriter, r *http.Request) {})
	h.Ingress = ratelimit.New(1, 2) // 1 req/s, burst of 2

	send := func(remoteAddr string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(`{}`))
		req.Header.Set("X-API-Key", "valid-key")
		req.RemoteAddr = remoteAddr
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr
	}

	// First two requests consume the burst and pass the rate gate.
	send("203.0.113.7:5555")
	send("203.0.113.7:5555")

	rr := send("203.0.113.7:5555")
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 after burst exhausted, got %d", rr.Code)
	}
	if rr.Header().Get("Retry-After") == "" {
		t.Error("expected Retry-After header on 429 response")
	}

	// A different source IP has an independent bucket and is not throttled.
	if rr := send("198.51.100.9:4444"); rr.Code == http.StatusTooManyRequests {
		t.Error("a different source IP should not be rate limited")
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
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"results":[{"attrs":{"vars":{"managed_by":"IcingaAlertingForge"}}}]}`))
			return
		}
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
	var createdPaths []string
	h := testWebhookHandler(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			createdPaths = append(createdPaths, r.URL.Path)
		}
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

	if len(createdPaths) != 2 {
		t.Fatalf("expected 2 create calls on different hosts, got %d", len(createdPaths))
	}
	if createdPaths[0] != "/v1/objects/services/team-a-host!Multi Key Test" {
		t.Fatalf("expected first create on team-a-host, got %s", createdPaths[0])
	}
	if createdPaths[1] != "/v1/objects/services/team-b-host!Multi Key Test" {
		t.Fatalf("expected second create on team-b-host, got %s", createdPaths[1])
	}
}

func TestWebhook_ReturnsBadGatewayAndLogsForwardingError(t *testing.T) {
	h := testWebhookHandler(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte(`{"error":"icinga unavailable"}`))
	})

	payload := `{
		"status": "firing",
		"alerts": [{
			"status": "firing",
			"labels": {"alertname": "Forward Fail", "severity": "critical"},
			"annotations": {"summary": "Something is wrong"}
		}]
	}`
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "valid-key")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", rr.Code)
	}

	var resp map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	results := resp["results"].([]any)
	result := results[0].(map[string]any)
	if result["icinga_ok"].(bool) {
		t.Fatalf("expected icinga_ok=false, got true")
	}
	if result["error"] == "" {
		t.Fatalf("expected forwarding error details in response")
	}

	entries, err := h.History.Query(history.QueryFilter{Limit: 10})
	if err != nil {
		t.Fatalf("failed to query history: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 history entry, got %d", len(entries))
	}
	if entries[0].Error == "" {
		t.Fatalf("expected history error to be populated")
	}
	if entries[0].Error == entries[0].Message {
		t.Fatalf("expected history error to contain API failure details, got message text only")
	}
}

func TestWebhook_WorkModeDoesNotCacheFailedAutoCreate(t *testing.T) {
	createCallCount := 0
	h := testWebhookHandler(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			if r.URL.Path == "/v1/objects/services/team-a-host!Create Retry" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"results":[{"attrs":{"vars":{"managed_by":"IcingaAlertingForge"}}}]}`))
			return
		}
		if r.Method == http.MethodPut {
			createCallCount++
		}
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte(`{"error":"icinga unavailable"}`))
	})

	payload := `{
		"status": "firing",
		"alerts": [{
			"status": "firing",
			"labels": {"alertname": "Create Retry", "severity": "critical"},
			"annotations": {"summary": "Retry auto create"}
		}]
	}`

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-API-Key", "valid-key")
		rr := httptest.NewRecorder()

		h.ServeHTTP(rr, req)

		if rr.Code != http.StatusBadGateway {
			t.Fatalf("expected 502, got %d", rr.Code)
		}
	}

	if createCallCount != 2 {
		t.Fatalf("expected failed auto-create to be retried, got %d create attempt(s)", createCallCount)
	}
}

func TestWebhook_Authenticate_AuthorizationHeader_WithScheme(t *testing.T) {
	h := testWebhookHandler(t, func(w http.ResponseWriter, r *http.Request) {})

	payload := `{"status": "firing", "alerts": [{"labels": {"alertname": "Test"}, "status": "firing"}]}`
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "ApiKey valid-key")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code == http.StatusUnauthorized {
		t.Errorf("expected auth to pass with a schemed Authorization header, got 401")
	}
}

func TestWebhook_Authenticate_AuthorizationHeader_NoScheme(t *testing.T) {
	h := testWebhookHandler(t, func(w http.ResponseWriter, r *http.Request) {})

	payload := `{"status": "firing", "alerts": [{"labels": {"alertname": "Test"}, "status": "firing"}]}`
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "valid-key")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code == http.StatusUnauthorized {
		t.Errorf("expected auth to pass with a schemeless Authorization header, got 401")
	}
}

func TestWebhook_Authenticate_XAPIKey(t *testing.T) {
	h := testWebhookHandler(t, func(w http.ResponseWriter, r *http.Request) {})

	payload := `{"status": "firing", "alerts": [{"labels": {"alertname": "Test"}, "status": "firing"}]}`
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "valid-key")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code == http.StatusUnauthorized {
		t.Errorf("expected auth to pass with X-API-Key, got 401")
	}
}

func TestWebhook_Authenticate_UnknownTarget(t *testing.T) {
	h := testWebhookHandler(t, func(w http.ResponseWriter, r *http.Request) {})
	// A valid key whose route points at a target that no longer exists.
	h.Targets = map[string]config.TargetConfig{}

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "valid-key")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 for a route with a missing target, got %d", rr.Code)
	}
}

// TestWebhook_Unauthorized_RecordsMetricsAndAudit exercises authenticate()'s
// h.Metrics and h.Audit nil-guards on the failure path, which testWebhookHandler
// otherwise leaves nil (and so untested) for every other auth test.
func TestWebhook_Unauthorized_RecordsMetricsAndAudit(t *testing.T) {
	h := testWebhookHandler(t, func(w http.ResponseWriter, r *http.Request) {})
	h.Metrics = metrics.NewCollector()

	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	auditLogger, err := audit.New(audit.Config{Enabled: true, File: auditPath, Format: "json"})
	if err != nil {
		t.Fatalf("failed to create audit logger: %v", err)
	}
	h.Audit = auditLogger

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "INVALID-KEY")
	req.RemoteAddr = "203.0.113.5:5555"
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}

	if got := h.Metrics.Snapshot().FailedAuthTotal; got != 1 {
		t.Errorf("expected 1 recorded auth failure, got %d", got)
	}

	auditData, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatalf("failed to read audit log: %v", err)
	}
	if !strings.Contains(string(auditData), `"event_type":"auth.failure"`) {
		t.Errorf("expected audit log to contain an auth.failure event, got: %s", auditData)
	}
	if !strings.Contains(string(auditData), `"outcome":"failure"`) {
		t.Errorf("expected audit log entry to have outcome=failure, got: %s", auditData)
	}
}

func TestWebhook_ParseWebhookPayload_GrafanaFallback(t *testing.T) {
	// No "status", "alerts", or Alertmanager fields at all — falls through
	// to the final "try Grafana format" branch, which succeeds trivially
	// (unknown fields are ignored) rather than erroring.
	rawBody := []byte(`{"invalid": "data"}`)
	payload, format, err := parseWebhookPayload(rawBody)
	if err != nil {
		t.Fatalf("expected no error for the fallback path, got %v", err)
	}
	if format != "grafana" {
		t.Errorf("expected fallback format 'grafana', got %s", format)
	}
	if len(payload.Alerts) != 0 {
		t.Errorf("expected no alerts, got %d", len(payload.Alerts))
	}
}

func TestWebhook_ParseWebhookPayload_Alertmanager(t *testing.T) {
	rawBody := []byte(`{"version": "4", "groupKey": "key", "receiver": "web", "alerts": [{"status": "firing", "labels": {"alertname": "Test"}}]}`)
	payload, format, err := parseWebhookPayload(rawBody)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if format != "alertmanager" {
		t.Errorf("expected format 'alertmanager', got %s", format)
	}
	if len(payload.Alerts) != 1 {
		t.Errorf("expected 1 alert, got %d", len(payload.Alerts))
	}
}

func TestWebhook_ParseWebhookPayload_Universal(t *testing.T) {
	rawBody := []byte(`{"alerts": [{"status": "firing", "labels": {"alertname": "Test"}}]}`)
	payload, format, err := parseWebhookPayload(rawBody)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if format != "universal" {
		t.Errorf("expected format 'universal', got %s", format)
	}
	if len(payload.Alerts) != 1 {
		t.Errorf("expected 1 alert, got %d", len(payload.Alerts))
	}
}

func TestWebhook_ParseWebhookPayload_InvalidJSON(t *testing.T) {
	rawBody := []byte(`{"version": "4", "groupKey": `)
	_, _, err := parseWebhookPayload(rawBody)
	if err == nil {
		t.Errorf("expected error for invalid json, got nil")
	}
}

func TestWebhook_ParseWebhookPayload_AlertmanagerInvalid(t *testing.T) {
	// "version" triggers the Alertmanager branch, but "alerts" is the wrong type.
	rawBody := []byte(`{"version": "4", "groupKey": "key", "receiver": "web", "alerts": "not-an-array"}`)
	_, _, err := parseWebhookPayload(rawBody)
	if err == nil {
		t.Errorf("expected error when alertmanager unmarshal fails, got nil")
	}
}

func TestWebhook_ParseWebhookPayload_GrafanaInvalid(t *testing.T) {
	// "status" triggers the Grafana branch, but "alerts" is the wrong type.
	rawBody := []byte(`{"status": "firing", "alerts": "not-an-array"}`)
	_, _, err := parseWebhookPayload(rawBody)
	if err == nil {
		t.Errorf("expected error when grafana unmarshal fails, got nil")
	}
}

func TestWebhook_ParseWebhookPayload_UniversalInvalid(t *testing.T) {
	// "alerts" triggers the Universal branch, but the payload is structurally invalid JSON.
	rawBody := []byte(`{"alerts": [{"invalid"}]}`)
	_, _, err := parseWebhookPayload(rawBody)
	if err == nil {
		t.Errorf("expected error when universal unmarshal fails, got nil")
	}
}

func TestWebhook_ResultHasError(t *testing.T) {
	if !resultHasError(map[string]any{"status": "error"}) {
		t.Errorf("expected true for status: error")
	}
	if !resultHasError(map[string]any{"icinga_ok": false}) {
		t.Errorf("expected true for icinga_ok: false")
	}
	if !resultHasError(map[string]any{"error": "some error"}) {
		t.Errorf("expected true for error string")
	}
	if resultHasError(map[string]any{"status": "success", "icinga_ok": true}) {
		t.Errorf("expected false for success")
	}
}

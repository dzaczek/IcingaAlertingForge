package handler

import (
	"bytes"
	"encoding/json"
	"icinga-webhook-bridge/auth"
	"icinga-webhook-bridge/cache"
	"icinga-webhook-bridge/config"
	"icinga-webhook-bridge/history"
	"icinga-webhook-bridge/icinga"
	"icinga-webhook-bridge/models"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestWebhook_ServeHTTP_Coverage(t *testing.T) {
	dir, _ := os.MkdirTemp("", "test")
	histLogger, _ := history.NewLogger(filepath.Join(dir, "hist.jsonl"), 100)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"results": [{"code": 200}]}`))
	}))
	defer ts.Close()

	apiClient := icinga.NewAPIClient(ts.URL, "user", "pass", false)

	h := &WebhookHandler{
		KeyStore: auth.NewKeyStore(map[string]config.WebhookRoute{"key": {Source: "src", TargetID: "t1"}}),
		Targets:  map[string]config.TargetConfig{"t1": {HostName: "test-host"}},
		Cache:    cache.NewServiceCache(60),
		History:  histLogger,
		API:      apiClient,
	}

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	// Valid payload
	payload := models.UniversalPayload{
		Alerts: []models.UniversalAlert{
			{Status: "firing", Name: "Test"},
		},
	}
	data, _ := json.Marshal(payload)
	req2 := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(data))
	req2.Header.Set("X-API-Key", "key")

	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, req2)
}

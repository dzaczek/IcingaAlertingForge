package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"icinga-webhook-bridge/cache"
	"icinga-webhook-bridge/config"
	"icinga-webhook-bridge/icinga"
)

func TestStatus_ServeHTTP(t *testing.T) {
	icingaServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{
			"results": [{
				"attrs": {
					"state": 2,
					"last_check_result": {
						"state": 2,
						"output": "CRITICAL: test",
						"execution_end": 1600000000
					}
				}
			}]
		}`))
	}))
	t.Cleanup(icingaServer.Close)

	svcCache := cache.NewServiceCache(60)
	svcCache.Register("host-a", "svc-1")

	h := &StatusHandler{
		Cache: svcCache,
		API: &icinga.APIClient{
			BaseURL:    icingaServer.URL,
			User:       "test",
			Pass:       "test",
			HTTPClient: icingaServer.Client(),
		},
		Targets: map[string]config.TargetConfig{
			"team-a": {ID: "team-a", HostName: "host-a"},
		},
	}

	t.Run("firing service", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/status/svc-1?host=host-a", nil)
		rr := httptest.NewRecorder()

		h.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", rr.Code)
		}

		var resp map[string]any
		json.NewDecoder(rr.Body).Decode(&resp)
		if resp["cache_state"] != "ready" {
			t.Errorf("expected cache_state ready, got %v", resp["cache_state"])
		}
		if resp["exists_in_icinga"] != true {
			t.Error("expected exists_in_icinga true")
		}

		lastCheck := resp["last_check_result"].(map[string]any)
		if lastCheck["exit_status"].(float64) != 2 {
			t.Errorf("expected exit_status 2, got %v", lastCheck["exit_status"])
		}
	})

	t.Run("not found in icinga", func(t *testing.T) {
		// Mock API to return error for non-existent service
		h.API.BaseURL = "http://invalid-url"
		req := httptest.NewRequest(http.MethodGet, "/status/unknown?host=host-a", nil)
		rr := httptest.NewRecorder()

		h.ServeHTTP(rr, req)

		var resp map[string]any
		json.NewDecoder(rr.Body).Decode(&resp)
		if resp["exists_in_icinga"] == true {
			t.Error("expected exists_in_icinga false")
		}
	})
}

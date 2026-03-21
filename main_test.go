package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"icinga-webhook-bridge/cache"
	"icinga-webhook-bridge/icinga"
)

func TestRestoreManagedServicesFromIcinga_RestoresManagedAndLegacyOnly(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/v1/objects/services" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{
			"results": [
				{"attrs": {"name": "IAF Device 01", "vars": {"managed_by": "IcingaAlertingForge"}}},
				{"attrs": {"name": "Legacy Device 02", "vars": {"managed_by": "webhook-bridge"}}},
				{"attrs": {"name": "External Device 03", "vars": {"managed_by": "director"}}}
			]
		}`))
	}))
	defer server.Close()

	apiClient := &icinga.APIClient{
		BaseURL:    server.URL,
		User:       "test",
		Pass:       "test",
		HTTPClient: server.Client(),
	}
	serviceCache := cache.NewServiceCache(10)

	restoreManagedServicesFromIcinga(apiClient, serviceCache, "test-host")

	if !serviceCache.Exists("test-host", "IAF Device 01") {
		t.Fatal("expected IAF-managed service to be restored into cache")
	}
	if !serviceCache.Exists("test-host", "Legacy Device 02") {
		t.Fatal("expected legacy-managed service to be restored into cache")
	}
	if serviceCache.Exists("test-host", "External Device 03") {
		t.Fatal("expected unmanaged service to be ignored")
	}
	if len(serviceCache.All()) != 2 {
		t.Fatalf("expected exactly 2 cached services, got %d", len(serviceCache.All()))
	}
}

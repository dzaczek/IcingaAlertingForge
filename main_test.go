package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"icinga-webhook-bridge/cache"
	"icinga-webhook-bridge/config"
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

func TestStartCacheResync_PeriodicallyReRegistersManagedServices(t *testing.T) {
	calls := make(chan struct{}, 16)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{
			"results": [
				{"attrs": {"name": "IAF Device 01", "vars": {"managed_by": "IcingaAlertingForge"}}}
			]
		}`))
		select {
		case calls <- struct{}{}:
		default:
		}
	}))
	defer server.Close()

	apiClient := &icinga.APIClient{
		BaseURL:    server.URL,
		User:       "test",
		Pass:       "test",
		HTTPClient: server.Client(),
	}
	serviceCache := cache.NewServiceCache(10)
	targets := map[string]config.TargetConfig{
		"default": {HostName: "test-host"},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Drive the loop with an explicit tiny interval so the test does not depend on
	// wall-clock timing; assert on observed periodic passes instead.
	startCacheResyncEvery(ctx, apiClient, serviceCache, targets, 5*time.Millisecond)

	// Expect at least two independent resync passes.
	for i := 0; i < 2; i++ {
		select {
		case <-calls:
		case <-time.After(2 * time.Second):
			t.Fatalf("expected periodic resync pass %d, but none occurred", i+1)
		}
	}

	cancel()

	if !serviceCache.Exists("test-host", "IAF Device 01") {
		t.Fatal("expected managed service to be present in cache after resync")
	}
}

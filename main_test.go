package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"icinga-webhook-bridge/cache"
	"icinga-webhook-bridge/config"
	"icinga-webhook-bridge/icinga"
)

// pruneMockServer mocks the Icinga2 endpoints used by the pruner: ListServices
// (POST collection), getServiceInfo (GET object, for the delete conflict check),
// and DeleteService (DELETE). Deleted object paths are sent to deleted.
func pruneMockServer(t *testing.T, listJSON string, deleted chan<- string) *icinga.APIClient {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/v1/objects/services/"):
			deleted <- r.URL.Path
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"results":[{"code":200.0,"status":"deleted"}]}`))
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/objects/services/"):
			// Conflict check: report the service as managed by us.
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"results":[{"attrs":{"name":"x","check_command":"dummy","vars":{"managed_by":"IcingaAlertingForge"}}}]}`))
		case r.URL.Path == "/v1/objects/services":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(listJSON))
		default:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"results":[]}`))
		}
	}))
	t.Cleanup(srv.Close)
	return &icinga.APIClient{BaseURL: srv.URL, User: "u", Pass: "p", HTTPClient: srv.Client()}
}

// pruneTestList builds a ListServices response with one stale-OK, one fresh-OK,
// one stale-critical, and one unmanaged service.
func pruneTestList() string {
	old := float64(time.Now().Add(-48 * time.Hour).Unix())
	now := float64(time.Now().Unix())
	return fmt.Sprintf(`{"results":[
		{"attrs":{"name":"Stale OK","state":0,"vars":{"managed_by":"IcingaAlertingForge"},"last_check_result":{"state":0,"execution_end":%f}}},
		{"attrs":{"name":"Fresh OK","state":0,"vars":{"managed_by":"IcingaAlertingForge"},"last_check_result":{"state":0,"execution_end":%f}}},
		{"attrs":{"name":"Stale Crit","state":2,"vars":{"managed_by":"IcingaAlertingForge"},"last_check_result":{"state":2,"execution_end":%f}}},
		{"attrs":{"name":"Unmanaged","state":0,"vars":{"managed_by":"director"},"last_check_result":{"state":0,"execution_end":%f}}}
	]}`, old, now, old, old)
}

func TestPruneStaleManagedServices_DryRunDeletesNothing(t *testing.T) {
	deleted := make(chan string, 4)
	api := pruneMockServer(t, pruneTestList(), deleted)
	sc := cache.NewServiceCache(60)

	candidates, del := pruneStaleManagedServices(api, sc, "host-a", time.Hour, true, nil)

	if candidates != 1 {
		t.Fatalf("expected 1 candidate (Stale OK only), got %d", candidates)
	}
	if del != 0 {
		t.Fatalf("dry-run must delete nothing, got %d", del)
	}
	select {
	case p := <-deleted:
		t.Fatalf("dry-run must not call DELETE, but deleted %q", p)
	default:
	}
}

func TestPruneStaleManagedServices_DeletesOnlyStaleOK(t *testing.T) {
	deleted := make(chan string, 4)
	api := pruneMockServer(t, pruneTestList(), deleted)
	sc := cache.NewServiceCache(60)

	candidates, del := pruneStaleManagedServices(api, sc, "host-a", time.Hour, false, nil)

	if candidates != 1 || del != 1 {
		t.Fatalf("expected 1 candidate and 1 deleted, got candidates=%d deleted=%d", candidates, del)
	}
	select {
	case p := <-deleted:
		if !strings.Contains(p, "Stale%20OK") && !strings.Contains(p, "Stale OK") {
			t.Errorf("expected to delete 'Stale OK', deleted %q", p)
		}
	case <-time.After(time.Second):
		t.Fatal("expected a DELETE call")
	}
}

func TestLivePruneState_SetGet(t *testing.T) {
	s := &livePruneState{}
	s.set(30, false, map[string]config.TargetConfig{"a": {HostName: "h1"}})
	days, dry, targets := s.get()
	if days != 30 || dry != false || len(targets) != 1 {
		t.Fatalf("unexpected state: days=%d dry=%v targets=%d", days, dry, len(targets))
	}
}

func TestServicePruner_LiveEnableViaTrigger(t *testing.T) {
	deleted := make(chan string, 4)
	api := pruneMockServer(t, pruneTestList(), deleted)
	sc := cache.NewServiceCache(60)

	// Start disabled — no pass should delete anything yet.
	state := &livePruneState{}
	targets := map[string]config.TargetConfig{"h": {HostName: "host-a"}}
	state.set(0, true, targets)

	trigger := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startServicePruner(ctx, api, sc, nil, state, trigger)

	// Reconfigure live: enable with real deletion, then trigger a pass.
	state.set(1, false, targets)
	trigger <- struct{}{}

	select {
	case p := <-deleted:
		if !strings.Contains(p, "Stale") {
			t.Errorf("expected to delete the stale service, deleted %q", p)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("live enable + trigger should run a prune pass without restart")
	}
}

func TestPruneStaleManagedServices_SkipsFrozen(t *testing.T) {
	deleted := make(chan string, 4)
	api := pruneMockServer(t, pruneTestList(), deleted)
	sc := cache.NewServiceCache(60)
	sc.Freeze("host-a", "Stale OK", nil) // permanent freeze

	candidates, del := pruneStaleManagedServices(api, sc, "host-a", time.Hour, false, nil)

	if candidates != 0 || del != 0 {
		t.Fatalf("frozen stale service must be skipped, got candidates=%d deleted=%d", candidates, del)
	}
}

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

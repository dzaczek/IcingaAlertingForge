package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"icinga-webhook-bridge/cache"
	"icinga-webhook-bridge/models"
)

func TestMapSeverityToExitStatus(t *testing.T) {
	tests := []struct {
		severity string
		want     int
	}{
		{"critical", 2},
		{"warning", 1},
		{"", 2},     // unknown defaults to critical
		{"info", 2}, // unknown defaults to critical
	}

	for _, tt := range tests {
		got := mapSeverityToExitStatus(tt.severity)
		if got != tt.want {
			t.Errorf("mapSeverityToExitStatus(%q) = %d, want %d", tt.severity, got, tt.want)
		}
	}
}

func TestExitStatusLabel(t *testing.T) {
	tests := []struct {
		status int
		want   string
	}{
		{0, "OK"},
		{1, "WARNING"},
		{2, "CRITICAL"},
		{3, "UNKNOWN"},
	}

	for _, tt := range tests {
		got := exitStatusLabel(tt.status)
		if got != tt.want {
			t.Errorf("exitStatusLabel(%d) = %q, want %q", tt.status, got, tt.want)
		}
	}
}

// TestWebhook_DuplicateFiring_WaitsForInFlightCreate guards against a
// regression where a duplicate "firing" webhook for a service that is still
// being auto-created (cache.StatePending) would skip straight to
// SendCheckResult instead of waiting, reaching Icinga2 before the service
// object existed there and getting rejected with 404 "No objects found".
// See ensureServiceExists / needsCreate in work_mode.go.
func TestWebhook_DuplicateFiring_WaitsForInFlightCreate(t *testing.T) {
	const host = "team-a-host"
	const alertname = "Race Test Alert"
	// Default naming mode: "<alertname> - <summary>".
	const service = alertname + " - race regression test"

	var createCalls atomic.Int32
	var created atomic.Bool
	releaseCreate := make(chan struct{})

	h := testWebhookHandler(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/objects/services/"):
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":404,"status":"No objects found."}`))

		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/v1/objects/services/"):
			createCalls.Add(1)
			<-releaseCreate // held open until the test says the second request has already raced in
			created.Store(true)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"results":[{"code":200}]}`))

		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v1/actions/process-check-result"):
			if !created.Load() {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error":404,"status":"No objects found."}`))
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"results":[{"code":200}]}`))

		default:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"results":[]}`))
		}
	})

	body := `{
		"status": "firing",
		"alerts": [{
			"status": "firing",
			"labels": {"alertname": "` + alertname + `", "severity": "critical"},
			"annotations": {"summary": "race regression test"}
		}]
	}`

	post := func() map[string]any {
		req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(body))
		req.Header.Set("X-API-Key", "valid-key")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		var resp map[string]any
		_ = json.Unmarshal(rr.Body.Bytes(), &resp)
		return resp
	}

	// First request starts the create and blocks inside the PUT handler.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		post()
	}()

	// Wait deterministically until the cache shows the create is in flight.
	deadline := time.Now().Add(2 * time.Second)
	for h.Cache.GetState(host, service) != cache.StatePending {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for first request to mark the service pending")
		}
		time.Sleep(time.Millisecond)
	}

	// Second, duplicate request arrives while the first is still creating.
	wg.Add(1)
	var second map[string]any
	go func() {
		defer wg.Done()
		second = post()
	}()

	// Give the second request time to reach ensureServiceExists; with the
	// bug it would return immediately (StatePending == "good enough") and
	// call SendCheckResult before the object exists.
	time.Sleep(100 * time.Millisecond)
	close(releaseCreate)
	wg.Wait()

	if createCalls.Load() != 1 {
		t.Fatalf("expected exactly 1 CreateService call, got %d", createCalls.Load())
	}

	resList, _ := second["results"].([]any)
	if len(resList) != 1 {
		t.Fatalf("expected 1 result in second response, got %#v", second)
	}
	first, _ := resList[0].(map[string]any)
	if ok, _ := first["icinga_ok"].(bool); !ok {
		t.Fatalf("duplicate firing request while create was pending got a failure instead of waiting: %+v", first)
	}
}

// TestWebhook_ServiceNamePerSummary covers one alert rule firing for several
// instances (e.g. one per Kubernetes job). Each distinct summary must get its
// own Icinga2 service, so resolving one instance cannot clear another, and the
// description must be forwarded in the check output.
func TestWebhook_ServiceNamePerSummary(t *testing.T) {
	var mu sync.Mutex
	created := map[string]bool{}
	outputs := map[string]string{}

	h := testWebhookHandler(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet:
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPut:
			mu.Lock()
			created[strings.TrimPrefix(r.URL.Path, "/v1/objects/services/team-a-host!")] = true
			mu.Unlock()
			_, _ = w.Write([]byte(`{"results":[{"code":200}]}`))
		case r.Method == http.MethodPost:
			var p struct {
				Filter       string `json:"filter"`
				PluginOutput string `json:"plugin_output"`
			}
			_ = json.NewDecoder(r.Body).Decode(&p)
			mu.Lock()
			outputs[p.Filter] = p.PluginOutput
			mu.Unlock()
			_, _ = w.Write([]byte(`{"results":[{"code":200}]}`))
		}
	})

	body := `{
		"status": "firing",
		"alerts": [
			{"status": "firing",
			 "labels": {"alertname": "K8s-job-active", "severity": "warning"},
			 "annotations": {"summary": "zrh01-heidi:pbx-services-job-a", "description": "active=1 for 35m"}},
			{"status": "resolved",
			 "labels": {"alertname": "K8s-job-active", "severity": "warning"},
			 "annotations": {"summary": "zrh01-heidi:pbx-services-lab-job-b"}}
		]
	}`
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(body))
	req.Header.Set("X-API-Key", "valid-key")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	svcA := "K8s-job-active - zrh01-heidi:pbx-services-job-a"
	svcB := "K8s-job-active - zrh01-heidi:pbx-services-lab-job-b"
	if !created[svcA] || !created[svcB] || len(created) != 2 {
		t.Fatalf("expected services %q and %q, got %v", svcA, svcB, created)
	}

	filter := func(svc string) string {
		return `host.name=="team-a-host" && service.name==` + strconv.Quote(svc)
	}
	if got, want := outputs[filter(svcA)], "WARNING: zrh01-heidi:pbx-services-job-a\nactive=1 for 35m"; got != want {
		t.Errorf("output for A = %q, want %q", got, want)
	}
	if got, want := outputs[filter(svcB)], "OK: zrh01-heidi:pbx-services-lab-job-b"; got != want {
		t.Errorf("output for B = %q, want %q", got, want)
	}
}

// TestWebhook_ServiceNameModeAlertname keeps the legacy behaviour available:
// all instances share one service named after the alertname.
func TestWebhook_ServiceNameModeAlertname(t *testing.T) {
	var paths []string
	var mu sync.Mutex
	h := testWebhookHandler(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Method == http.MethodPut {
			mu.Lock()
			paths = append(paths, r.URL.Path)
			mu.Unlock()
		}
		_, _ = w.Write([]byte(`{"results":[{"code":200}]}`))
	})
	h.ServiceNameMode = models.ServiceNameModeAlertname

	body := `{"status":"firing","alerts":[{"status":"firing","labels":{"alertname":"HighCPU"},"annotations":{"summary":"web-01"}}]}`
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(body))
	req.Header.Set("X-API-Key", "valid-key")
	h.ServeHTTP(httptest.NewRecorder(), req)

	if len(paths) != 1 || paths[0] != "/v1/objects/services/team-a-host!HighCPU" {
		t.Fatalf("expected single service HighCPU, got %v", paths)
	}
}

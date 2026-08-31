package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"icinga-webhook-bridge/queue"

	"icinga-webhook-bridge/cache"
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
	const service = "Race Test Alert"

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
			"labels": {"alertname": "` + service + `", "severity": "critical"},
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

func TestWebhook_IcingaError_QueuesRetry(t *testing.T) {
	const host = "team-a-host"
	const service = "Retry Test Alert"

	h := testWebhookHandler(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/objects/services/"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"results":[{"code":200}]}`))
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v1/actions/process-check-result"):
			// Simulate a transient Icinga2 failure
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":500,"status":"Internal Server Error"}`))
		default:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"results":[]}`))
		}
	})

	h.RetryQueue = queue.New(queue.Config{MaxSize: 100}, nil)

	body := `{
		"status": "firing",
		"alerts": [{
			"status": "firing",
			"labels": {"alertname": "` + service + `", "severity": "critical"},
			"annotations": {"summary": "retry test"}
		}]
	}`

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(body))
	req.Header.Set("X-API-Key", "valid-key")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if h.RetryQueue.Depth() != 1 {
		t.Fatalf("expected exactly 1 item in retry queue, got %d", h.RetryQueue.Depth())
	}
}

package icinga

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSendCheckResult_Success(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/v1/actions/process-check-result" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		var payload processCheckResultPayload
		json.NewDecoder(r.Body).Decode(&payload)

		if payload.ExitStatus != 2 {
			t.Errorf("expected exit_status 2, got %d", payload.ExitStatus)
		}
		if payload.Type != "Service" {
			t.Errorf("expected type Service, got %s", payload.Type)
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"results":[{"code":200}]}`))
	}))
	defer server.Close()

	client := &APIClient{
		BaseURL:    server.URL,
		User:       "test",
		Pass:       "test",
		HTTPClient: server.Client(),
	}

	err := client.SendCheckResult("test-host", "Test Service", 2, "CRITICAL: something broke")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSendCheckResult_ServerError(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"internal error"}`))
	}))
	defer server.Close()

	client := &APIClient{
		BaseURL:    server.URL,
		User:       "test",
		Pass:       "test",
		HTTPClient: server.Client(),
	}

	err := client.SendCheckResult("test-host", "Test Service", 2, "CRITICAL: test")
	if err == nil {
		t.Error("expected error for 500 response")
	}
}

func TestSendCheckResult_BasicAuth(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "apiuser" || pass != "apipass" {
			t.Errorf("unexpected auth: user=%q pass=%q ok=%v", user, pass, ok)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := &APIClient{
		BaseURL:    server.URL,
		User:       "apiuser",
		Pass:       "apipass",
		HTTPClient: server.Client(),
	}

	err := client.SendCheckResult("host", "svc", 0, "OK")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCreateService_Success(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("expected PUT, got %s", r.Method)
		}
		if r.URL.Path != "/v1/objects/services/test-host!My Service" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		var payload map[string]any
		json.NewDecoder(r.Body).Decode(&payload)

		attrs := payload["attrs"].(map[string]any)
		if attrs["check_command"] != "dummy" {
			t.Errorf("expected check_command dummy, got %v", attrs["check_command"])
		}
		if attrs["enable_active_checks"] != false {
			t.Errorf("expected enable_active_checks false")
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"results":[{"code":200}]}`))
	}))
	defer server.Close()

	client := &APIClient{
		BaseURL:    server.URL,
		User:       "test",
		Pass:       "test",
		HTTPClient: server.Client(),
	}

	err := client.CreateService("test-host", "My Service")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCreateService_Error(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"conflict"}`))
	}))
	defer server.Close()

	client := &APIClient{
		BaseURL:    server.URL,
		User:       "test",
		Pass:       "test",
		HTTPClient: server.Client(),
	}

	err := client.CreateService("test-host", "Existing Service")
	if err == nil {
		t.Error("expected error for 500 response")
	}
}

func TestDeleteService_Success(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("expected DELETE, got %s", r.Method)
		}
		if r.URL.Path != "/v1/objects/services/test-host!My Service" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("cascade") != "1" {
			t.Error("expected cascade=1 query param")
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"results":[{"code":200}]}`))
	}))
	defer server.Close()

	client := &APIClient{
		BaseURL:    server.URL,
		User:       "test",
		Pass:       "test",
		HTTPClient: server.Client(),
	}

	err := client.DeleteService("test-host", "My Service")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDeleteService_Error(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":"not found"}`))
	}))
	defer server.Close()

	client := &APIClient{
		BaseURL:    server.URL,
		User:       "test",
		Pass:       "test",
		HTTPClient: server.Client(),
	}

	err := client.DeleteService("test-host", "Missing Service")
	if err == nil {
		t.Error("expected error for 404 response")
	}
}

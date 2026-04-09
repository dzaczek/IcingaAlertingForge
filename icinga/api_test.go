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

func TestConflictDetection(t *testing.T) {
	mux := http.NewServeMux()

	// Managed host
	mux.HandleFunc("/v1/objects/hosts/managed-host", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"results":[{"attrs":{"vars":{"managed_by":"IcingaAlertingForge"},"check_command":"dummy"}}]}`))
	})
	// Unmanaged host
	mux.HandleFunc("/v1/objects/hosts/unmanaged-host", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"results":[{"attrs":{"check_command":"hostalive"}}]}`))
	})
	// Managed service
	mux.HandleFunc("/v1/objects/services/host!managed-svc", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"results":[{"attrs":{"name":"managed-svc","vars":{"managed_by":"IcingaAlertingForge"},"check_command":"dummy"}}]}`))
	})
	// Unmanaged service
	mux.HandleFunc("/v1/objects/services/host!unmanaged-svc", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"results":[{"attrs":{"name":"unmanaged-svc","check_command":"check_cpu"}}]}`))
	})
	// Non-existent
	mux.HandleFunc("/v1/objects/hosts/missing", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	// Delete mock success
	mux.HandleFunc("/v1/objects/services/host!managed-svc-del", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"results":[{"attrs":{"name":"managed-svc-del","vars":{"managed_by":"IcingaAlertingForge"}}}]}`))
		} else {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"results":[{"code":200}]}`))
		}
	})

	server := httptest.NewTLSServer(mux)
	defer server.Close()

	client := &APIClient{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	}

	t.Run("CreateHost_Managed_Success", func(t *testing.T) {
		err := client.CreateHost(HostSpec{Name: "managed-host"})
		if err != nil {
			t.Errorf("expected no error for managed host, got %v", err)
		}
	})

	t.Run("CreateHost_Unmanaged_Fail", func(t *testing.T) {
		client.ConflictPolicy = ConflictPolicyFail
		err := client.CreateHost(HostSpec{Name: "unmanaged-host"})
		if err == nil {
			t.Fatal("expected conflict error")
		}
		if _, ok := err.(*ErrConflict); !ok {
			t.Errorf("expected ErrConflict, got %T", err)
		}
	})

	t.Run("CreateHost_Unmanaged_Force_Success", func(t *testing.T) {
		client.Force = true
		err := client.CreateHost(HostSpec{Name: "unmanaged-host"})
		if err != nil {
			t.Errorf("expected no error with Force=true, got %v", err)
		}
		client.Force = false
	})

	t.Run("CreateService_Unmanaged_Skip", func(t *testing.T) {
		client.ConflictPolicy = ConflictPolicySkip
		err := client.CreateService("host", "unmanaged-svc", nil, nil)
		if err == nil {
			t.Fatal("expected conflict error for skip policy")
		}
	})

	t.Run("DeleteService_Unmanaged_Fail", func(t *testing.T) {
		err := client.DeleteService("host", "unmanaged-svc")
		if err == nil {
			t.Fatal("expected conflict error for deletion of unmanaged service")
		}
	})

	t.Run("DeleteService_Managed_Success", func(t *testing.T) {
		err := client.DeleteService("host", "managed-svc-del")
		if err != nil {
			t.Errorf("expected success for managed service deletion, got %v", err)
		}
	})
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
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusNotFound)
			return
		}
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

		// Verify notes contain the summary
		notes, _ := attrs["notes"].(string)
		if notes != "CPU is too high" {
			t.Errorf("expected notes to contain summary, got %q", notes)
		}

		// Verify vars contain labels and annotations
		vars, _ := attrs["vars"].(map[string]any)
		if vars["managed_by"] != ManagedByIAF {
			t.Errorf("expected managed_by=%s, got %v", ManagedByIAF, vars["managed_by"])
		}
		if vars["iaf_managed"] != true {
			t.Errorf("expected iaf_managed=true, got %v", vars["iaf_managed"])
		}
		if vars["iaf_component"] != ManagedByIAF {
			t.Errorf("expected iaf_component=%s, got %v", ManagedByIAF, vars["iaf_component"])
		}
		if vars["iaf_host"] != "test-host" {
			t.Errorf("expected iaf_host=test-host, got %v", vars["iaf_host"])
		}
		if vars["iaf_created_at"] == "" {
			t.Error("expected iaf_created_at to be set")
		}
		if vars["bridge_host"] != "test-host" {
			t.Errorf("expected bridge_host=test-host, got %v", vars["bridge_host"])
		}
		if vars["bridge_created_at"] == "" {
			t.Error("expected bridge_created_at to be set")
		}
		if vars["grafana_label_severity"] != "critical" {
			t.Errorf("expected grafana_label_severity=critical, got %v", vars["grafana_label_severity"])
		}
		if vars["grafana_annotation_summary"] != "CPU is too high" {
			t.Errorf("expected grafana_annotation_summary, got %v", vars["grafana_annotation_summary"])
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

	labels := map[string]string{
		"alertname": "My Service",
		"severity":  "critical",
	}
	annotations := map[string]string{
		"summary": "CPU is too high",
	}

	err := client.CreateService("test-host", "My Service", labels, annotations)
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

	err := client.CreateService("test-host", "Existing Service", nil, nil)
	if err == nil {
		t.Error("expected error for 500 response")
	}
}

func TestListServices_ParsesManagedMetadata(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{
			"results": [{
				"attrs": {
					"name": "Synthetic Device 01",
					"display_name": "Synthetic Device 01 - CRITICAL",
					"state": 2,
					"notes": "Managed by IcingaAlertingForge",
					"vars": {
						"managed_by": "IcingaAlertingForge",
						"iaf_created_at": "2026-03-20T19:40:00Z"
					},
					"last_check_result": {
						"state": 2,
						"output": "CRITICAL",
						"execution_end": 1700000000
					}
				}
			}]
		}`))
	}))
	defer server.Close()

	client := &APIClient{
		BaseURL:    server.URL,
		User:       "test",
		Pass:       "test",
		HTTPClient: server.Client(),
	}

	services, err := client.ListServices("test-host")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(services))
	}
	if services[0].HostName != "test-host" {
		t.Fatalf("expected host test-host, got %q", services[0].HostName)
	}
	if services[0].ManagedBy != ManagedByIAF {
		t.Fatalf("expected managed_by %s, got %q", ManagedByIAF, services[0].ManagedBy)
	}
	if services[0].BridgeCreatedAt != "2026-03-20T19:40:00Z" {
		t.Fatalf("unexpected bridge_created_at: %q", services[0].BridgeCreatedAt)
	}
	if !services[0].IsManagedByUs() {
		t.Fatal("expected service to be recognized as managed by us")
	}
	if services[0].IsLegacyManagedByUs() {
		t.Fatal("expected service to use the new managed_by marker")
	}
}

func TestListServices_ParsesLegacyCreatedAtFallback(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{
			"results": [{
				"attrs": {
					"name": "Legacy Service",
					"display_name": "Legacy Service",
					"notes": "Managed by webhook-bridge",
					"vars": {
						"managed_by": "webhook-bridge",
						"bridge_created_at": "2026-03-19T10:00:00Z"
					}
				}
			}]
		}`))
	}))
	defer server.Close()

	client := &APIClient{
		BaseURL:    server.URL,
		User:       "test",
		Pass:       "test",
		HTTPClient: server.Client(),
	}

	services, err := client.ListServices("test-host")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(services))
	}
	if services[0].BridgeCreatedAt != "2026-03-19T10:00:00Z" {
		t.Fatalf("unexpected legacy bridge_created_at: %q", services[0].BridgeCreatedAt)
	}
	if !services[0].IsLegacyManagedByUs() {
		t.Fatal("expected service to be recognized as legacy-managed")
	}
}

func TestDeleteService_Success(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"results":[{"attrs":{"vars":{"managed_by":"IcingaAlertingForge"}}}]}`))
			return
		}
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

func TestGetServiceStatus_UsesPostFilterAndReturnsState(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("X-HTTP-Method-Override") != "GET" {
			t.Fatalf("expected X-HTTP-Method-Override=GET, got %q", r.Header.Get("X-HTTP-Method-Override"))
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("failed to decode payload: %v", err)
		}
		if payload["filter"] != `host.name=="b-dummy-device" && service.name=="Team B Manual Check"` {
			t.Fatalf("unexpected filter: %v", payload["filter"])
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{
			"results": [{
				"attrs": {
					"state": 2,
					"last_check_result": {
						"state": 2,
						"output": "CRITICAL: Manual Team B routing test",
						"execution_end": 1700000000
					}
				}
			}]
		}`))
	}))
	defer server.Close()

	client := &APIClient{
		BaseURL:    server.URL,
		User:       "test",
		Pass:       "test",
		HTTPClient: server.Client(),
	}

	exitStatus, output, checkTime, err := client.GetServiceStatus("b-dummy-device", "Team B Manual Check")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exitStatus != 2 {
		t.Fatalf("expected exit status 2, got %d", exitStatus)
	}
	if output != "CRITICAL: Manual Team B routing test" {
		t.Fatalf("unexpected output: %q", output)
	}
	if checkTime.IsZero() {
		t.Fatal("expected non-zero checkTime")
	}
}

func TestGetHostInfo_ExistsManagedByUs(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/objects/hosts/test-host" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"results":[{"attrs":{
			"check_command":"dummy",
			"display_name":"Test Host",
			"address":"127.0.0.1",
			"vars":{"managed_by":"IcingaAlertingForge","iaf_managed":true,"os":"Linux"}
		}}]}`))
	}))
	defer server.Close()

	client := &APIClient{
		BaseURL:    server.URL,
		User:       "test",
		Pass:       "test",
		HTTPClient: server.Client(),
	}

	info, err := client.GetHostInfo("test-host")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !info.Exists {
		t.Error("expected host to exist")
	}
	if !info.IsDummy() {
		t.Errorf("expected dummy, got check_command=%q", info.CheckCommand)
	}
	if !info.IsManagedByUs() {
		t.Errorf("expected managed_by=%s, got %q", ManagedByIAF, info.ManagedBy)
	}
	if info.IsLegacyManagedByUs() {
		t.Error("expected host to use the new managed_by marker")
	}
}

func TestGetHostInfo_ExistsManagedByLegacyMarker(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"results":[{"attrs":{
			"check_command":"dummy",
			"display_name":"Test Host (webhook-bridge)",
			"address":"127.0.0.1",
			"vars":{"managed_by":"webhook-bridge","os":"Linux"}
		}}]}`))
	}))
	defer server.Close()

	client := &APIClient{
		BaseURL:    server.URL,
		User:       "test",
		Pass:       "test",
		HTTPClient: server.Client(),
	}

	info, err := client.GetHostInfo("test-host")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !info.IsManagedByUs() {
		t.Fatal("expected legacy host to still be recognized as managed by us")
	}
	if !info.IsLegacyManagedByUs() {
		t.Fatal("expected legacy host marker to be detected")
	}
}

func TestGetHostInfo_ExistsNotDummy(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"results":[{"attrs":{
			"check_command":"hostalive",
			"display_name":"Production Server",
			"address":"10.0.0.1",
			"vars":{"os":"Linux"}
		}}]}`))
	}))
	defer server.Close()

	client := &APIClient{
		BaseURL:    server.URL,
		User:       "test",
		Pass:       "test",
		HTTPClient: server.Client(),
	}

	info, err := client.GetHostInfo("prod-server")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !info.Exists {
		t.Error("expected host to exist")
	}
	if info.IsDummy() {
		t.Error("expected non-dummy host")
	}
	if info.IsManagedByUs() {
		t.Error("expected not managed by us")
	}
}

func TestGetHostInfo_NotFound(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":404,"status":"No objects found."}`))
	}))
	defer server.Close()

	client := &APIClient{
		BaseURL:    server.URL,
		User:       "test",
		Pass:       "test",
		HTTPClient: server.Client(),
	}

	info, err := client.GetHostInfo("nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Exists {
		t.Error("expected host to not exist")
	}
}

func TestCreateHost_Success(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Method != http.MethodPut {
			t.Errorf("expected PUT, got %s", r.Method)
		}
		if r.URL.Path != "/v1/objects/hosts/new-host" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		var payload map[string]any
		json.NewDecoder(r.Body).Decode(&payload)

		attrs := payload["attrs"].(map[string]any)
		if attrs["check_command"] != "dummy" {
			t.Errorf("expected check_command dummy, got %v", attrs["check_command"])
		}
		if _, ok := attrs["address"]; ok {
			t.Errorf("expected passive dummy host without address attr, got %v", attrs["address"])
		}
		if attrs["max_check_attempts"] != float64(1) && attrs["max_check_attempts"] != 1 {
			t.Errorf("expected max_check_attempts=1, got %v", attrs["max_check_attempts"])
		}
		vars := attrs["vars"].(map[string]any)
		if vars["managed_by"] != ManagedByIAF {
			t.Errorf("expected managed_by %s, got %v", ManagedByIAF, vars["managed_by"])
		}
		if vars["iaf_managed"] != true {
			t.Errorf("expected iaf_managed=true, got %v", vars["iaf_managed"])
		}
		if vars["iaf_created_at"] == "" {
			t.Error("expected iaf_created_at to be set")
		}
		if vars["iaf_host_address"] != "127.0.0.1" {
			t.Errorf("expected iaf_host_address=127.0.0.1, got %v", vars["iaf_host_address"])
		}
		notification := vars["notification"].(map[string]any)
		users := notification["users"].([]any)
		if len(users) != 2 || users[0] != "alpha" || users[1] != "omega" {
			t.Errorf("unexpected notification users: %#v", users)
		}
		groups := notification["groups"].([]any)
		if len(groups) != 1 || groups[0] != "sre-oncall" {
			t.Errorf("unexpected notification groups: %#v", groups)
		}
		userGroups := notification["user_groups"].([]any)
		if len(userGroups) != 1 || userGroups[0] != "sre-oncall" {
			t.Errorf("unexpected notification user_groups: %#v", userGroups)
		}
		serviceStates := notification["service_states"].([]any)
		if len(serviceStates) != 1 || serviceStates[0] != "critical" {
			t.Errorf("unexpected notification service_states: %#v", serviceStates)
		}
		sms := notification["sms"].(map[string]any)
		if sms["users"].([]any)[0] != "alpha" {
			t.Errorf("expected sms alias to mirror users, got %#v", sms["users"])
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

	err := client.CreateHost(HostSpec{
		Name:        "new-host",
		DisplayName: "New Host (test)",
		Address:     "127.0.0.1",
		Notification: HostNotificationConfig{
			Users:         []string{"alpha", "omega"},
			Groups:        []string{"sre-oncall"},
			ServiceStates: []string{"critical"},
		},
	})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCreateHost_Error(t *testing.T) {
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

	err := client.CreateHost(HostSpec{Name: "bad-host"})
	if err == nil {
		t.Error("expected error for 500 response")
	}
}

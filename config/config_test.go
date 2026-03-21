package config

import (
	"os"
	"testing"
)

func setLegacyTestEnv(t *testing.T) {
	t.Helper()
	envVars := map[string]string{
		"WEBHOOK_KEY_GRAFANA_PROD": "test-key-prod",
		"WEBHOOK_KEY_GRAFANA_DEV":  "test-key-dev",
		"ICINGA2_HOST":             "https://icinga2.test:5665",
		"ICINGA2_USER":             "apiuser",
		"ICINGA2_PASS":             "apipass",
		"ICINGA2_HOST_NAME":        "test-host",
	}
	for k, v := range envVars {
		t.Setenv(k, v)
	}
}

func setMultiTargetEnv(t *testing.T) {
	t.Helper()
	envVars := map[string]string{
		"ICINGA2_HOST":                                  "https://icinga2.test:5665",
		"ICINGA2_USER":                                  "apiuser",
		"ICINGA2_PASS":                                  "apipass",
		"IAF_TARGET_TEAM_A_HOST_NAME":                   "a-dummy-dev",
		"IAF_TARGET_TEAM_A_HOST_DISPLAY":                "A Dummy Dev",
		"IAF_TARGET_TEAM_A_API_KEYS":                    "key-a-1,key-a-2",
		"IAF_TARGET_TEAM_A_NOTIFICATION_USERS":          "alpha,omega",
		"IAF_TARGET_TEAM_A_NOTIFICATION_SERVICE_STATES": "critical",
		"IAF_TARGET_TEAM_B_HOST_NAME":                   "b-dummy-device",
		"IAF_TARGET_TEAM_B_API_KEYS":                    "key-b-1",
		"IAF_TARGET_TEAM_B_NOTIFICATION_USERS":          "beta,ceta",
		"IAF_TARGET_TEAM_B_NOTIFICATION_SERVICE_STATES": "critical",
	}
	for k, v := range envVars {
		t.Setenv(k, v)
	}
}

func TestLoad_ValidLegacyConfig(t *testing.T) {
	setLegacyTestEnv(t)

	cfg := Load()

	if cfg.ServerPort != "8080" {
		t.Errorf("expected default server port 8080, got %s", cfg.ServerPort)
	}
	if cfg.Icinga2Host != "https://icinga2.test:5665" {
		t.Errorf("expected Icinga2Host, got %s", cfg.Icinga2Host)
	}
	if cfg.Icinga2HostName != "test-host" {
		t.Errorf("expected Icinga2HostName test-host, got %s", cfg.Icinga2HostName)
	}
	if len(cfg.WebhookRoutes) != 2 {
		t.Errorf("expected 2 webhook routes, got %d", len(cfg.WebhookRoutes))
	}
	if route, ok := cfg.WebhookRoutes["test-key-prod"]; !ok || route.Source != "grafana-prod" || route.TargetID != "default" {
		t.Errorf("expected grafana-prod/default route for test-key-prod, got %+v (found=%v)", route, ok)
	}
	if cfg.DefaultTarget().HostName != "test-host" {
		t.Errorf("expected default target host test-host, got %s", cfg.DefaultTarget().HostName)
	}
}

func TestLoad_ValidMultiTargetConfig(t *testing.T) {
	setMultiTargetEnv(t)

	cfg := Load()

	if len(cfg.Targets) != 2 {
		t.Fatalf("expected 2 targets, got %d", len(cfg.Targets))
	}
	targetA := cfg.Targets["team-a"]
	if targetA.HostName != "a-dummy-dev" {
		t.Fatalf("expected team-a host a-dummy-dev, got %s", targetA.HostName)
	}
	if targetA.HostDisplay != "A Dummy Dev" {
		t.Fatalf("expected team-a display name, got %s", targetA.HostDisplay)
	}
	if len(targetA.Notification.Users) != 2 || targetA.Notification.Users[0] != "alpha" || targetA.Notification.Users[1] != "omega" {
		t.Fatalf("unexpected team-a notification users: %#v", targetA.Notification.Users)
	}
	if len(targetA.Notification.ServiceStates) != 1 || targetA.Notification.ServiceStates[0] != "critical" {
		t.Fatalf("unexpected team-a service states: %#v", targetA.Notification.ServiceStates)
	}
	if route, ok := cfg.WebhookRoutes["key-a-2"]; !ok || route.TargetID != "team-a" || route.Source != "team-a" {
		t.Fatalf("unexpected route for key-a-2: %+v found=%v", route, ok)
	}
	if route, ok := cfg.WebhookRoutes["key-b-1"]; !ok || route.TargetID != "team-b" || route.Source != "team-b" {
		t.Fatalf("unexpected route for key-b-1: %+v found=%v", route, ok)
	}
}

func TestLoad_MissingRequiredVar(t *testing.T) {
	t.Setenv("WEBHOOK_KEY_TEST", "key123")

	os.Unsetenv("ICINGA2_HOST")

	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for missing required env var")
		}
	}()
	Load()
}

func TestLoad_NoWebhookKeys(t *testing.T) {
	t.Setenv("ICINGA2_HOST", "https://test:5665")
	t.Setenv("ICINGA2_USER", "user")
	t.Setenv("ICINGA2_PASS", "pass")
	t.Setenv("ICINGA2_HOST_NAME", "host")

	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for no webhook keys")
		}
	}()
	Load()
}

func TestLoad_CustomPort(t *testing.T) {
	setLegacyTestEnv(t)
	t.Setenv("SERVER_PORT", "9090")

	cfg := Load()

	if cfg.ServerPort != "9090" {
		t.Errorf("expected port 9090, got %s", cfg.ServerPort)
	}
	if cfg.ListenAddr() != "0.0.0.0:9090" {
		t.Errorf("expected listen addr 0.0.0.0:9090, got %s", cfg.ListenAddr())
	}
}

func TestLoad_TLSSkipVerify(t *testing.T) {
	setLegacyTestEnv(t)
	t.Setenv("ICINGA2_TLS_SKIP_VERIFY", "true")

	cfg := Load()

	if !cfg.Icinga2TLSSkipVerify {
		t.Error("expected TLS skip verify to be true")
	}
}

func TestLoadLegacyWebhookKeys_SourceNaming(t *testing.T) {
	t.Setenv("WEBHOOK_KEY_MY_GRAFANA_INSTANCE", "key-123")

	keys := loadLegacyWebhookKeys()

	source, ok := keys["key-123"]
	if !ok {
		t.Fatal("expected key to be found")
	}
	if source != "my-grafana-instance" {
		t.Errorf("expected source my-grafana-instance, got %s", source)
	}
}

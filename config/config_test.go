package config

import (
	"os"
	"testing"
)

func setTestEnv(t *testing.T) {
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

func TestLoad_ValidConfig(t *testing.T) {
	setTestEnv(t)

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
	if len(cfg.WebhookKeys) != 2 {
		t.Errorf("expected 2 webhook keys, got %d", len(cfg.WebhookKeys))
	}
	if source, ok := cfg.WebhookKeys["test-key-prod"]; !ok || source != "grafana-prod" {
		t.Errorf("expected source grafana-prod for test-key-prod, got %q (found=%v)", source, ok)
	}
}

func TestLoad_MissingRequiredVar(t *testing.T) {
	// Only set webhook key, miss ICINGA2_HOST
	t.Setenv("WEBHOOK_KEY_TEST", "key123")

	// Clear required vars
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
	setTestEnv(t)
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
	setTestEnv(t)
	t.Setenv("ICINGA2_TLS_SKIP_VERIFY", "true")

	cfg := Load()

	if !cfg.Icinga2TLSSkipVerify {
		t.Error("expected TLS skip verify to be true")
	}
}

func TestLoadWebhookKeys_SourceNaming(t *testing.T) {
	t.Setenv("WEBHOOK_KEY_MY_GRAFANA_INSTANCE", "key-123")

	keys := loadWebhookKeys()

	source, ok := keys["key-123"]
	if !ok {
		t.Fatal("expected key to be found")
	}
	if source != "my-grafana-instance" {
		t.Errorf("expected source my-grafana-instance, got %s", source)
	}
}

package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

// Config holds all application configuration loaded from environment variables.
type Config struct {
	// Server
	ServerPort string
	ServerHost string

	// Webhook API keys: map[key_value] -> source_name
	WebhookKeys map[string]string

	// Icinga2 REST API
	Icinga2Host          string
	Icinga2User          string
	Icinga2Pass          string
	Icinga2HostName      string
	Icinga2TLSSkipVerify bool

	// History
	HistoryFile       string
	HistoryMaxEntries int

	// Cache
	CacheTTLMinutes int

	// Logging
	LogLevel  string
	LogFormat string
}

// Load reads the .env file and populates the Config struct.
// It panics if any required variable is missing.
func Load() *Config {
	// Load .env file if it exists (ignore error — env vars may come from the system)
	_ = godotenv.Load()

	cfg := &Config{
		ServerPort:  getEnvOrDefault("SERVER_PORT", "8080"),
		ServerHost:  getEnvOrDefault("SERVER_HOST", "0.0.0.0"),
		WebhookKeys: loadWebhookKeys(),

		Icinga2Host:          requireEnv("ICINGA2_HOST"),
		Icinga2User:          requireEnv("ICINGA2_USER"),
		Icinga2Pass:          requireEnv("ICINGA2_PASS"),
		Icinga2HostName:      requireEnv("ICINGA2_HOST_NAME"),
		Icinga2TLSSkipVerify: getEnvBool("ICINGA2_TLS_SKIP_VERIFY", false),

		HistoryFile:       getEnvOrDefault("HISTORY_FILE", "/var/log/webhook-bridge/history.jsonl"),
		HistoryMaxEntries: getEnvInt("HISTORY_MAX_ENTRIES", 10000),

		CacheTTLMinutes: getEnvInt("CACHE_TTL_MINUTES", 60),

		LogLevel:  getEnvOrDefault("LOG_LEVEL", "info"),
		LogFormat: getEnvOrDefault("LOG_FORMAT", "json"),
	}

	if len(cfg.WebhookKeys) == 0 {
		panic("config: at least one WEBHOOK_KEY_* environment variable is required")
	}

	return cfg
}

// ListenAddr returns the full listen address (host:port).
func (c *Config) ListenAddr() string {
	return c.ServerHost + ":" + c.ServerPort
}

// loadWebhookKeys scans environment variables for the WEBHOOK_KEY_ prefix
// and builds a map from key value to source name.
func loadWebhookKeys() map[string]string {
	keys := make(map[string]string)
	for _, env := range os.Environ() {
		parts := strings.SplitN(env, "=", 2)
		if len(parts) != 2 {
			continue
		}
		name, value := parts[0], parts[1]
		if strings.HasPrefix(name, "WEBHOOK_KEY_") && value != "" {
			// Extract source name: WEBHOOK_KEY_GRAFANA_PROD -> grafana-prod
			source := strings.TrimPrefix(name, "WEBHOOK_KEY_")
			source = strings.ToLower(strings.ReplaceAll(source, "_", "-"))
			keys[value] = source
		}
	}
	return keys
}

func requireEnv(key string) string {
	val := os.Getenv(key)
	if val == "" {
		panic(fmt.Sprintf("config: required environment variable %s is not set", key))
	}
	return val
}

func getEnvOrDefault(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

func getEnvBool(key string, defaultVal bool) bool {
	val := os.Getenv(key)
	if val == "" {
		return defaultVal
	}
	b, err := strconv.ParseBool(val)
	if err != nil {
		return defaultVal
	}
	return b
}

func getEnvInt(key string, defaultVal int) int {
	val := os.Getenv(key)
	if val == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(val)
	if err != nil {
		return defaultVal
	}
	return n
}

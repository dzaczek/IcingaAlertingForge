package config

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

// NotificationConfig holds per-target Icinga notification customization.
type NotificationConfig struct {
	Users         []string
	Groups        []string
	ServiceStates []string
	HostStates    []string
}

// TargetConfig describes a single managed dummy host and its webhook routing.
type TargetConfig struct {
	ID           string
	Source       string
	HostName     string
	HostDisplay  string
	HostAddress  string
	Notification NotificationConfig
}

// WebhookRoute describes how a single API key maps to a source and target.
type WebhookRoute struct {
	Source   string
	TargetID string
}

// Config holds all application configuration loaded from environment variables.
type Config struct {
	// Server
	ServerPort string
	ServerHost string

	// Webhook routing.
	WebhookKeys     map[string]string
	WebhookRoutes   map[string]WebhookRoute
	Targets         map[string]TargetConfig
	DefaultTargetID string

	// Icinga2 REST API
	Icinga2Host           string
	Icinga2User           string
	Icinga2Pass           string
	Icinga2HostName       string
	Icinga2HostDisplay    string
	Icinga2HostAddress    string
	Icinga2HostAutoCreate bool
	Icinga2TLSSkipVerify  bool

	// History
	HistoryFile       string
	HistoryMaxEntries int

	// Cache
	CacheTTLMinutes int

	// Logging
	LogLevel  string
	LogFormat string

	// Admin dashboard auth
	AdminUser string
	AdminPass string

	// Rate limiting for Icinga2 API
	RateLimitMutate   int // max concurrent create/delete operations
	RateLimitStatus   int // max concurrent status update operations
	RateLimitMaxQueue int // max queued status operations

	// Retry queue
	RetryQueueEnabled       bool
	RetryQueueMaxSize       int
	RetryQueueFilePath      string
	RetryQueueRetryBaseSec  int
	RetryQueueRetryMaxSec   int
	RetryQueueCheckInterval int

	// Health checker (reverse health monitoring)
	HealthCheckEnabled     bool
	HealthCheckIntervalSec int
	HealthCheckServiceName string
	HealthCheckTargetHost  string // host to register under (default: first target)
	HealthCheckRegister    bool   // auto-create service in Icinga2

	// Audit log / SIEM
	AuditLogEnabled bool
	AuditLogFile    string
	AuditLogFormat  string // "json" or "cef"

	// Dashboard config mode
	ConfigInDashboard   bool   // if true, config is managed via admin panel
	ConfigEncryptionKey string // AES key for encrypting secrets (auto-generated if empty)
	ConfigFilePath      string // path to JSON config file
}

// Load reads the .env file and populates the Config struct.
// It returns an error if any required variable is missing or invalid.
func Load() (*Config, error) {
	// Load .env file if it exists (ignore error — env vars may come from the system)
	_ = godotenv.Load()

	targets, routes, defaultTargetID, err := loadTargetsAndRoutes()
	if err != nil {
		return nil, err
	}

	webhookKeys := make(map[string]string, len(routes))
	for key, route := range routes {
		webhookKeys[key] = route.Source
	}

	cfg := &Config{
		ServerPort:      getEnvOrDefault("SERVER_PORT", "8080"),
		ServerHost:      getEnvOrDefault("SERVER_HOST", "0.0.0.0"),
		WebhookKeys:     webhookKeys,
		WebhookRoutes:   routes,
		Targets:         targets,
		DefaultTargetID: defaultTargetID,

		Icinga2HostName:    getLegacyHostName(targets, defaultTargetID),
		Icinga2HostDisplay: getEnvOrDefault("ICINGA2_HOST_DISPLAY", ""),
		Icinga2HostAddress: getEnvOrDefault("ICINGA2_HOST_ADDRESS", ""),

		HistoryFile: getEnvOrDefault("HISTORY_FILE", "/var/log/webhook-bridge/history.jsonl"),

		LogLevel:  getEnvOrDefault("LOG_LEVEL", "info"),
		LogFormat: getEnvOrDefault("LOG_FORMAT", "json"),

		AdminUser: getEnvOrDefault("ADMIN_USER", "admin"),
		AdminPass: getEnvOrDefault("ADMIN_PASS", ""),

		RetryQueueFilePath:      getEnvOrDefault("RETRY_QUEUE_FILE", "/var/log/webhook-bridge/retry-queue.json"),
		HealthCheckServiceName: getEnvOrDefault("HEALTH_CHECK_SERVICE_NAME", "IcingaAlertForge-Health"),
		HealthCheckTargetHost:  getEnvOrDefault("HEALTH_CHECK_TARGET_HOST", ""),

		AuditLogFile:   getEnvOrDefault("AUDIT_LOG_FILE", "/var/log/webhook-bridge/audit.log"),
		AuditLogFormat: getEnvOrDefault("AUDIT_LOG_FORMAT", "json"),

		ConfigEncryptionKey: getEnvOrDefault("CONFIG_ENCRYPTION_KEY", ""),
		ConfigFilePath:      getEnvOrDefault("CONFIG_FILE_PATH", "/var/log/webhook-bridge/config.json"),
	}

	if cfg.Icinga2Host, err = requireEnv("ICINGA2_HOST"); err != nil {
		return nil, err
	}
	if cfg.Icinga2User, err = requireEnv("ICINGA2_USER"); err != nil {
		return nil, err
	}
	if cfg.Icinga2Pass, err = requireEnv("ICINGA2_PASS"); err != nil {
		return nil, err
	}
	if cfg.Icinga2HostAutoCreate, err = optBool("ICINGA2_HOST_AUTO_CREATE", false); err != nil {
		return nil, err
	}
	if cfg.Icinga2TLSSkipVerify, err = optBool("ICINGA2_TLS_SKIP_VERIFY", false); err != nil {
		return nil, err
	}

	if cfg.HistoryMaxEntries, err = optInt("HISTORY_MAX_ENTRIES", 10000); err != nil {
		return nil, err
	}
	if cfg.CacheTTLMinutes, err = optInt("CACHE_TTL_MINUTES", 60); err != nil {
		return nil, err
	}

	if cfg.RateLimitMutate, err = optInt("RATELIMIT_MUTATE_MAX", 5); err != nil {
		return nil, err
	}
	if cfg.RateLimitStatus, err = optInt("RATELIMIT_STATUS_MAX", 20); err != nil {
		return nil, err
	}
	if cfg.RateLimitMaxQueue, err = optInt("RATELIMIT_MAX_QUEUE", 100); err != nil {
		return nil, err
	}

	if cfg.RetryQueueEnabled, err = optBool("RETRY_QUEUE_ENABLED", true); err != nil {
		return nil, err
	}
	if cfg.RetryQueueMaxSize, err = optInt("RETRY_QUEUE_MAX_SIZE", 1000); err != nil {
		return nil, err
	}
	if cfg.RetryQueueRetryBaseSec, err = optInt("RETRY_QUEUE_RETRY_BASE_SEC", 5); err != nil {
		return nil, err
	}
	if cfg.RetryQueueRetryMaxSec, err = optInt("RETRY_QUEUE_RETRY_MAX_SEC", 300); err != nil {
		return nil, err
	}
	if cfg.RetryQueueCheckInterval, err = optInt("RETRY_QUEUE_CHECK_INTERVAL_SEC", 10); err != nil {
		return nil, err
	}

	if cfg.HealthCheckEnabled, err = optBool("HEALTH_CHECK_ENABLED", true); err != nil {
		return nil, err
	}
	if cfg.HealthCheckIntervalSec, err = optInt("HEALTH_CHECK_INTERVAL_SEC", 60); err != nil {
		return nil, err
	}
	if cfg.HealthCheckRegister, err = optBool("HEALTH_CHECK_REGISTER", true); err != nil {
		return nil, err
	}

	if cfg.AuditLogEnabled, err = optBool("AUDIT_LOG_ENABLED", false); err != nil {
		return nil, err
	}
	if cfg.ConfigInDashboard, err = optBool("CONFIG_IN_DASHBOARD", false); err != nil {
		return nil, err
	}

	if len(cfg.WebhookRoutes) == 0 {
		return nil, fmt.Errorf("config: at least one webhook route is required")
	}

	return cfg, nil
}

// ListenAddr returns the full listen address (host:port).
func (c *Config) ListenAddr() string {
	return c.ServerHost + ":" + c.ServerPort
}

// DefaultTarget returns the primary target used for legacy single-host flows.
func (c *Config) DefaultTarget() TargetConfig {
	return c.Targets[c.DefaultTargetID]
}

type targetEnvSpec struct {
	ID                 string
	Source             string
	HostName           string
	HostDisplay        string
	HostAddress        string
	APIKeys            []string
	NotificationUsers  []string
	NotificationGroups []string
	ServiceStates      []string
	HostStates         []string
}

func loadTargetsAndRoutes() (map[string]TargetConfig, map[string]WebhookRoute, string, error) {
	targetSpecs := loadTargetSpecs()
	if len(targetSpecs) == 0 {
		return loadLegacyTargetAndRoutes()
	}

	ids := make([]string, 0, len(targetSpecs))
	targets := make(map[string]TargetConfig, len(targetSpecs))
	routes := make(map[string]WebhookRoute)

	for id, spec := range targetSpecs {
		if spec.HostName == "" {
			return nil, nil, "", fmt.Errorf("config: target %s missing required IAF_TARGET_%s_HOST_NAME", id, envTokenFromTargetID(id))
		}
		source := spec.Source
		if source == "" {
			source = id
		}
		targets[id] = TargetConfig{
			ID:          id,
			Source:      source,
			HostName:    spec.HostName,
			HostDisplay: firstNonEmpty(spec.HostDisplay, spec.HostName),
			HostAddress: spec.HostAddress,
			Notification: NotificationConfig{
				Users:         spec.NotificationUsers,
				Groups:        spec.NotificationGroups,
				ServiceStates: spec.ServiceStates,
				HostStates:    spec.HostStates,
			},
		}

		for _, key := range spec.APIKeys {
			if _, exists := routes[key]; exists {
				return nil, nil, "", fmt.Errorf("config: duplicate webhook API key configured for multiple targets: %s", key)
			}
			routes[key] = WebhookRoute{
				Source:   source,
				TargetID: id,
			}
		}

		ids = append(ids, id)
	}

	if len(routes) == 0 {
		return nil, nil, "", fmt.Errorf("config: at least one IAF_TARGET_*_API_KEYS value is required")
	}

	sort.Strings(ids)
	return targets, routes, ids[0], nil
}

func loadLegacyTargetAndRoutes() (map[string]TargetConfig, map[string]WebhookRoute, string, error) {
	hostName, err := requireEnv("ICINGA2_HOST_NAME")
	if err != nil {
		return nil, nil, "", err
	}
	targetID := "default"
	targets := map[string]TargetConfig{
		targetID: {
			ID:          targetID,
			Source:      targetID,
			HostName:    hostName,
			HostDisplay: firstNonEmpty(getEnvOrDefault("ICINGA2_HOST_DISPLAY", ""), hostName),
			HostAddress: getEnvOrDefault("ICINGA2_HOST_ADDRESS", ""),
		},
	}

	keys := loadLegacyWebhookKeys()
	if len(keys) == 0 {
		return nil, nil, "", fmt.Errorf("config: at least one WEBHOOK_KEY_* environment variable is required")
	}

	routes := make(map[string]WebhookRoute, len(keys))
	for key, source := range keys {
		routes[key] = WebhookRoute{
			Source:   source,
			TargetID: targetID,
		}
	}

	return targets, routes, targetID, nil
}

func loadTargetSpecs() map[string]*targetEnvSpec {
	specs := make(map[string]*targetEnvSpec)
	for _, env := range os.Environ() {
		parts := strings.SplitN(env, "=", 2)
		if len(parts) != 2 {
			continue
		}
		name, value := parts[0], parts[1]
		if value == "" || !strings.HasPrefix(name, "IAF_TARGET_") {
			continue
		}

		switch {
		case strings.HasSuffix(name, "_NOTIFICATION_SERVICE_STATES"):
			specs[getTargetSpec(specs, strings.TrimSuffix(strings.TrimPrefix(name, "IAF_TARGET_"), "_NOTIFICATION_SERVICE_STATES")).ID].ServiceStates = parseCSV(value)
		case strings.HasSuffix(name, "_NOTIFICATION_HOST_STATES"):
			specs[getTargetSpec(specs, strings.TrimSuffix(strings.TrimPrefix(name, "IAF_TARGET_"), "_NOTIFICATION_HOST_STATES")).ID].HostStates = parseCSV(value)
		case strings.HasSuffix(name, "_NOTIFICATION_USERS"):
			specs[getTargetSpec(specs, strings.TrimSuffix(strings.TrimPrefix(name, "IAF_TARGET_"), "_NOTIFICATION_USERS")).ID].NotificationUsers = parseCSV(value)
		case strings.HasSuffix(name, "_NOTIFICATION_GROUPS"):
			specs[getTargetSpec(specs, strings.TrimSuffix(strings.TrimPrefix(name, "IAF_TARGET_"), "_NOTIFICATION_GROUPS")).ID].NotificationGroups = parseCSV(value)
		case strings.HasSuffix(name, "_HOST_DISPLAY"):
			specs[getTargetSpec(specs, strings.TrimSuffix(strings.TrimPrefix(name, "IAF_TARGET_"), "_HOST_DISPLAY")).ID].HostDisplay = value
		case strings.HasSuffix(name, "_HOST_ADDRESS"):
			specs[getTargetSpec(specs, strings.TrimSuffix(strings.TrimPrefix(name, "IAF_TARGET_"), "_HOST_ADDRESS")).ID].HostAddress = value
		case strings.HasSuffix(name, "_HOST_NAME"):
			specs[getTargetSpec(specs, strings.TrimSuffix(strings.TrimPrefix(name, "IAF_TARGET_"), "_HOST_NAME")).ID].HostName = value
		case strings.HasSuffix(name, "_API_KEYS"):
			specs[getTargetSpec(specs, strings.TrimSuffix(strings.TrimPrefix(name, "IAF_TARGET_"), "_API_KEYS")).ID].APIKeys = parseCSV(value)
		case strings.HasSuffix(name, "_SOURCE"):
			specs[getTargetSpec(specs, strings.TrimSuffix(strings.TrimPrefix(name, "IAF_TARGET_"), "_SOURCE")).ID].Source = value
		}
	}
	return specs
}

// loadLegacyWebhookKeys scans environment variables for the WEBHOOK_KEY_ prefix
// and builds a map from key value to source name.
func loadLegacyWebhookKeys() map[string]string {
	keys := make(map[string]string)
	for _, env := range os.Environ() {
		parts := strings.SplitN(env, "=", 2)
		if len(parts) != 2 {
			continue
		}
		name, value := parts[0], parts[1]
		if strings.HasPrefix(name, "WEBHOOK_KEY_") && value != "" {
			source := strings.TrimPrefix(name, "WEBHOOK_KEY_")
			source = strings.ToLower(strings.ReplaceAll(source, "_", "-"))
			keys[value] = source
		}
	}
	return keys
}

func getTargetSpec(specs map[string]*targetEnvSpec, rawID string) *targetEnvSpec {
	id := normalizeTargetID(rawID)
	spec, ok := specs[id]
	if ok {
		return spec
	}

	spec = &targetEnvSpec{ID: id}
	specs[id] = spec
	return spec
}

func normalizeTargetID(raw string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(raw), "_", "-"))
}

func envTokenFromTargetID(id string) string {
	return strings.ToUpper(strings.ReplaceAll(id, "-", "_"))
}

func parseCSV(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			values = append(values, part)
		}
	}
	if len(values) == 0 {
		return nil
	}
	return values
}

func getLegacyHostName(targets map[string]TargetConfig, defaultTargetID string) string {
	if target, ok := targets[defaultTargetID]; ok {
		return target.HostName
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func requireEnv(key string) (string, error) {
	val := os.Getenv(key)
	if val == "" {
		return "", fmt.Errorf("config: required environment variable %s is not set", key)
	}
	return val, nil
}

func getEnvOrDefault(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

func optBool(key string, def bool) (bool, error) {
	val := os.Getenv(key)
	if val == "" {
		return def, nil
	}
	b, err := strconv.ParseBool(val)
	if err != nil {
		return false, fmt.Errorf("config: invalid boolean for %s=%q (use true/false/1/0)", key, val)
	}
	return b, nil
}

func optInt(key string, def int) (int, error) {
	val := os.Getenv(key)
	if val == "" {
		return def, nil
	}
	n, err := strconv.Atoi(val)
	if err != nil {
		return 0, fmt.Errorf("config: invalid integer for %s=%q", key, val)
	}
	if n < 0 {
		return 0, fmt.Errorf("config: negative value for %s=%d", key, n)
	}
	return n, nil
}

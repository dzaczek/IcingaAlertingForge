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
}

// Load reads the .env file and populates the Config struct.
// It panics if any required variable is missing.
func Load() *Config {
	// Load .env file if it exists (ignore error — env vars may come from the system)
	_ = godotenv.Load()

	targets, routes, defaultTargetID := loadTargetsAndRoutes()
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

		Icinga2Host:           requireEnv("ICINGA2_HOST"),
		Icinga2User:           requireEnv("ICINGA2_USER"),
		Icinga2Pass:           requireEnv("ICINGA2_PASS"),
		Icinga2HostName:       getLegacyHostName(targets, defaultTargetID),
		Icinga2HostDisplay:    getEnvOrDefault("ICINGA2_HOST_DISPLAY", ""),
		Icinga2HostAddress:    getEnvOrDefault("ICINGA2_HOST_ADDRESS", ""),
		Icinga2HostAutoCreate: getEnvBool("ICINGA2_HOST_AUTO_CREATE", false),
		Icinga2TLSSkipVerify:  getEnvBool("ICINGA2_TLS_SKIP_VERIFY", false),

		HistoryFile:       getEnvOrDefault("HISTORY_FILE", "/var/log/webhook-bridge/history.jsonl"),
		HistoryMaxEntries: getEnvInt("HISTORY_MAX_ENTRIES", 10000),

		CacheTTLMinutes: getEnvInt("CACHE_TTL_MINUTES", 60),

		LogLevel:  getEnvOrDefault("LOG_LEVEL", "info"),
		LogFormat: getEnvOrDefault("LOG_FORMAT", "json"),

		AdminUser: getEnvOrDefault("ADMIN_USER", "admin"),
		AdminPass: getEnvOrDefault("ADMIN_PASS", ""),

		RateLimitMutate:   getEnvInt("RATELIMIT_MUTATE_MAX", 5),
		RateLimitStatus:   getEnvInt("RATELIMIT_STATUS_MAX", 20),
		RateLimitMaxQueue: getEnvInt("RATELIMIT_MAX_QUEUE", 100),
	}

	if len(cfg.WebhookRoutes) == 0 {
		panic("config: at least one webhook route is required")
	}

	return cfg
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

func loadTargetsAndRoutes() (map[string]TargetConfig, map[string]WebhookRoute, string) {
	targetSpecs := loadTargetSpecs()
	if len(targetSpecs) == 0 {
		return loadLegacyTargetAndRoutes()
	}

	ids := make([]string, 0, len(targetSpecs))
	targets := make(map[string]TargetConfig, len(targetSpecs))
	routes := make(map[string]WebhookRoute)

	for id, spec := range targetSpecs {
		if spec.HostName == "" {
			panic(fmt.Sprintf("config: target %s missing required IAF_TARGET_%s_HOST_NAME", id, envTokenFromTargetID(id)))
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
				panic(fmt.Sprintf("config: duplicate webhook API key configured for multiple targets: %s", key))
			}
			routes[key] = WebhookRoute{
				Source:   source,
				TargetID: id,
			}
		}

		ids = append(ids, id)
	}

	if len(routes) == 0 {
		panic("config: at least one IAF_TARGET_*_API_KEYS value is required")
	}

	sort.Strings(ids)
	return targets, routes, ids[0]
}

func loadLegacyTargetAndRoutes() (map[string]TargetConfig, map[string]WebhookRoute, string) {
	hostName := requireEnv("ICINGA2_HOST_NAME")
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
		panic("config: at least one WEBHOOK_KEY_* environment variable is required")
	}

	routes := make(map[string]WebhookRoute, len(keys))
	for key, source := range keys {
		routes[key] = WebhookRoute{
			Source:   source,
			TargetID: targetID,
		}
	}

	return targets, routes, targetID
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
		panic(fmt.Sprintf("config: invalid boolean for %s=%q (use true/false/1/0)", key, val))
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
		panic(fmt.Sprintf("config: invalid integer for %s=%q", key, val))
	}
	if n < 0 {
		panic(fmt.Sprintf("config: negative value for %s=%d", key, n))
	}
	return n
}

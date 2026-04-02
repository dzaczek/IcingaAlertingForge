// Package configstore provides persistent JSON-based configuration storage
// with AES-256-GCM encryption for secrets. It enables dashboard-based config
// management as an alternative to environment variables.
package configstore

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"icinga-webhook-bridge/config"
)

// TargetStore holds a single target's dashboard-editable configuration.
type TargetStore struct {
	ID                    string   `json:"id"`
	Source                string   `json:"source"`
	HostName              string   `json:"host_name"`
	HostDisplay           string   `json:"host_display"`
	HostAddress           string   `json:"host_address"`
	APIKeys               []string `json:"api_keys"`
	NotificationUsers     []string `json:"notification_users,omitempty"`
	NotificationGroups    []string `json:"notification_groups,omitempty"`
	NotificationSvcStates []string `json:"notification_service_states,omitempty"`
	NotificationHstStates []string `json:"notification_host_states,omitempty"`
}

// StoredUser represents a persisted RBAC user with encrypted password.
type StoredUser struct {
	Username string `json:"username"`
	Password string `json:"password"` // encrypted at rest
	Role     string `json:"role"`
}

// StoredConfig is the JSON-serializable configuration document.
type StoredConfig struct {
	Version   int       `json:"version"`
	UpdatedAt time.Time `json:"updated_at"`
	CreatedAt time.Time `json:"created_at"`

	// Icinga2
	Icinga2Host           string `json:"icinga2_host"`
	Icinga2User           string `json:"icinga2_user"`
	Icinga2Pass           string `json:"icinga2_pass"` // encrypted at rest
	Icinga2HostAutoCreate bool   `json:"icinga2_host_auto_create"`
	Icinga2TLSSkipVerify  bool   `json:"icinga2_tls_skip_verify"`

	// Targets
	Targets []TargetStore `json:"targets"`

	// History
	HistoryFile       string `json:"history_file"`
	HistoryMaxEntries int    `json:"history_max_entries"`

	// Cache
	CacheTTLMinutes int `json:"cache_ttl_minutes"`

	// Logging
	LogLevel  string `json:"log_level"`
	LogFormat string `json:"log_format"`

	// Rate limiting
	RateLimitMutate   int `json:"ratelimit_mutate_max"`
	RateLimitStatus   int `json:"ratelimit_status_max"`
	RateLimitMaxQueue int `json:"ratelimit_max_queue"`

	// RBAC users (passwords encrypted at rest)
	Users []StoredUser `json:"users,omitempty"`
}

// Store provides thread-safe persistent configuration storage.
type Store struct {
	mu       sync.RWMutex
	filePath string
	keyPath  string
	encKey   []byte
	current  *StoredConfig
}

// New creates a new Store. If encryptionKey is empty, a key is auto-generated.
func New(configPath, encryptionKey string) (*Store, error) {
	dir := filepath.Dir(configPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("configstore: create dir: %w", err)
	}

	s := &Store{
		filePath: configPath,
		keyPath:  filepath.Join(dir, ".config.key"),
	}

	if encryptionKey != "" {
		hash := sha256.Sum256([]byte(encryptionKey))
		s.encKey = hash[:]
	} else {
		key, err := s.loadOrCreateKey()
		if err != nil {
			return nil, err
		}
		s.encKey = key
	}

	return s, nil
}

// Exists returns true if a stored config file already exists.
func (s *Store) Exists() bool {
	_, err := os.Stat(s.filePath)
	return err == nil
}

// Load reads the config from disk.
func (s *Store) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.filePath)
	if err != nil {
		return fmt.Errorf("configstore: read: %w", err)
	}

	var sc StoredConfig
	if err := json.Unmarshal(data, &sc); err != nil {
		return fmt.Errorf("configstore: parse: %w", err)
	}

	// Decrypt secrets
	if sc.Icinga2Pass != "" {
		sc.Icinga2Pass, err = s.decrypt(sc.Icinga2Pass)
		if err != nil {
			return fmt.Errorf("configstore: decrypt icinga2_pass: %w", err)
		}
	}
	for i := range sc.Targets {
		for j := range sc.Targets[i].APIKeys {
			sc.Targets[i].APIKeys[j], err = s.decrypt(sc.Targets[i].APIKeys[j])
			if err != nil {
				return fmt.Errorf("configstore: decrypt api_key for target %s: %w", sc.Targets[i].ID, err)
			}
		}
	}

	for i := range sc.Users {
		if sc.Users[i].Password != "" {
			sc.Users[i].Password, err = s.decrypt(sc.Users[i].Password)
			if err != nil {
				return fmt.Errorf("configstore: decrypt user %s password: %w", sc.Users[i].Username, err)
			}
		}
	}

	s.current = &sc
	return nil
}

// Save writes the current config to disk with secrets encrypted.
func (s *Store) Save() error {
	s.mu.RLock()
	sc := *s.current
	// Deep-copy slices so encryption does not mutate in-memory data.
	sc.Targets = make([]TargetStore, len(s.current.Targets))
	for i, t := range s.current.Targets {
		sc.Targets[i] = t
		sc.Targets[i].APIKeys = make([]string, len(t.APIKeys))
		copy(sc.Targets[i].APIKeys, t.APIKeys)
	}
	sc.Users = make([]StoredUser, len(s.current.Users))
	copy(sc.Users, s.current.Users)
	s.mu.RUnlock()

	sc.UpdatedAt = time.Now().UTC()

	// Encrypt secrets for storage
	var err error
	if sc.Icinga2Pass != "" {
		sc.Icinga2Pass, err = s.encrypt(sc.Icinga2Pass)
		if err != nil {
			return fmt.Errorf("configstore: encrypt icinga2_pass: %w", err)
		}
	}
	for i := range sc.Targets {
		for j := range sc.Targets[i].APIKeys {
			sc.Targets[i].APIKeys[j], err = s.encrypt(sc.Targets[i].APIKeys[j])
			if err != nil {
				return fmt.Errorf("configstore: encrypt api_key: %w", err)
			}
		}
	}
	for i := range sc.Users {
		if sc.Users[i].Password != "" {
			sc.Users[i].Password, err = s.encrypt(sc.Users[i].Password)
			if err != nil {
				return fmt.Errorf("configstore: encrypt user %s password: %w", sc.Users[i].Username, err)
			}
		}
	}

	data, err := json.MarshalIndent(sc, "", "  ")
	if err != nil {
		return fmt.Errorf("configstore: marshal: %w", err)
	}

	// Atomic write: write to temp file, then rename
	tmp := s.filePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("configstore: write tmp: %w", err)
	}
	if err := os.Rename(tmp, s.filePath); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("configstore: rename: %w", err)
	}

	return nil
}

// Get returns a copy of the current stored config.
func (s *Store) Get() StoredConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.current == nil {
		return StoredConfig{}
	}
	cp := *s.current
	cp.Targets = make([]TargetStore, len(s.current.Targets))
	for i, t := range s.current.Targets {
		cp.Targets[i] = t
		cp.Targets[i].APIKeys = make([]string, len(t.APIKeys))
		copy(cp.Targets[i].APIKeys, t.APIKeys)
		cp.Targets[i].NotificationUsers = append([]string(nil), t.NotificationUsers...)
		cp.Targets[i].NotificationGroups = append([]string(nil), t.NotificationGroups...)
		cp.Targets[i].NotificationSvcStates = append([]string(nil), t.NotificationSvcStates...)
		cp.Targets[i].NotificationHstStates = append([]string(nil), t.NotificationHstStates...)
	}
	cp.Users = make([]StoredUser, len(s.current.Users))
	copy(cp.Users, s.current.Users)
	return cp
}

// SetUsers replaces the stored RBAC users list and saves to disk.
func (s *Store) SetUsers(users []StoredUser) error {
	s.mu.Lock()
	if s.current == nil {
		s.current = &StoredConfig{Version: 1, CreatedAt: time.Now().UTC()}
	}
	s.current.Users = users
	s.mu.Unlock()
	return s.Save()
}

// GetUsers returns a copy of the stored RBAC users.
func (s *Store) GetUsers() []StoredUser {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.current == nil {
		return nil
	}
	users := make([]StoredUser, len(s.current.Users))
	copy(users, s.current.Users)
	return users
}

// Update replaces the stored config and saves to disk.
func (s *Store) Update(sc StoredConfig) error {
	s.mu.Lock()
	sc.UpdatedAt = time.Now().UTC()
	if s.current != nil {
		sc.CreatedAt = s.current.CreatedAt
	}
	sc.Version = 1
	s.current = &sc
	s.mu.Unlock()
	return s.Save()
}

// MigrateFromEnv creates the initial stored config from a loaded env config.
func (s *Store) MigrateFromEnv(cfg *config.Config) error {
	now := time.Now().UTC()
	sc := StoredConfig{
		Version:   1,
		CreatedAt: now,
		UpdatedAt: now,

		Icinga2Host:           cfg.Icinga2Host,
		Icinga2User:           cfg.Icinga2User,
		Icinga2Pass:           cfg.Icinga2Pass,
		Icinga2HostAutoCreate: cfg.Icinga2HostAutoCreate,
		Icinga2TLSSkipVerify:  cfg.Icinga2TLSSkipVerify,

		HistoryFile:       cfg.HistoryFile,
		HistoryMaxEntries: cfg.HistoryMaxEntries,

		CacheTTLMinutes: cfg.CacheTTLMinutes,

		LogLevel:  cfg.LogLevel,
		LogFormat: cfg.LogFormat,

		RateLimitMutate:   cfg.RateLimitMutate,
		RateLimitStatus:   cfg.RateLimitStatus,
		RateLimitMaxQueue: cfg.RateLimitMaxQueue,
	}

	// Convert targets with their API keys
	for id, t := range cfg.Targets {
		ts := TargetStore{
			ID:                    id,
			Source:                t.Source,
			HostName:              t.HostName,
			HostDisplay:           t.HostDisplay,
			HostAddress:           t.HostAddress,
			NotificationUsers:     t.Notification.Users,
			NotificationGroups:    t.Notification.Groups,
			NotificationSvcStates: t.Notification.ServiceStates,
			NotificationHstStates: t.Notification.HostStates,
		}
		// Find API keys that map to this target
		for key, route := range cfg.WebhookRoutes {
			if route.TargetID == id {
				ts.APIKeys = append(ts.APIKeys, key)
			}
		}
		sort.Strings(ts.APIKeys)
		sc.Targets = append(sc.Targets, ts)
	}
	sort.Slice(sc.Targets, func(i, j int) bool { return sc.Targets[i].ID < sc.Targets[j].ID })

	s.mu.Lock()
	s.current = &sc
	s.mu.Unlock()

	if err := s.Save(); err != nil {
		return err
	}

	slog.Info("Migrated configuration from environment variables to dashboard store", "path", s.filePath)
	return nil
}

// ToConfig converts the stored config back to a config.Config for runtime use.
func (s *Store) ToConfig(serverPort, serverHost string) *config.Config {
	s.mu.RLock()
	sc := s.current
	s.mu.RUnlock()

	if sc == nil {
		return nil
	}

	targets := make(map[string]config.TargetConfig, len(sc.Targets))
	routes := make(map[string]config.WebhookRoute)
	webhookKeys := make(map[string]string)

	for _, t := range sc.Targets {
		targets[t.ID] = config.TargetConfig{
			ID:          t.ID,
			Source:      t.Source,
			HostName:    t.HostName,
			HostDisplay: firstNonEmpty(t.HostDisplay, t.HostName),
			HostAddress: t.HostAddress,
			Notification: config.NotificationConfig{
				Users:         t.NotificationUsers,
				Groups:        t.NotificationGroups,
				ServiceStates: t.NotificationSvcStates,
				HostStates:    t.NotificationHstStates,
			},
		}
		for _, key := range t.APIKeys {
			routes[key] = config.WebhookRoute{
				Source:   t.Source,
				TargetID: t.ID,
			}
			webhookKeys[key] = t.Source
		}
	}

	var defaultID string
	ids := make([]string, 0, len(targets))
	for id := range targets {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if len(ids) > 0 {
		defaultID = ids[0]
	}

	return &config.Config{
		ServerPort:            serverPort,
		ServerHost:            serverHost,
		WebhookKeys:           webhookKeys,
		WebhookRoutes:         routes,
		Targets:               targets,
		DefaultTargetID:       defaultID,
		Icinga2Host:           sc.Icinga2Host,
		Icinga2User:           sc.Icinga2User,
		Icinga2Pass:           sc.Icinga2Pass,
		Icinga2HostName:       getLegacyHostName(targets, defaultID),
		Icinga2HostAutoCreate: sc.Icinga2HostAutoCreate,
		Icinga2TLSSkipVerify:  sc.Icinga2TLSSkipVerify,
		HistoryFile:           sc.HistoryFile,
		HistoryMaxEntries:     sc.HistoryMaxEntries,
		CacheTTLMinutes:       sc.CacheTTLMinutes,
		LogLevel:              sc.LogLevel,
		LogFormat:             sc.LogFormat,
		RateLimitMutate:       sc.RateLimitMutate,
		RateLimitStatus:       sc.RateLimitStatus,
		RateLimitMaxQueue:     sc.RateLimitMaxQueue,
	}
}

// Export returns a JSON export suitable for backup.
func (s *Store) Export() ([]byte, error) {
	sc := s.Get()
	export := struct {
		Meta struct {
			Version    int       `json:"version"`
			ExportedAt time.Time `json:"exported_at"`
		} `json:"meta"`
		Config StoredConfig `json:"config"`
	}{}
	export.Meta.Version = 1
	export.Meta.ExportedAt = time.Now().UTC()
	export.Config = sc
	return json.MarshalIndent(export, "", "  ")
}

// ── Encryption helpers ──────────────────────────────────────────────

func (s *Store) encrypt(plaintext string) (string, error) {
	block, err := aes.NewCipher(s.encKey)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return "enc:" + hex.EncodeToString(ciphertext), nil
}

func (s *Store) decrypt(encoded string) (string, error) {
	if !strings.HasPrefix(encoded, "enc:") {
		return encoded, nil // not encrypted, return as-is (migration)
	}
	data, err := hex.DecodeString(strings.TrimPrefix(encoded, "enc:"))
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(s.encKey)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(data) < gcm.NonceSize() {
		return "", fmt.Errorf("ciphertext too short")
	}
	nonce, ciphertext := data[:gcm.NonceSize()], data[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

func (s *Store) loadOrCreateKey() ([]byte, error) {
	data, err := os.ReadFile(s.keyPath)
	if err == nil && len(data) == 64 {
		key, err := hex.DecodeString(string(data))
		if err == nil && len(key) == 32 {
			return key, nil
		}
	}

	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("configstore: generate key: %w", err)
	}

	if err := os.WriteFile(s.keyPath, []byte(hex.EncodeToString(key)), 0600); err != nil {
		return nil, fmt.Errorf("configstore: save key: %w", err)
	}

	slog.Info("Generated new encryption key for config store", "path", s.keyPath)
	return key, nil
}

func getLegacyHostName(targets map[string]config.TargetConfig, defaultID string) string {
	if t, ok := targets[defaultID]; ok {
		return t.HostName
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

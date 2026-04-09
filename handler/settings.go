package handler

import (
	"icinga-webhook-bridge/httputil"
	"crypto/rand"
	"crypto/subtle"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"icinga-webhook-bridge/config"
	"icinga-webhook-bridge/configstore"
	"icinga-webhook-bridge/metrics"
	"icinga-webhook-bridge/rbac"

	"github.com/google/uuid"
)

// SettingsHandler serves REST API endpoints for managing configuration
// through the dashboard.
type SettingsHandler struct {
	Store    *configstore.Store
	User     string
	Pass     string
	Metrics  *metrics.Collector
	RBAC     *rbac.Manager
	OnReload func(*config.Config) // called after config save to hot-reload components
}

// checkAuth validates HTTP Basic Auth credentials and manage.config permission.
func (h *SettingsHandler) checkAuth(w http.ResponseWriter, r *http.Request) bool {
	if h.Pass == "" {
		httputil.WriteJSON(w, http.StatusForbidden, map[string]string{"error": "admin access not configured (ADMIN_PASS not set)"})
		return false
	}
	user, pass, ok := r.BasicAuth()
	if !ok {
		w.Header().Set("WWW-Authenticate", `Basic realm="Admin"`)
		httputil.WriteJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return false
	}

	// Check primary admin credentials
	userOK := subtle.ConstantTimeCompare([]byte(user), []byte(h.User)) == 1
	passOK := subtle.ConstantTimeCompare([]byte(pass), []byte(h.Pass)) == 1
	if userOK && passOK {
		return true
	}

	// Check RBAC users — must have manage.config permission
	if h.RBAC != nil {
		if _, authenticated := h.RBAC.Authenticate(user, pass); authenticated {
			if h.RBAC.HasPermission(user, rbac.PermManageConfig) {
				return true
			}
			httputil.WriteJSON(w, http.StatusForbidden, map[string]string{"error": "insufficient permissions"})
			return false
		}
	}

	if h.Metrics != nil {
		h.Metrics.RecordAuthFailure(r.RemoteAddr, user)
	}
	slog.Warn("Settings auth failed", "remote_addr", r.RemoteAddr, "user", user)
	w.Header().Set("WWW-Authenticate", `Basic realm="Admin"`)
	httputil.WriteJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
	return false
}

// maskConfig returns a copy of the stored config with all secrets replaced by "***".
func maskConfig(sc configstore.StoredConfig) configstore.StoredConfig {
	if sc.Icinga2Pass != "" {
		sc.Icinga2Pass = "***"
	}
	for i := range sc.Targets {
		for j := range sc.Targets[i].APIKeys {
			sc.Targets[i].APIKeys[j] = "***"
		}
	}
	return sc
}

// HandleGetSettings returns the full config with secrets masked.
// GET /admin/settings
func (h *SettingsHandler) HandleGetSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httputil.WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if !h.checkAuth(w, r) {
		return
	}

	sc := h.Store.Get()
	httputil.WriteJSON(w, http.StatusOK, maskConfig(sc))
}

// HandlePatchSettings partially updates the stored config.
// PATCH /admin/settings
func (h *SettingsHandler) HandlePatchSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		httputil.WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if !h.checkAuth(w, r) {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	var patch configstore.StoredConfig
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}

	current := h.Store.Get()

	// Apply non-zero fields from patch to current config.
	if patch.Icinga2Host != "" {
		current.Icinga2Host = patch.Icinga2Host
	}
	if patch.Icinga2User != "" {
		current.Icinga2User = patch.Icinga2User
	}
	if patch.Icinga2Pass != "" && patch.Icinga2Pass != "***" {
		current.Icinga2Pass = patch.Icinga2Pass
	}
	// Boolean fields are applied directly from the patch.
	current.Icinga2HostAutoCreate = patch.Icinga2HostAutoCreate
	current.Icinga2TLSSkipVerify = patch.Icinga2TLSSkipVerify
	current.Icinga2Force = patch.Icinga2Force

	if patch.Icinga2ConflictPolicy != "" {
		current.Icinga2ConflictPolicy = patch.Icinga2ConflictPolicy
	}

	if patch.HistoryFile != "" {
		current.HistoryFile = patch.HistoryFile
	}
	if patch.HistoryMaxEntries > 0 {
		current.HistoryMaxEntries = patch.HistoryMaxEntries
	}
	if patch.CacheTTLMinutes > 0 {
		current.CacheTTLMinutes = patch.CacheTTLMinutes
	}
	if patch.LogLevel != "" {
		current.LogLevel = patch.LogLevel
	}
	if patch.LogFormat != "" {
		current.LogFormat = patch.LogFormat
	}
	if patch.RateLimitMutate > 0 {
		current.RateLimitMutate = patch.RateLimitMutate
	}
	if patch.RateLimitStatus > 0 {
		current.RateLimitStatus = patch.RateLimitStatus
	}
	if patch.RateLimitMaxQueue > 0 {
		current.RateLimitMaxQueue = patch.RateLimitMaxQueue
	}

	if err := h.Store.Update(current); err != nil {
		slog.Error("Settings: failed to save config", "error", err)
		httputil.WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save: " + err.Error()})
		return
	}

	slog.Info("Settings: config updated", "remote_addr", r.RemoteAddr)
	h.reload()

	httputil.WriteJSON(w, http.StatusOK, maskConfig(h.Store.Get()))
}

// HandleAddTarget adds a new target to the configuration.
// POST /admin/settings/targets
func (h *SettingsHandler) HandleAddTarget(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httputil.WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if !h.checkAuth(w, r) {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	var target configstore.TargetStore
	if err := json.NewDecoder(r.Body).Decode(&target); err != nil {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}

	// Auto-generate ID if not provided.
	if target.ID == "" {
		target.ID = uuid.New().String()
	}

	// Validate required fields.
	if target.HostName == "" {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "host_name is required"})
		return
	}

	// Reject HTML/script content in input fields.
	for _, s := range []string{target.ID, target.HostName, target.HostDisplay, target.Source} {
		if strings.ContainsAny(s, "<>\"'&") {
			httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "input contains invalid characters"})
			return
		}
	}

	// Auto-generate an API key if none provided.
	if len(target.APIKeys) == 0 {
		key, err := generateAPIKey()
		if err != nil {
			slog.Error("Settings: failed to generate API key", "error", err)
			httputil.WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to generate API key"})
			return
		}
		target.APIKeys = []string{key}
	}

	current := h.Store.Get()

	// Check for duplicate target ID.
	for _, t := range current.Targets {
		if t.ID == target.ID {
			httputil.WriteJSON(w, http.StatusConflict, map[string]string{"error": "target with this ID already exists"})
			return
		}
	}

	current.Targets = append(current.Targets, target)

	if err := h.Store.Update(current); err != nil {
		slog.Error("Settings: failed to save config after adding target", "error", err)
		httputil.WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save: " + err.Error()})
		return
	}

	slog.Info("Settings: target added", "target_id", target.ID, "host_name", target.HostName)
	h.reload()

	// Return new target with the first auto-generated key visible (only time it is shown).
	firstKey := ""
	if len(target.APIKeys) > 0 {
		firstKey = target.APIKeys[0]
	}
	resp := map[string]interface{}{
		"id":        target.ID,
		"host_name": target.HostName,
		"source":    target.Source,
		"api_key":   firstKey,
	}
	httputil.WriteJSON(w, http.StatusCreated, resp)
}

// HandleDeleteTarget removes a target from the configuration.
// DELETE /admin/settings/targets/{id}
func (h *SettingsHandler) HandleDeleteTarget(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		httputil.WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if !h.checkAuth(w, r) {
		return
	}

	targetID := strings.TrimPrefix(r.URL.Path, "/admin/settings/targets/")
	if targetID == "" {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "target ID required"})
		return
	}

	current := h.Store.Get()
	found := false
	targets := make([]configstore.TargetStore, 0, len(current.Targets))
	for _, t := range current.Targets {
		if t.ID == targetID {
			found = true
			continue
		}
		targets = append(targets, t)
	}

	if !found {
		httputil.WriteJSON(w, http.StatusNotFound, map[string]string{"error": "target not found"})
		return
	}

	current.Targets = targets

	if err := h.Store.Update(current); err != nil {
		slog.Error("Settings: failed to save config after deleting target", "error", err)
		httputil.WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save: " + err.Error()})
		return
	}

	slog.Info("Settings: target deleted", "target_id", targetID)
	h.reload()

	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted", "target_id": targetID})
}

// HandleGenerateKey generates a new API key for a target.
// POST /admin/settings/targets/{id}/generate-key
func (h *SettingsHandler) HandleGenerateKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httputil.WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if !h.checkAuth(w, r) {
		return
	}

	// Extract target ID: strip prefix and suffix from the path.
	path := strings.TrimPrefix(r.URL.Path, "/admin/settings/targets/")
	targetID := strings.TrimSuffix(path, "/generate-key")
	if targetID == "" {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "target ID required"})
		return
	}

	current := h.Store.Get()
	found := false
	var newKey string
	for i := range current.Targets {
		if current.Targets[i].ID == targetID {
			found = true
			key, err := generateAPIKey()
			if err != nil {
				slog.Error("Settings: failed to generate API key", "error", err)
				httputil.WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to generate API key"})
				return
			}
			newKey = key
			current.Targets[i].APIKeys = append(current.Targets[i].APIKeys, newKey)
			break
		}
	}

	if !found {
		httputil.WriteJSON(w, http.StatusNotFound, map[string]string{"error": "target not found"})
		return
	}

	if err := h.Store.Update(current); err != nil {
		slog.Error("Settings: failed to save config after generating key", "error", err)
		httputil.WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save: " + err.Error()})
		return
	}

	slog.Info("Settings: API key generated", "target_id", targetID)
	h.reload()

	// Return the full new key in cleartext (only time it is shown).
	httputil.WriteJSON(w, http.StatusOK, map[string]string{
		"target_id": targetID,
		"api_key":   newKey,
	})
}

// HandleRevealKeys returns the unmasked API keys for a specific target.
// GET /admin/settings/targets/{id}/reveal-keys
func (h *SettingsHandler) HandleRevealKeys(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httputil.WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if !h.checkAuth(w, r) {
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/admin/settings/targets/")
	targetID := strings.TrimSuffix(path, "/reveal-keys")
	if targetID == "" {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "target ID required"})
		return
	}

	current := h.Store.Get()
	for _, t := range current.Targets {
		if t.ID == targetID {
			httputil.WriteJSON(w, http.StatusOK, map[string]interface{}{
				"target_id": targetID,
				"api_keys":  t.APIKeys,
			})
			return
		}
	}

	httputil.WriteJSON(w, http.StatusNotFound, map[string]string{"error": "target not found"})
}

// HandleTestIcinga tests the Icinga2 connection using stored credentials.
// POST /admin/settings/test-icinga
func (h *SettingsHandler) HandleTestIcinga(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httputil.WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if !h.checkAuth(w, r) {
		return
	}

	sc := h.Store.Get()
	if sc.Icinga2Host == "" {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"status": "error", "error": "icinga2_host not configured"})
		return
	}

	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: sc.Icinga2TLSSkipVerify,
			},
		},
	}

	url := strings.TrimRight(sc.Icinga2Host, "/") + "/v1/status"
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, url, nil)
	if err != nil {
		httputil.WriteJSON(w, http.StatusInternalServerError, map[string]string{"status": "error", "error": "failed to create request: " + err.Error()})
		return
	}
	req.SetBasicAuth(sc.Icinga2User, sc.Icinga2Pass)
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "error", "error": "connection failed: " + err.Error()})
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode != http.StatusOK {
		httputil.WriteJSON(w, http.StatusOK, map[string]string{
			"status": "error",
			"error":  fmt.Sprintf("icinga2 returned HTTP %d: %s", resp.StatusCode, string(body)),
		})
		return
	}

	// Try to extract the Icinga2 version from the response.
	// Icinga2 /v1/status returns: results[].status.icingaapplication.app.version
	var icingaResp struct {
		Results []struct {
			Name   string `json:"name"`
			Status struct {
				IcingaApplication struct {
					App struct {
						Version string `json:"version"`
					} `json:"app"`
				} `json:"icingaapplication"`
			} `json:"status"`
		} `json:"results"`
	}
	version := "unknown"
	if err := json.Unmarshal(body, &icingaResp); err == nil {
		for _, r := range icingaResp.Results {
			if v := r.Status.IcingaApplication.App.Version; v != "" {
				version = v
				break
			}
		}
	}

	httputil.WriteJSON(w, http.StatusOK, map[string]string{
		"status":          "ok",
		"icinga2_version": version,
	})
}

// HandleExportConfig exports the configuration as a downloadable JSON backup.
// GET /admin/settings/export
func (h *SettingsHandler) HandleExportConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httputil.WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if !h.checkAuth(w, r) {
		return
	}

	data, err := h.Store.Export()
	if err != nil {
		slog.Error("Settings: failed to export config", "error", err)
		httputil.WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to export: " + err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="icinga-alertforge-config-%s.json"`, time.Now().UTC().Format("2006-01-02")))
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

// HandleImportConfig restores configuration from a previously exported backup.
func (h *SettingsHandler) HandleImportConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httputil.WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if !h.checkAuth(w, r) {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1MB limit

	var importData struct {
		Meta struct {
			Version int `json:"version"`
		} `json:"meta"`
		Config configstore.StoredConfig `json:"config"`
	}

	if err := json.NewDecoder(r.Body).Decode(&importData); err != nil {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}

	if importData.Meta.Version == 0 {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "missing or invalid meta.version in import file"})
		return
	}

	// Validate imported config has at least one target with a host name
	if len(importData.Config.Targets) == 0 {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "import must contain at least one target"})
		return
	}
	for _, t := range importData.Config.Targets {
		if t.HostName == "" {
			httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "target " + t.ID + " missing host_name"})
			return
		}
	}

	// Preserve secrets that are masked in the export
	current := h.Store.Get()
	if importData.Config.Icinga2Pass == "***" || importData.Config.Icinga2Pass == "" {
		importData.Config.Icinga2Pass = current.Icinga2Pass
	}
	for i := range importData.Config.Targets {
		for j := range importData.Config.Targets[i].APIKeys {
			if importData.Config.Targets[i].APIKeys[j] == "***" {
				// Try to preserve key from matching current target
				for _, ct := range current.Targets {
					if ct.ID == importData.Config.Targets[i].ID && j < len(ct.APIKeys) {
						importData.Config.Targets[i].APIKeys[j] = ct.APIKeys[j]
					}
				}
			}
		}
	}

	if err := h.Store.Update(importData.Config); err != nil {
		slog.Error("Settings: import failed", "error", err)
		httputil.WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save: " + err.Error()})
		return
	}

	slog.Info("Configuration imported from backup", "targets", len(importData.Config.Targets))
	h.reload()

	httputil.WriteJSON(w, http.StatusOK, map[string]string{
		"status":  "imported",
		"targets": fmt.Sprintf("%d", len(importData.Config.Targets)),
	})
}

// reload calls OnReload with the current config if the callback is set.
func (h *SettingsHandler) reload() {
	if h.OnReload == nil {
		return
	}
	cfg := h.Store.ToConfig("", "")
	if cfg != nil {
		h.OnReload(cfg)
	}
}

// generateAPIKey returns a cryptographically random 32-character hex string.
func generateAPIKey() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate api key: %w", err)
	}
	return hex.EncodeToString(b), nil
}

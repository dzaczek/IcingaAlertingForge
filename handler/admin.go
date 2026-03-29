package handler

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"icinga-webhook-bridge/cache"
	"icinga-webhook-bridge/config"
	"icinga-webhook-bridge/history"
	"icinga-webhook-bridge/icinga"
	"icinga-webhook-bridge/metrics"
	"icinga-webhook-bridge/models"
)

// AdminHandler serves admin API endpoints for service management.
type AdminHandler struct {
	Cache     *cache.ServiceCache
	API       *icinga.APIClient
	Limiter   *icinga.RateLimiter
	History   *history.Logger
	Metrics   *metrics.Collector
	DebugRing *icinga.DebugRing
	Targets   map[string]config.TargetConfig
	User      string
	Pass      string
}

// checkAuth validates HTTP Basic Auth credentials for admin endpoints.
func (h *AdminHandler) checkAuth(w http.ResponseWriter, r *http.Request) bool {
	if h.Pass == "" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin access not configured (ADMIN_PASS not set)"})
		return false
	}
	user, pass, ok := r.BasicAuth()
	userOK := subtle.ConstantTimeCompare([]byte(user), []byte(h.User)) == 1
	passOK := subtle.ConstantTimeCompare([]byte(pass), []byte(h.Pass)) == 1
	if !ok || !userOK || !passOK {
		if h.Metrics != nil {
			h.Metrics.RecordAuthFailure(r.RemoteAddr, user)
		}
		slog.Warn("Admin auth failed", "remote_addr", r.RemoteAddr, "user", user)
		w.Header().Set("WWW-Authenticate", `Basic realm="Admin"`)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return false
	}
	return true
}

type adminServiceRef struct {
	Host    string `json:"host"`
	Service string `json:"service"`
}

// HandleListServices returns all services from Icinga2 for the configured host(s).
// GET /admin/services
func (h *AdminHandler) HandleListServices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if !h.checkAuth(w, r) {
		return
	}

	targets, err := resolveScopedHosts(h.Targets, r.URL.Query().Get("host"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	services := make([]icinga.ServiceInfo, 0)
	var fetchErrors []string
	for _, target := range targets {
		hostServices, err := h.API.ListServices(target.HostName)
		if err != nil {
			slog.Error("Failed to list services from Icinga2", "host", target.HostName, "error", err)
			fetchErrors = append(fetchErrors, target.HostName+": "+err.Error())
			continue
		}
		services = append(services, hostServices...)
	}

	sort.Slice(services, func(i, j int) bool {
		if services[i].HostName == services[j].HostName {
			return services[i].Name < services[j].Name
		}
		return services[i].HostName < services[j].HostName
	})

	hostNames := make([]string, 0, len(targets))
	for _, target := range targets {
		hostNames = append(hostNames, target.HostName)
	}

	resp := map[string]any{
		"host":     firstHostName(hostNames),
		"hosts":    hostNames,
		"services": services,
		"count":    len(services),
	}
	if len(fetchErrors) > 0 {
		resp["errors"] = fetchErrors
	}

	writeJSON(w, http.StatusOK, resp)
}

// HandleDeleteService deletes a service from Icinga2.
// DELETE /admin/services/{service_name}
func (h *AdminHandler) HandleDeleteService(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if !h.checkAuth(w, r) {
		return
	}

	serviceName := strings.TrimPrefix(r.URL.Path, "/admin/services/")
	if serviceName == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "service name required"})
		return
	}

	target, err := resolveSingleHost(h.Targets, r.URL.Query().Get("host"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	// Use rate limiter for mutation
	if h.Limiter != nil {
		if err := h.Limiter.AcquireMutate(r.Context()); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "rate limit: " + err.Error()})
			return
		}
		defer h.Limiter.ReleaseMutate()
	}

	if err := h.API.DeleteService(target.HostName, serviceName); err != nil {
		slog.Error("Admin: failed to delete service", "host", target.HostName, "service", serviceName, "error", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"host":    target.HostName,
			"error":   err.Error(),
			"service": serviceName,
		})
		return
	}

	h.Cache.Remove(target.HostName, serviceName)
	slog.Info("Admin: service deleted", "host", target.HostName, "service", serviceName)

	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "deleted",
		"host":    target.HostName,
		"service": serviceName,
	})
}

// HandleBulkDelete deletes multiple services.
// POST /admin/services/bulk-delete  body: {"services": ["svc1", "svc2"]}
func (h *AdminHandler) HandleBulkDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if !h.checkAuth(w, r) {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	refs, err := h.parseBulkDeleteRequest(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if len(refs) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no services specified"})
		return
	}

	var results []map[string]any
	for _, ref := range refs {
		if h.Limiter != nil {
			if err := h.Limiter.AcquireMutate(r.Context()); err != nil {
				results = append(results, map[string]any{
					"host":    ref.Host,
					"service": ref.Service,
					"status":  "error",
					"error":   "rate limit: " + err.Error(),
				})
				continue
			}
		}

		if err := h.API.DeleteService(ref.Host, ref.Service); err != nil {
			if h.Limiter != nil {
				h.Limiter.ReleaseMutate()
			}
			slog.Error("Admin: bulk delete failed", "host", ref.Host, "service", ref.Service, "error", err)
			results = append(results, map[string]any{
				"host":    ref.Host,
				"service": ref.Service,
				"status":  "error",
				"error":   err.Error(),
			})
			continue
		}
		if h.Limiter != nil {
			h.Limiter.ReleaseMutate()
		}
		h.Cache.Remove(ref.Host, ref.Service)
		results = append(results, map[string]any{
			"host":    ref.Host,
			"service": ref.Service,
			"status":  "deleted",
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

// HandleRateLimitStats returns current rate limiter statistics.
// GET /admin/ratelimit
func (h *AdminHandler) HandleRateLimitStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if !h.checkAuth(w, r) {
		return
	}

	if h.Limiter == nil {
		writeJSON(w, http.StatusOK, map[string]string{"status": "rate limiter not configured"})
		return
	}

	mInUse, mMax, sInUse, sMax, queued, maxQ := h.Limiter.Stats()
	writeJSON(w, http.StatusOK, map[string]any{
		"mutate": map[string]int{"in_use": mInUse, "max": mMax},
		"status": map[string]int{"in_use": sInUse, "max": sMax},
		"queue":  map[string]int{"current": queued, "max": maxQ},
	})
}

func (h *AdminHandler) parseBulkDeleteRequest(r *http.Request) ([]adminServiceRef, error) {
	var payload struct {
		Services []json.RawMessage `json:"services"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		return nil, err
	}

	defaultTarget, defaultErr := resolveSingleHost(h.Targets, "")
	refs := make([]adminServiceRef, 0, len(payload.Services))
	for _, raw := range payload.Services {
		var ref adminServiceRef
		if len(raw) > 0 && raw[0] == '"' {
			var serviceName string
			if err := json.Unmarshal(raw, &serviceName); err != nil {
				return nil, err
			}
			if defaultErr != nil {
				return nil, defaultErr
			}
			ref = adminServiceRef{
				Host:    defaultTarget.HostName,
				Service: serviceName,
			}
		} else {
			if err := json.Unmarshal(raw, &ref); err != nil {
				return nil, err
			}
			if ref.Service == "" {
				return nil, errors.New("service field is required")
			}
			target, err := resolveSingleHost(h.Targets, ref.Host)
			if err != nil {
				return nil, err
			}
			ref.Host = target.HostName
		}
		refs = append(refs, ref)
	}

	return refs, nil
}

func firstHostName(hosts []string) string {
	if len(hosts) == 0 {
		return ""
	}
	if len(hosts) == 1 {
		return hosts[0]
	}
	return "ALL TARGETS"
}

// HandleClearHistory clears the history file.
// POST /admin/history/clear
func (h *AdminHandler) HandleClearHistory(w http.ResponseWriter, r *http.Request) {
	if !h.checkAuth(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if h.History == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "history not configured"})
		return
	}
	if err := h.History.Clear(); err != nil {
		slog.Error("Failed to clear history", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "history cleared"})
}

// HandleDebugToggle enables/disables the API debug ring buffer.
// POST /admin/debug/toggle  body: {"enabled": true|false}
func (h *AdminHandler) HandleDebugToggle(w http.ResponseWriter, r *http.Request) {
	if !h.checkAuth(w, r) {
		return
	}
	if h.DebugRing == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "debug ring not configured"})
		return
	}

	if r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, map[string]bool{"enabled": h.DebugRing.Enabled()})
		return
	}

	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	h.DebugRing.SetEnabled(body.Enabled)
	slog.Info("Debug ring toggled", "enabled", body.Enabled)
	writeJSON(w, http.StatusOK, map[string]bool{"enabled": body.Enabled})
}

// HandleSetServiceStatus sends a manual passive check result to Icinga2.
// POST /admin/services/{name}/status  body: {"host": "...", "exit_status": 0|1|2, "output": "..."}
func (h *AdminHandler) HandleSetServiceStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if !h.checkAuth(w, r) {
		return
	}

	// Extract service name: /admin/services/{name}/status
	path := strings.TrimPrefix(r.URL.Path, "/admin/services/")
	serviceName := strings.TrimSuffix(path, "/status")
	if serviceName == "" || serviceName == path {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "service name required"})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var body struct {
		Host       string `json:"host"`
		ExitStatus int    `json:"exit_status"`
		Output     string `json:"output"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}

	if body.ExitStatus < 0 || body.ExitStatus > 3 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "exit_status must be 0, 1, 2, or 3"})
		return
	}

	host := body.Host
	if host == "" {
		target, err := resolveSingleHost(h.Targets, "")
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		host = target.HostName
	}

	if body.Output == "" {
		labels := []string{"OK", "WARNING", "CRITICAL", "UNKNOWN"}
		body.Output = labels[body.ExitStatus] + ": Manual status set via dashboard"
	}

	if h.Limiter != nil {
		if err := h.Limiter.AcquireMutate(r.Context()); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "rate limit: " + err.Error()})
			return
		}
		defer h.Limiter.ReleaseMutate()
	}

	start := time.Now()
	if err := h.API.SendCheckResult(host, serviceName, body.ExitStatus, body.Output); err != nil {
		slog.Error("Admin: failed to set service status", "host", host, "service", serviceName, "exit_status", body.ExitStatus, "error", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	duration := time.Since(start)

	adminUser, _, _ := r.BasicAuth()
	labels := []string{"OK", "WARNING", "CRITICAL", "UNKNOWN"}
	slog.Info("Admin: manual status change",
		"host", host, "service", serviceName,
		"exit_status", body.ExitStatus, "user", adminUser)

	if h.History != nil {
		_ = h.History.Append(models.HistoryEntry{
			Timestamp:   time.Now(),
			RequestID:   "",
			SourceKey:   "admin:" + adminUser,
			HostName:    host,
			Mode:        "manual",
			Action:      "status_change",
			ServiceName: serviceName,
			Severity:    strings.ToLower(labels[body.ExitStatus]),
			ExitStatus:  body.ExitStatus,
			Message:     body.Output,
			IcingaOK:    true,
			DurationMs:  duration.Milliseconds(),
			RemoteAddr:  r.RemoteAddr,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":      "updated",
		"host":        host,
		"service":     serviceName,
		"exit_status": body.ExitStatus,
	})
}

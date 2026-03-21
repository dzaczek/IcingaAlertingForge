package handler

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sort"
	"strings"

	"icinga-webhook-bridge/cache"
	"icinga-webhook-bridge/config"
	"icinga-webhook-bridge/icinga"
)

// AdminHandler serves admin API endpoints for service management.
type AdminHandler struct {
	Cache   *cache.ServiceCache
	API     *icinga.APIClient
	Limiter *icinga.RateLimiter
	Targets map[string]config.TargetConfig
	User    string
	Pass    string
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
	for _, target := range targets {
		hostServices, err := h.API.ListServices(target.HostName)
		if err != nil {
			slog.Error("Failed to list services from Icinga2", "host", target.HostName, "error", err)
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "failed to list services: " + err.Error()})
			return
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

	writeJSON(w, http.StatusOK, map[string]any{
		"host":     firstHostName(hostNames),
		"hosts":    hostNames,
		"services": services,
		"count":    len(services),
	})
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
	if err := h.Limiter.AcquireMutate(r.Context()); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "rate limit: " + err.Error()})
		return
	}
	defer h.Limiter.ReleaseMutate()

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
		if err := h.Limiter.AcquireMutate(r.Context()); err != nil {
			results = append(results, map[string]any{
				"host":    ref.Host,
				"service": ref.Service,
				"status":  "error",
				"error":   "rate limit: " + err.Error(),
			})
			continue
		}

		if err := h.API.DeleteService(ref.Host, ref.Service); err != nil {
			h.Limiter.ReleaseMutate()
			slog.Error("Admin: bulk delete failed", "host", ref.Host, "service", ref.Service, "error", err)
			results = append(results, map[string]any{
				"host":    ref.Host,
				"service": ref.Service,
				"status":  "error",
				"error":   err.Error(),
			})
			continue
		}
		h.Limiter.ReleaseMutate()
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

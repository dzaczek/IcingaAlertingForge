package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"icinga-webhook-bridge/cache"
	"icinga-webhook-bridge/icinga"
)

// AdminHandler serves admin API endpoints for service management.
type AdminHandler struct {
	Cache    *cache.ServiceCache
	API      *icinga.APIClient
	Limiter  *icinga.RateLimiter
	HostName string
	User     string
	Pass     string
}

// checkAuth validates HTTP Basic Auth credentials for admin endpoints.
func (h *AdminHandler) checkAuth(w http.ResponseWriter, r *http.Request) bool {
	if h.Pass == "" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin access not configured (ADMIN_PASS not set)"})
		return false
	}
	user, pass, ok := r.BasicAuth()
	if !ok || user != h.User || pass != h.Pass {
		w.Header().Set("WWW-Authenticate", `Basic realm="Admin"`)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return false
	}
	return true
}

// HandleListServices returns all services from Icinga2 for the configured host.
// GET /admin/services
func (h *AdminHandler) HandleListServices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if !h.checkAuth(w, r) {
		return
	}

	services, err := h.API.ListServices(h.HostName)
	if err != nil {
		slog.Error("Failed to list services from Icinga2", "error", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "failed to list services: " + err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"host":     h.HostName,
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

	// Use rate limiter for mutation
	if err := h.Limiter.AcquireMutate(r.Context()); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "rate limit: " + err.Error()})
		return
	}
	defer h.Limiter.ReleaseMutate()

	if err := h.API.DeleteService(h.HostName, serviceName); err != nil {
		slog.Error("Admin: failed to delete service", "service", serviceName, "error", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error":   err.Error(),
			"service": serviceName,
		})
		return
	}

	h.Cache.Remove(serviceName)
	slog.Info("Admin: service deleted", "service", serviceName)

	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "deleted",
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

	var req struct {
		Services []string `json:"services"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if len(req.Services) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no services specified"})
		return
	}

	var results []map[string]any
	for _, svc := range req.Services {
		if err := h.Limiter.AcquireMutate(r.Context()); err != nil {
			results = append(results, map[string]any{
				"service": svc,
				"status":  "error",
				"error":   "rate limit: " + err.Error(),
			})
			continue
		}

		if err := h.API.DeleteService(h.HostName, svc); err != nil {
			h.Limiter.ReleaseMutate()
			slog.Error("Admin: bulk delete failed", "service", svc, "error", err)
			results = append(results, map[string]any{
				"service": svc,
				"status":  "error",
				"error":   err.Error(),
			})
			continue
		}
		h.Limiter.ReleaseMutate()
		h.Cache.Remove(svc)
		results = append(results, map[string]any{
			"service": svc,
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
		"mutate":  map[string]int{"in_use": mInUse, "max": mMax},
		"status":  map[string]int{"in_use": sInUse, "max": sMax},
		"queue":   map[string]int{"current": queued, "max": maxQ},
	})
}

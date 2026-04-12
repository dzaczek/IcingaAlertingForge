package handler

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"icinga-webhook-bridge/httputil"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"icinga-webhook-bridge/cache"
	"icinga-webhook-bridge/config"
	"icinga-webhook-bridge/history"
	"icinga-webhook-bridge/icinga"
	"icinga-webhook-bridge/metrics"
	"icinga-webhook-bridge/models"
	"icinga-webhook-bridge/queue"
	"icinga-webhook-bridge/rbac"
)

// AdminHandler serves admin API endpoints for service management.
type AdminHandler struct {
	Cache      *cache.ServiceCache
	API        *icinga.APIClient
	Limiter    *icinga.RateLimiter
	History    *history.Logger
	Metrics    *metrics.Collector
	DebugRing  *icinga.DebugRing
	Targets    map[string]config.TargetConfig
	User       string
	Pass       string
	RetryQueue *queue.Queue
	RBAC       *rbac.Manager
}

// checkAuth validates HTTP Basic Auth credentials for admin endpoints.
// Checks against both the primary admin credentials and RBAC users.
func (h *AdminHandler) checkAuth(w http.ResponseWriter, r *http.Request) bool {
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

	// Check RBAC users
	if h.RBAC != nil {
		if _, authenticated := h.RBAC.Authenticate(user, pass); authenticated {
			return true
		}
	}

	if h.Metrics != nil {
		h.Metrics.RecordAuthFailure(r.RemoteAddr, user)
	}
	slog.Warn("Admin auth failed", "remote_addr", r.RemoteAddr, "user", user)
	w.Header().Set("WWW-Authenticate", `Basic realm="Admin"`)
	httputil.WriteJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
	return false
}

// requirePermission checks that the authenticated user has the given RBAC permission.
// The primary admin user (ADMIN_USER) always has full access.
func (h *AdminHandler) requirePermission(w http.ResponseWriter, r *http.Request, perm rbac.Permission) bool {
	user, _, _ := r.BasicAuth()
	// Primary admin has all permissions
	if subtle.ConstantTimeCompare([]byte(user), []byte(h.User)) == 1 {
		return true
	}
	// Check RBAC permission
	if h.RBAC != nil && h.RBAC.HasPermission(user, perm) {
		return true
	}
	httputil.WriteJSON(w, http.StatusForbidden, map[string]string{"error": "insufficient permissions"})
	return false
}

type adminServiceRef struct {
	Host    string `json:"host"`
	Service string `json:"service"`
}

// HandleListServices returns all services from Icinga2 for the configured host(s).
// GET /admin/services
func (h *AdminHandler) HandleListServices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httputil.WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if !h.checkAuth(w, r) {
		return
	}
	if !h.requirePermission(w, r, rbac.PermViewDashboard) {
		return
	}

	targets, err := resolveScopedHosts(h.Targets, r.URL.Query().Get("host"))
	if err != nil {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	services := make([]icinga.ServiceInfo, 0)
	var fetchErrors []string
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, target := range targets {
		wg.Add(1)
		go func(targetHostName string) {
			defer wg.Done()
			hostServices, err := h.API.ListServices(targetHostName)

			mu.Lock()
			defer mu.Unlock()

			if err != nil {
				slog.Error("Failed to list services from Icinga2", "host", targetHostName, "error", err)
				fetchErrors = append(fetchErrors, targetHostName+": "+err.Error())
				return
			}
			services = append(services, hostServices...)
		}(target.HostName)
	}
	wg.Wait()

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

	httputil.WriteJSON(w, http.StatusOK, resp)
}

// HandleDeleteService deletes a service from Icinga2.
// DELETE /admin/services/{service_name}
func (h *AdminHandler) HandleDeleteService(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		httputil.WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if !h.checkAuth(w, r) {
		return
	}
	if !h.requirePermission(w, r, rbac.PermDeleteService) {
		return
	}

	serviceName := strings.TrimPrefix(r.URL.Path, "/admin/services/")
	if serviceName == "" {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "service name required"})
		return
	}

	target, err := resolveSingleHost(h.Targets, r.URL.Query().Get("host"))
	if err != nil {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	// Use rate limiter for mutation
	if h.Limiter != nil {
		if err := h.Limiter.AcquireMutate(r.Context()); err != nil {
			httputil.WriteJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "rate limit: " + err.Error()})
			return
		}
		defer h.Limiter.ReleaseMutate()
	}

	if err := h.API.DeleteService(target.HostName, serviceName); err != nil {
		var conflict *icinga.ErrConflict
		if errors.As(err, &conflict) {
			slog.Warn("Admin: delete service refused (conflict)", "host", target.HostName, "service", serviceName, "error", err)
			httputil.WriteJSON(w, http.StatusForbidden, map[string]string{
				"host":    target.HostName,
				"error":   err.Error(),
				"service": serviceName,
			})
			return
		}
		slog.Error("Admin: failed to delete service", "host", target.HostName, "service", serviceName, "error", err)
		httputil.WriteJSON(w, http.StatusBadGateway, map[string]string{
			"host":    target.HostName,
			"error":   err.Error(),
			"service": serviceName,
		})
		return
	}

	h.Cache.Remove(target.HostName, serviceName)
	slog.Info("Admin: service deleted", "host", target.HostName, "service", serviceName)

	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"status":  "deleted",
		"host":    target.HostName,
		"service": serviceName,
	})
}

// HandleBulkDelete deletes multiple services.
// POST /admin/services/bulk-delete  body: {"services": ["svc1", "svc2"]}
func (h *AdminHandler) HandleBulkDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httputil.WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if !h.checkAuth(w, r) {
		return
	}
	if !h.requirePermission(w, r, rbac.PermDeleteService) {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	refs, err := h.parseBulkDeleteRequest(r)
	if err != nil {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if len(refs) == 0 {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "no services specified"})
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
			var conflict *icinga.ErrConflict
			if errors.As(err, &conflict) {
				slog.Warn("Admin: bulk delete refused (conflict)", "host", ref.Host, "service", ref.Service, "error", err)
				results = append(results, map[string]any{
					"host":    ref.Host,
					"service": ref.Service,
					"status":  "error",
					"error":   "rate limit: " + err.Error(),
				})
				continue
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

	httputil.WriteJSON(w, http.StatusOK, map[string]any{"results": results})
}

// HandleRateLimitStats returns current rate limiter statistics.
// GET /admin/ratelimit
func (h *AdminHandler) HandleRateLimitStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httputil.WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if !h.checkAuth(w, r) {
		return
	}
	if !h.requirePermission(w, r, rbac.PermViewDashboard) {
		return
	}

	if h.Limiter == nil {
		httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "rate limiter not configured"})
		return
	}

	mInUse, mMax, sInUse, sMax, queued, maxQ := h.Limiter.Stats()
	httputil.WriteJSON(w, http.StatusOK, map[string]any{
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
	if !h.requirePermission(w, r, rbac.PermClearHistory) {
		return
	}
	if r.Method != http.MethodPost {
		httputil.WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if h.History == nil {
		httputil.WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": "history not configured"})
		return
	}
	if err := h.History.Clear(); err != nil {
		slog.Error("Failed to clear history", "error", err)
		httputil.WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "history cleared"})
}

// HandleDebugToggle enables/disables the API debug ring buffer.
// POST /admin/debug/toggle  body: {"enabled": true|false}
func (h *AdminHandler) HandleDebugToggle(w http.ResponseWriter, r *http.Request) {
	if !h.checkAuth(w, r) {
		return
	}
	if !h.requirePermission(w, r, rbac.PermDebugToggle) {
		return
	}
	if h.DebugRing == nil {
		httputil.WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": "debug ring not configured"})
		return
	}

	if r.Method == http.MethodGet {
		httputil.WriteJSON(w, http.StatusOK, map[string]bool{"enabled": h.DebugRing.Enabled()})
		return
	}

	if r.Method != http.MethodPost {
		httputil.WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	h.DebugRing.SetEnabled(body.Enabled)
	slog.Info("Debug ring toggled", "enabled", body.Enabled)
	httputil.WriteJSON(w, http.StatusOK, map[string]bool{"enabled": body.Enabled})
}

// HandleSetServiceStatus sends a manual passive check result to Icinga2.
// POST /admin/services/{name}/status  body: {"host": "...", "exit_status": 0|1|2, "output": "..."}
func (h *AdminHandler) HandleSetServiceStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httputil.WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if !h.checkAuth(w, r) {
		return
	}
	if !h.requirePermission(w, r, rbac.PermChangeStatus) {
		return
	}

	// Extract service name: /admin/services/{name}/status
	path := strings.TrimPrefix(r.URL.Path, "/admin/services/")
	serviceName := strings.TrimSuffix(path, "/status")
	if serviceName == "" || serviceName == path {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "service name required"})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var body struct {
		Host       string `json:"host"`
		ExitStatus int    `json:"exit_status"`
		Output     string `json:"output"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}

	if body.ExitStatus < 0 || body.ExitStatus > 3 {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "exit_status must be 0, 1, 2, or 3"})
		return
	}

	host := body.Host
	if host == "" {
		target, err := resolveSingleHost(h.Targets, "")
		if err != nil {
			httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
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
			httputil.WriteJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "rate limit: " + err.Error()})
			return
		}
		defer h.Limiter.ReleaseMutate()
	}

	start := time.Now()
	if err := h.API.SendCheckResult(host, serviceName, body.ExitStatus, body.Output); err != nil {
		slog.Error("Admin: failed to set service status", "host", host, "service", serviceName, "exit_status", body.ExitStatus, "error", err)
		httputil.WriteJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
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

	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"status":      "updated",
		"host":        host,
		"service":     serviceName,
		"exit_status": body.ExitStatus,
	})
}

// HandleQueueStats returns current retry queue statistics.
// GET /admin/queue
func (h *AdminHandler) HandleQueueStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httputil.WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if !h.checkAuth(w, r) {
		return
	}
	if !h.requirePermission(w, r, rbac.PermViewQueue) {
		return
	}
	if h.RetryQueue == nil {
		httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "disabled"})
		return
	}
	httputil.WriteJSON(w, http.StatusOK, h.RetryQueue.Stats())
}

// HandleQueueFlush forces immediate retry of all queued items.
// POST /admin/queue/flush
func (h *AdminHandler) HandleQueueFlush(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httputil.WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if !h.checkAuth(w, r) {
		return
	}
	if !h.requirePermission(w, r, rbac.PermFlushQueue) {
		return
	}
	if h.RetryQueue == nil {
		httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "disabled"})
		return
	}
	processed := h.RetryQueue.Flush()
	slog.Info("Admin: queue flush", "processed", processed)
	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"status":    "flushed",
		"processed": processed,
		"remaining": h.RetryQueue.Depth(),
	})
}

// HandleListUsers returns all RBAC users (without passwords).
// GET /admin/users
func (h *AdminHandler) HandleListUsers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httputil.WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if !h.checkAuth(w, r) {
		return
	}
	if !h.requirePermission(w, r, rbac.PermManageUsers) {
		return
	}
	if h.RBAC == nil {
		httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "disabled"})
		return
	}
	httputil.WriteJSON(w, http.StatusOK, h.RBAC.ListUsers())
}

// HandleCreateUser adds or updates an RBAC user.
// POST /admin/users
func (h *AdminHandler) HandleCreateUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httputil.WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if !h.checkAuth(w, r) {
		return
	}
	if !h.requirePermission(w, r, rbac.PermManageUsers) {
		return
	}
	if h.RBAC == nil {
		httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "disabled"})
		return
	}

	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if body.Username == "" || body.Password == "" {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "username and password required"})
		return
	}

	role := rbac.ParseRole(body.Role)
	if err := h.RBAC.AddUser(rbac.User{
		Username: body.Username,
		Password: body.Password,
		Role:     role,
	}); err != nil {
		slog.Error("RBAC: failed to persist user", "error", err)
		httputil.WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save user"})
		return
	}

	actor, _, _ := r.BasicAuth()
	slog.Info("RBAC: user created/updated via admin API", "actor", actor, "target_user", body.Username, "role", role)
	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"status":   "ok",
		"username": body.Username,
		"role":     role,
	})
}

// HandleDeleteUser removes an RBAC user.
// DELETE /admin/users/{username}
func (h *AdminHandler) HandleDeleteUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		httputil.WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if !h.checkAuth(w, r) {
		return
	}
	if !h.requirePermission(w, r, rbac.PermManageUsers) {
		return
	}
	if h.RBAC == nil {
		httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "disabled"})
		return
	}

	// Extract username from path: /admin/users/{username}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/admin/users/"), "/")
	username := parts[0]
	if username == "" {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "username required"})
		return
	}

	// Prevent self-deletion
	actor, _, _ := r.BasicAuth()
	if username == actor {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "cannot delete your own account"})
		return
	}

	removed, err := h.RBAC.RemoveUser(username)
	if err != nil {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if !removed {
		httputil.WriteJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
		return
	}

	slog.Info("RBAC: user deleted via admin API", "actor", actor, "target_user", username)
	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"status":   "deleted",
		"username": username,
	})
}

// HandleFreezeService freezes or unfreezes alert forwarding for a service.
// POST   /admin/services/{name}/freeze  body: {"host":"...","duration_seconds":N} N=0 → permanent
// DELETE /admin/services/{name}/freeze  body: {"host":"..."}
func (h *AdminHandler) HandleFreezeService(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodDelete {
		httputil.WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if !h.checkAuth(w, r) {
		return
	}
	if !h.requirePermission(w, r, rbac.PermChangeStatus) {
		return
	}

	// Extract service name: /admin/services/{name}/freeze
	path := strings.TrimPrefix(r.URL.Path, "/admin/services/")
	serviceName := strings.TrimSuffix(path, "/freeze")
	if serviceName == "" || serviceName == path {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "service name required"})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var body struct {
		Host            string `json:"host"`
		DurationSeconds int    `json:"duration_seconds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}

	host := body.Host
	if host == "" {
		target, err := resolveSingleHost(h.Targets, "")
		if err != nil {
			httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		host = target.HostName
	}

	adminUser, _, _ := r.BasicAuth()

	if r.Method == http.MethodDelete {
		h.Cache.Unfreeze(host, serviceName)
		slog.Info("Service unfrozen", "host", host, "service", serviceName, "user", adminUser)
		if h.History != nil {
			_ = h.History.Append(models.HistoryEntry{
				Timestamp:   time.Now(),
				SourceKey:   "admin:" + adminUser,
				HostName:    host,
				Mode:        "manual",
				Action:      "unfreeze",
				ServiceName: serviceName,
				IcingaOK:    true,
				RemoteAddr:  r.RemoteAddr,
			})
		}
		httputil.WriteJSON(w, http.StatusOK, map[string]any{
			"status":  "unfrozen",
			"host":    host,
			"service": serviceName,
		})
		return
	}

	// POST — freeze
	var until *time.Time
	if body.DurationSeconds > 0 {
		t := time.Now().Add(time.Duration(body.DurationSeconds) * time.Second)
		until = &t
	}
	h.Cache.Freeze(host, serviceName, until)

	resp := map[string]any{
		"status":  "frozen",
		"host":    host,
		"service": serviceName,
	}
	msg := "Frozen permanently"
	if until != nil {
		resp["frozen_until"] = until.Format(time.RFC3339)
		msg = "Frozen until " + until.Format(time.RFC3339)
	} else {
		resp["frozen_until"] = nil
	}
	slog.Info("Service frozen", "host", host, "service", serviceName, "until", until, "user", adminUser)
	if h.History != nil {
		_ = h.History.Append(models.HistoryEntry{
			Timestamp:   time.Now(),
			SourceKey:   "admin:" + adminUser,
			HostName:    host,
			Mode:        "manual",
			Action:      "freeze",
			ServiceName: serviceName,
			Message:     msg,
			IcingaOK:    true,
			RemoteAddr:  r.RemoteAddr,
		})
	}
	httputil.WriteJSON(w, http.StatusOK, resp)
}

// HandleListFrozen returns all currently frozen services.
// GET /admin/services/frozen
func (h *AdminHandler) HandleListFrozen(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httputil.WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if !h.checkAuth(w, r) {
		return
	}

	entries := h.Cache.AllFrozen()
	type frozenItem struct {
		Host        string  `json:"host"`
		Service     string  `json:"service"`
		FrozenUntil *string `json:"frozen_until"` // nil = permanent
	}
	items := make([]frozenItem, len(entries))
	for i, e := range entries {
		var fu *string
		if e.FrozenUntil != nil {
			s := e.FrozenUntil.Format(time.RFC3339)
			fu = &s
		}
		items[i] = frozenItem{Host: e.Host, Service: e.Service, FrozenUntil: fu}
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"frozen": items})
}

package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"syscall"
	"time"

	"icinga-webhook-bridge/audit"
	"icinga-webhook-bridge/auth"
	"icinga-webhook-bridge/cache"
	"icinga-webhook-bridge/config"
	"icinga-webhook-bridge/configstore"
	"icinga-webhook-bridge/handler"
	"icinga-webhook-bridge/health"
	"icinga-webhook-bridge/history"
	"icinga-webhook-bridge/httputil"
	"icinga-webhook-bridge/icinga"
	"icinga-webhook-bridge/metrics"
	"icinga-webhook-bridge/queue"
	"icinga-webhook-bridge/rbac"
)

// version is set at build time via -ldflags "-X main.version=..."
// Falls back to "dev" when built without ldflags.
var version = "dev"

func main() {
	// ── Load Configuration ──────────────────────────────────────────
	cfg, err := config.Load()
	if err != nil {
		slog.Error("configuration error", "error", err)
		os.Exit(1)
	}

	// ── Setup Structured Logging ────────────────────────────────────
	setupLogging(cfg.LogLevel, cfg.LogFormat)

	slog.Info("Starting Webhook Bridge",
		"version", version,
		"listen", cfg.ListenAddr(),
	)

	// ── Config Store (dashboard-based config) ──────────────────────
	var cfgStore *configstore.Store
	if cfg.ConfigInDashboard {
		var err error
		cfgStore, err = configstore.New(cfg.ConfigFilePath, cfg.ConfigEncryptionKey)
		if err != nil {
			slog.Error("Failed to initialize config store", "error", err)
			os.Exit(1)
		}
		if cfgStore.Exists() {
			if err := cfgStore.Load(); err != nil {
				slog.Error("Failed to load stored config", "error", err)
				os.Exit(1)
			}
			// JSON is the source of truth — only infrastructure fields come from env
			storedCfg := cfgStore.ToConfig(cfg.ServerPort, cfg.ServerHost)
			storedCfg.ConfigInDashboard = true
			storedCfg.ConfigEncryptionKey = cfg.ConfigEncryptionKey
			storedCfg.ConfigFilePath = cfg.ConfigFilePath
			// Admin credentials always from env (never stored in JSON)
			storedCfg.AdminUser = cfg.AdminUser
			storedCfg.AdminPass = cfg.AdminPass
			// Enterprise features: use env as defaults, JSON overrides if stored
			storedCfg.RetryQueueEnabled = cfg.RetryQueueEnabled
			storedCfg.RetryQueueMaxSize = cfg.RetryQueueMaxSize
			storedCfg.RetryQueueFilePath = cfg.RetryQueueFilePath
			storedCfg.RetryQueueRetryBaseSec = cfg.RetryQueueRetryBaseSec
			storedCfg.RetryQueueRetryMaxSec = cfg.RetryQueueRetryMaxSec
			storedCfg.RetryQueueCheckInterval = cfg.RetryQueueCheckInterval
			storedCfg.HealthCheckEnabled = cfg.HealthCheckEnabled
			storedCfg.HealthCheckIntervalSec = cfg.HealthCheckIntervalSec
			storedCfg.HealthCheckServiceName = cfg.HealthCheckServiceName
			storedCfg.HealthCheckTargetHost = cfg.HealthCheckTargetHost
			storedCfg.HealthCheckRegister = cfg.HealthCheckRegister
			storedCfg.AuditLogEnabled = cfg.AuditLogEnabled
			storedCfg.AuditLogFile = cfg.AuditLogFile
			storedCfg.AuditLogFormat = cfg.AuditLogFormat
			cfg = storedCfg
			slog.Info("Configuration loaded from dashboard store", "path", cfg.ConfigFilePath)
		} else {
			if err := cfgStore.MigrateFromEnv(cfg); err != nil {
				slog.Error("Failed to migrate config to store", "error", err)
				os.Exit(1)
			}
		}
	}

	// ── Initialize Components ───────────────────────────────────────
	keyStore := auth.NewKeyStore(cfg.WebhookRoutes)

	apiClient := icinga.NewAPIClient(
		cfg.Icinga2Host,
		cfg.Icinga2User,
		cfg.Icinga2Pass,
		cfg.Icinga2TLSSkipVerify,
	)
	apiClient.ConflictPolicy = icinga.ConflictPolicy(cfg.Icinga2ConflictPolicy)
	apiClient.Force = cfg.Icinga2Force
	debugRing := icinga.NewDebugRing()
	apiClient.Debug = debugRing

	// ── Validate / auto-create hosts in Icinga2 ─────────────────────
	if err := ensureConfiguredHosts(apiClient, cfg.Targets, cfg.Icinga2HostAutoCreate); err != nil {
		slog.Error("Failed to prepare target hosts", "error", err)
		os.Exit(1)
	}

	serviceCache := cache.NewServiceCache(cfg.CacheTTLMinutes)
	var restoreWg sync.WaitGroup
	// ⚡ Bolt: Fetch and restore managed services from Icinga concurrently to reduce startup latency.
	// Impact: Reduces wait time from O(N) to O(1) relative to target count.
	for _, target := range sortedTargets(cfg.Targets) {
		restoreWg.Add(1)
		go func(t config.TargetConfig) {
			defer restoreWg.Done()
			restoreManagedServicesFromIcinga(apiClient, serviceCache, t.HostName)
		}(target)
	}
	restoreWg.Wait()

	historyLogger, err := history.NewLogger(cfg.HistoryFile, cfg.HistoryMaxEntries)
	if err != nil {
		slog.Error("Failed to initialize history logger", "error", err)
		os.Exit(1)
	}

	// Start history maintenance goroutine (rotation checks)
	mainCtx, mainCancel := context.WithCancel(context.Background())
	defer mainCancel()
	historyLogger.StartMaintenance(mainCtx)
	serviceCache.StartMaintenance(mainCtx, time.Minute)

	// ── Metrics Collector ────────────────────────────────────────────
	metricsCollector := metrics.NewCollector()
	perKeyCollector := metrics.NewPerKeyCollector()
	prometheusCollector := metrics.NewPrometheusCollector(
		metricsCollector,
		historyLogger,
		nil, // queue not yet initialized
		nil, // rateLimiter not yet initialized
		nil, // healthChecker not yet initialized
		perKeyCollector,
	)

	// ── Rate Limiter ────────────────────────────────────────────────
	rateLimiter := icinga.NewRateLimiter(
		cfg.RateLimitMutate,
		cfg.RateLimitStatus,
		cfg.RateLimitMaxQueue,
	)
	slog.Info("Rate limiter initialized",
		"mutate_max", cfg.RateLimitMutate,
		"status_max", cfg.RateLimitStatus,
		"queue_max", cfg.RateLimitMaxQueue,
	)

	// ── Retry Queue ────────────────────────────────────────────────
	var retryQueue *queue.Queue
	if cfg.RetryQueueEnabled {
		retryQueue = queue.New(queue.Config{
			Enabled:       true,
			MaxSize:       cfg.RetryQueueMaxSize,
			FilePath:      cfg.RetryQueueFilePath,
			RetryBase:     time.Duration(cfg.RetryQueueRetryBaseSec) * time.Second,
			RetryMax:      time.Duration(cfg.RetryQueueRetryMaxSec) * time.Second,
			CheckInterval: time.Duration(cfg.RetryQueueCheckInterval) * time.Second,
		}, apiClient)
		retryQueue.Start(mainCtx)
		slog.Info("Retry queue enabled",
			"max_size", cfg.RetryQueueMaxSize,
			"retry_base", cfg.RetryQueueRetryBaseSec,
			"retry_max", cfg.RetryQueueRetryMaxSec,
		)
	}

	// ── Audit Logger ───────────────────────────────────────────────
	auditLogger, err := audit.New(audit.Config{
		Enabled: cfg.AuditLogEnabled,
		File:    cfg.AuditLogFile,
		Format:  cfg.AuditLogFormat,
	})
	if err != nil {
		slog.Error("Failed to initialize audit logger", "error", err)
		os.Exit(1)
	}
	defer auditLogger.Close()

	// ── Health Checker ─────────────────────────────────────────────
	var healthChecker *health.Checker
	if cfg.HealthCheckEnabled {
		targetHost := cfg.HealthCheckTargetHost
		if targetHost == "" {
			// Default to first target host
			for _, t := range sortedTargets(cfg.Targets) {
				targetHost = t.HostName
				break
			}
		}
		healthChecker = health.New(health.Config{
			Enabled:     true,
			IntervalSec: cfg.HealthCheckIntervalSec,
			ServiceName: cfg.HealthCheckServiceName,
			TargetHost:  targetHost,
			Register:    cfg.HealthCheckRegister,
		}, &icingaHealthAdapter{api: apiClient})
		go healthChecker.Start(mainCtx)
	}

	// ── RBAC Manager ───────────────────────────────────────────────
	rbacUsers := []rbac.User{
		{Username: cfg.AdminUser, Password: cfg.AdminPass, Role: rbac.RoleAdmin},
	}
	// Restore persisted RBAC users from configstore
	if cfgStore != nil {
		for _, su := range cfgStore.GetUsers() {
			rbacUsers = append(rbacUsers, rbac.User{
				Username: su.Username,
				Password: su.Password,
				Role:     rbac.ParseRole(su.Role),
			})
		}
		slog.Info("RBAC: loaded persisted users", "count", len(cfgStore.GetUsers()))
	}
	rbacManager := rbac.New(rbacUsers)
	rbacManager.SetPrimary(cfg.AdminUser)
	// Wire persistence: save non-primary users to configstore on every mutation
	if cfgStore != nil {
		rbacManager.SetOnSave(func() error {
			users := rbacManager.PersistableUsers()
			stored := make([]configstore.StoredUser, len(users))
			for i, u := range users {
				stored[i] = configstore.StoredUser{
					Username: u.Username,
					Password: u.Password,
					Role:     string(u.Role),
				}
			}
			return cfgStore.SetUsers(stored)
		})
	}

	// ── SSE Broker ─────────────────────────────────────────────────
	sseBroker := handler.NewSSEBroker()

	// Wire debug ring to SSE for real-time dev panel
	debugRing.SetListener(func(entry icinga.DebugEntry) {
		data, err := json.Marshal(entry)
		if err != nil {
			return
		}
		sseBroker.PublishRaw("debug", data)
	})

	// ── Create Handlers ─────────────────────────────────────────────
	webhookHandler := &handler.WebhookHandler{
		KeyStore:   keyStore,
		Cache:      serviceCache,
		API:        apiClient,
		History:    historyLogger,
		Targets:    cfg.Targets,
		Limiter:    rateLimiter,
		Metrics:    metricsCollector,
		PerKey:     perKeyCollector,
		SSE:        sseBroker,
		DebugRing:  debugRing,
		RetryQueue: retryQueue,
		Audit:      auditLogger,
	}

	statusHandler := &handler.StatusHandler{
		Cache:   serviceCache,
		API:     apiClient,
		Targets: cfg.Targets,
	}

	historyHandler := history.NewHandler(historyLogger)

	startedAt := time.Now()

	dashboardHandler := &handler.DashboardHandler{
		Cache:             serviceCache,
		History:           historyLogger,
		API:               apiClient,
		Metrics:           metricsCollector,
		PromCollector:     prometheusCollector,
		Targets:           cfg.Targets,
		AdminUser:         cfg.AdminUser,
		AdminPass:         cfg.AdminPass,
		Version:           version,
		StartedAt:         startedAt,
		DebugRing:         debugRing,
		ConfigInDashboard: cfg.ConfigInDashboard,
		RetryQueue:        retryQueue,
		HealthChecker:     healthChecker,
		Audit:             auditLogger,
		RBAC:              rbacManager,
	}

	adminHandler := &handler.AdminHandler{
		Cache:      serviceCache,
		API:        apiClient,
		Limiter:    rateLimiter,
		History:    historyLogger,
		Metrics:    metricsCollector,
		DebugRing:  debugRing,
		Targets:    cfg.Targets,
		User:       cfg.AdminUser,
		Pass:       cfg.AdminPass,
		RetryQueue: retryQueue,
		RBAC:       rbacManager,
	}

	// ── Auth Middleware ─────────────────────────────────────────────
	requireAuth := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if cfg.AdminPass == "" {
				httputil.WriteJSON(w, http.StatusForbidden, map[string]string{"error": "admin access not configured"})
				return
			}
			user, pass, ok := r.BasicAuth()
			if !ok {
				w.Header().Set("WWW-Authenticate", `Basic realm="IcingaAlertForge"`)
				httputil.WriteJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
				return
			}
			// Check primary admin
			primaryOK := subtle.ConstantTimeCompare([]byte(user), []byte(cfg.AdminUser)) == 1 &&
				subtle.ConstantTimeCompare([]byte(pass), []byte(cfg.AdminPass)) == 1
			// Check RBAC users
			rbacOK := false
			if !primaryOK && rbacManager != nil {
				_, rbacOK = rbacManager.Authenticate(user, pass)
			}
			if !primaryOK && !rbacOK {
				if metricsCollector != nil && user != "" {
					metricsCollector.RecordAuthFailure(r.RemoteAddr, user)
				}
				w.Header().Set("WWW-Authenticate", `Basic realm="IcingaAlertForge"`)
				httputil.WriteJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
				return
			}
			next(w, r)
		}
	}

	// ── Register Routes ─────────────────────────────────────────────
	mux := http.NewServeMux()

	// ── Prometheus Metrics ─────────────────────────────────────────
	// Update collector with fully initialized components
	prometheusCollector.UpdateComponents(retryQueue, rateLimiter, healthChecker)

	if cfg.MetricsEnabled {
		reg := prometheus.NewRegistry()
		reg.MustRegister(prometheusCollector)

		metricsHandler := promhttp.HandlerFor(reg, promhttp.HandlerOpts{})

		// Metrics Auth Middleware
		metricsAuth := func(next http.Handler) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				// 1. Try Token Auth if configured
				if cfg.MetricsToken != "" {
					authHeader := r.Header.Get("Authorization")
					if strings.HasPrefix(authHeader, "Bearer ") {
						token := strings.TrimPrefix(authHeader, "Bearer ")
						if subtle.ConstantTimeCompare([]byte(token), []byte(cfg.MetricsToken)) == 1 {
							next.ServeHTTP(w, r)
							return
						}
					}
				}

				// 2. Fall back to Admin Basic Auth
				user, pass, ok := r.BasicAuth()
				if !ok {
					w.Header().Set("WWW-Authenticate", `Basic realm="IcingaAlertForge"`)
					httputil.WriteJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
					return
				}
				primaryOK := subtle.ConstantTimeCompare([]byte(user), []byte(cfg.AdminUser)) == 1 &&
					subtle.ConstantTimeCompare([]byte(pass), []byte(cfg.AdminPass)) == 1

				rbacOK := false
				if !primaryOK && rbacManager != nil {
					_, rbacOK = rbacManager.Authenticate(user, pass)
				}

				if primaryOK || rbacOK {
					next.ServeHTTP(w, r)
					return
				}

				w.Header().Set("WWW-Authenticate", `Basic realm="IcingaAlertForge"`)
				httputil.WriteJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			}
		}

		mux.Handle("/metrics", metricsAuth(metricsHandler))
	}

	// Health check (enhanced with Icinga2 connectivity status)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]any{
			"status":  "ok",
			"version": version,
		}
		if healthChecker != nil {
			hs := healthChecker.GetStatus()
			resp["icinga_up"] = hs.IcingaUp
			resp["healthy"] = hs.Healthy
			resp["last_check"] = hs.LastCheck
			resp["consecutive_fails"] = hs.ConsecutiveFails
			if !hs.Healthy {
				resp["status"] = "degraded"
			}
		}
		if retryQueue != nil {
			resp["queue_depth"] = retryQueue.Depth()
		}
		json.NewEncoder(w).Encode(resp)
	})

	// Core endpoints
	mux.Handle("/webhook", webhookHandler)
	mux.Handle("/status/beauty/events", sseBroker)
	mux.Handle("/status/beauty", dashboardHandler)
	mux.HandleFunc("/status/beauty/logout", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Basic realm="IcingaAlertForge"`)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `<html><head><meta http-equiv="refresh" content="1;url=/status/beauty"></head><body>Logged out. Redirecting...</body></html>`)
	})
	mux.HandleFunc("/status/", requireAuth(func(w http.ResponseWriter, r *http.Request) {
		statusHandler.ServeHTTP(w, r)
	}))

	// History endpoints (auth required — exposes alert names, hosts, IPs)
	mux.HandleFunc("/history", requireAuth(historyHandler.HandleHistory))
	mux.HandleFunc("/history/export", requireAuth(historyHandler.HandleExport))

	// Admin endpoints (password protected)
	mux.HandleFunc("/admin/services/frozen", adminHandler.HandleListFrozen)
	mux.HandleFunc("/admin/services/bulk-delete", adminHandler.HandleBulkDelete)
	mux.HandleFunc("/admin/services/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/status") {
			adminHandler.HandleSetServiceStatus(w, r)
		} else if strings.HasSuffix(r.URL.Path, "/freeze") {
			adminHandler.HandleFreezeService(w, r)
		} else {
			adminHandler.HandleDeleteService(w, r)
		}
	})
	mux.HandleFunc("/admin/services", adminHandler.HandleListServices)
	mux.HandleFunc("/admin/ratelimit", adminHandler.HandleRateLimitStats)
	mux.HandleFunc("/admin/queue", adminHandler.HandleQueueStats)
	mux.HandleFunc("/admin/queue/flush", adminHandler.HandleQueueFlush)
	mux.HandleFunc("/admin/users/", adminHandler.HandleDeleteUser)
	mux.HandleFunc("/admin/users", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			adminHandler.HandleListUsers(w, r)
		case http.MethodPost:
			adminHandler.HandleCreateUser(w, r)
		default:
			httputil.WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		}
	})
	mux.HandleFunc("/admin/history/clear", adminHandler.HandleClearHistory)
	mux.HandleFunc("/admin/debug/toggle", adminHandler.HandleDebugToggle)

	// Settings endpoints (only when CONFIG_IN_DASHBOARD=true)
	if cfg.ConfigInDashboard && cfgStore != nil {
		settingsHandler := &handler.SettingsHandler{
			Store:   cfgStore,
			User:    cfg.AdminUser,
			Pass:    cfg.AdminPass,
			Metrics: metricsCollector,
			RBAC:    rbacManager,
		}
		settingsHandler.OnReload = func(newCfg *config.Config) {
			slog.Info("Hot-reloading configuration from dashboard store")

			newKeyStore := auth.NewKeyStore(newCfg.WebhookRoutes)
			webhookHandler.KeyStore = newKeyStore
			webhookHandler.Targets = newCfg.Targets

			apiClient.UpdateCredentials(newCfg.Icinga2Host, newCfg.Icinga2User, newCfg.Icinga2Pass, newCfg.Icinga2TLSSkipVerify)
			apiClient.ConflictPolicy = icinga.ConflictPolicy(newCfg.Icinga2ConflictPolicy)
			apiClient.Force = newCfg.Icinga2Force

			if err := ensureConfiguredHosts(apiClient, newCfg.Targets, newCfg.Icinga2HostAutoCreate); err != nil {
				slog.Error("Failed to prepare target hosts on config reload", "error", err)
			}

			statusHandler.Targets = newCfg.Targets
			adminHandler.Targets = newCfg.Targets
			dashboardHandler.Targets = newCfg.Targets
			dashboardHandler.AdminUser = newCfg.AdminUser
			dashboardHandler.AdminPass = newCfg.AdminPass
			adminHandler.User = newCfg.AdminUser
			adminHandler.Pass = newCfg.AdminPass
			settingsHandler.User = newCfg.AdminUser
			settingsHandler.Pass = newCfg.AdminPass

			slog.Info("Configuration hot-reload complete",
				"targets", len(newCfg.Targets),
				"routes", len(newCfg.WebhookRoutes))
		}
		mux.HandleFunc("/admin/settings/export", settingsHandler.HandleExportConfig)
		mux.HandleFunc("/admin/settings/import", settingsHandler.HandleImportConfig)
		mux.HandleFunc("/admin/settings/test-icinga", settingsHandler.HandleTestIcinga)
		mux.HandleFunc("/admin/settings/targets/", func(w http.ResponseWriter, r *http.Request) {
			// Route /admin/settings/targets/{id}/generate-key vs DELETE /admin/settings/targets/{id}
			if strings.HasSuffix(r.URL.Path, "/generate-key") {
				settingsHandler.HandleGenerateKey(w, r)
			} else if strings.HasSuffix(r.URL.Path, "/reveal-keys") {
				settingsHandler.HandleRevealKeys(w, r)
			} else if r.Method == http.MethodDelete {
				settingsHandler.HandleDeleteTarget(w, r)
			} else {
				http.NotFound(w, r)
			}
		})
		mux.HandleFunc("/admin/settings/targets", settingsHandler.HandleAddTarget)
		mux.HandleFunc("/admin/settings", func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				settingsHandler.HandleGetSettings(w, r)
			case http.MethodPatch:
				settingsHandler.HandlePatchSettings(w, r)
			default:
				httputil.WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			}
		})
	}

	// ── Start Server ────────────────────────────────────────────────
	slog.Info("Routes registered",
		"endpoints", []string{
			"GET  /health",
			"POST /webhook",
			"GET  /status/beauty/events",
			"GET  /status/beauty",
			"GET  /status/{service_name}",
			"GET  /history",
			"GET  /history/export",
			"GET  /admin/services",
			"DELETE /admin/services/{name}",
			"POST /admin/services/{name}/status",
			"POST /admin/services/bulk-delete",
			"GET  /admin/ratelimit",
			"POST /admin/history/clear",
			"GET  /admin/debug/toggle",
			"POST /admin/debug/toggle",
		},
	)

	if cfg.AdminPass == "" {
		slog.Warn("ADMIN_PASS not set — admin endpoints and dashboard management will be disabled")
	}

	// Security headers middleware
	secureHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:;")
		mux.ServeHTTP(w, r)
	})

	server := &http.Server{
		Addr:              cfg.ListenAddr(),
		Handler:           secureHandler,
		ReadTimeout:       15 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20, // 1 MB
	}

	// ── Graceful Shutdown ───────────────────────────────────────────
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ListenAndServe()
	}()

	slog.Info("Server started", "addr", cfg.ListenAddr())

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	select {
	case sig := <-stop:
		slog.Info("Received shutdown signal", "signal", sig)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		// Stop accepting new connections, drain in-flight requests
		if err := server.Shutdown(ctx); err != nil {
			slog.Error("Server shutdown error", "error", err)
		}

		// Drain retry queue before shutdown
		if retryQueue != nil {
			retryQueue.Drain()
		}

		// Stop history maintenance
		historyLogger.Shutdown()
		mainCancel()

		slog.Info("Server stopped gracefully")

	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			slog.Error("Server failed", "error", err)
			historyLogger.Shutdown()
			os.Exit(1)
		}
	}
}

func restoreManagedServicesFromIcinga(apiClient *icinga.APIClient, serviceCache *cache.ServiceCache, host string) {
	services, err := apiClient.ListServices(host)
	if err != nil {
		slog.Warn("Could not scan services in Icinga2 on startup",
			"host", host, "error", err)
		return
	}

	managedCount := 0
	legacyCount := 0
	legacySamples := make([]string, 0, 10)

	for _, svc := range services {
		if !svc.IsManagedByUs() {
			continue
		}
		serviceCache.Register(host, svc.Name)
		managedCount++

		if svc.IsLegacyManagedByUs() {
			legacyCount++
			if len(legacySamples) < cap(legacySamples) {
				legacySamples = append(legacySamples, svc.Name)
			}
		}
	}

	if managedCount > 0 {
		slog.Info("Restored managed services into cache from Icinga2",
			"host", host,
			"count", managedCount,
			"legacy_count", legacyCount)
	}

	if legacyCount > 0 {
		slog.Warn("Legacy webhook-bridge service artifacts found on startup",
			"host", host,
			"count", legacyCount,
			"sample", legacySamples,
			"expected_managed_by", icinga.ManagedByIAF)
	}
}

func ensureConfiguredHosts(apiClient *icinga.APIClient, targets map[string]config.TargetConfig, autoCreate bool) error {
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error

	// ⚡ Bolt: Verify host configurations concurrently.
	// Impact: Prevents N+1 API call bottleneck, reducing startup latency.
	for _, target := range sortedTargets(targets) {
		wg.Add(1)
		go func(t config.TargetConfig) {
			defer wg.Done()
			hostInfo, err := apiClient.GetHostInfo(t.HostName)
			if err != nil {
				slog.Warn("Could not verify host in Icinga2 (will retry on requests)",
					"host", t.HostName, "error", err)
				return
			}

			if !hostInfo.Exists {
				if !autoCreate {
					mu.Lock()
					if firstErr == nil {
						firstErr = fmt.Errorf("host %s does not exist in Icinga2 — set ICINGA2_HOST_AUTO_CREATE=true to create it automatically, or create it manually", t.HostName)
					}
					mu.Unlock()
					return
				}

				slog.Info("Host not found in Icinga2, creating dummy host...",
					"target_id", t.ID,
					"host", t.HostName)
				if err := apiClient.CreateHost(toIcingaHostSpec(t)); err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = fmt.Errorf("create host %s: %w", t.HostName, err)
					}
					mu.Unlock()
					return
				}
				slog.Info("Dummy host created in Icinga2",
					"target_id", t.ID,
					"host", t.HostName,
					"address", t.HostAddress,
					"managed_by", icinga.ManagedByIAF)
				return
			}

			if hostInfo.IsManagedByUs() {
				slog.Info("Host validated in Icinga2 (managed by us)",
					"target_id", t.ID,
					"host", t.HostName,
					"check_command", hostInfo.CheckCommand,
					"managed_by", hostInfo.ManagedBy)
				if hostInfo.IsLegacyManagedByUs() {
					slog.Warn("Host still uses legacy managed_by marker",
						"target_id", t.ID,
						"host", t.HostName,
						"managed_by", hostInfo.ManagedBy,
						"expected", icinga.ManagedByIAF)
				}
				return
			}

			if hostInfo.IsDummy() {
				slog.Info("Host validated in Icinga2 (dummy, not managed by us)",
					"target_id", t.ID,
					"host", t.HostName,
					"display_name", hostInfo.DisplayName)
				return
			}

			slog.Warn("CONFLICT: Host exists but is NOT a dummy host — it may be managed by Director or manual config. "+
				"Services created by IcingaAlertingForge may conflict with existing configuration!",
				"target_id", t.ID,
				"host", t.HostName,
				"check_command", hostInfo.CheckCommand,
				"display_name", hostInfo.DisplayName,
				"managed_by", hostInfo.ManagedBy)
		}(target)
	}

	wg.Wait()
	return firstErr
}

func toIcingaHostSpec(target config.TargetConfig) icinga.HostSpec {
	return icinga.HostSpec{
		Name:        target.HostName,
		DisplayName: target.HostDisplay,
		Address:     target.HostAddress,
		Notification: icinga.HostNotificationConfig{
			Users:         target.Notification.Users,
			Groups:        target.Notification.Groups,
			ServiceStates: target.Notification.ServiceStates,
			HostStates:    target.Notification.HostStates,
		},
	}
}

func sortedTargets(targets map[string]config.TargetConfig) []config.TargetConfig {
	list := make([]config.TargetConfig, 0, len(targets))
	for _, target := range targets {
		list = append(list, target)
	}

	sort.Slice(list, func(i, j int) bool {
		if list[i].HostName == list[j].HostName {
			return list[i].ID < list[j].ID
		}
		return list[i].HostName < list[j].HostName
	})

	return list
}

// icingaHealthAdapter adapts icinga.APIClient to the health.IcingaProber interface.
type icingaHealthAdapter struct {
	api *icinga.APIClient
}

func (a *icingaHealthAdapter) GetHostInfo(host string) (health.HostResult, error) {
	info, err := a.api.GetHostInfo(host)
	if err != nil {
		return health.HostResult{}, err
	}
	return health.HostResult{Exists: info.Exists}, nil
}

func (a *icingaHealthAdapter) SendCheckResult(host, service string, exitStatus int, message string) error {
	return a.api.SendCheckResult(host, service, exitStatus, message)
}

func (a *icingaHealthAdapter) CreateService(host, name string, labels, annotations map[string]string) error {
	return a.api.CreateService(host, name, labels, annotations)
}

// setupLogging configures the global slog logger based on config.
func setupLogging(level, format string) {
	var logLevel slog.Level
	switch strings.ToLower(level) {
	case "debug":
		logLevel = slog.LevelDebug
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	default:
		logLevel = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: logLevel}

	var logHandler slog.Handler
	if strings.ToLower(format) == "text" {
		logHandler = slog.NewTextHandler(os.Stdout, opts)
	} else {
		logHandler = slog.NewJSONHandler(os.Stdout, opts)
	}

	slog.SetDefault(slog.New(logHandler))
}

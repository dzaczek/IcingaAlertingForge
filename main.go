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
	"syscall"
	"time"

	"icinga-webhook-bridge/auth"
	"icinga-webhook-bridge/cache"
	"icinga-webhook-bridge/config"
	"icinga-webhook-bridge/configstore"
	"icinga-webhook-bridge/handler"
	"icinga-webhook-bridge/history"
	"icinga-webhook-bridge/icinga"
	"icinga-webhook-bridge/metrics"
	"icinga-webhook-bridge/queue"
)

// version is set at build time via -ldflags "-X main.version=..."
// Falls back to "dev" when built without ldflags.
var version = "dev"

func main() {
	// ── Load Configuration ──────────────────────────────────────────
	cfg := config.Load()

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
			// Override runtime config from store (keep server port/host from env)
			storedCfg := cfgStore.ToConfig(cfg.ServerPort, cfg.ServerHost)
			storedCfg.ConfigInDashboard = true
			storedCfg.ConfigEncryptionKey = cfg.ConfigEncryptionKey
			storedCfg.ConfigFilePath = cfg.ConfigFilePath
			// Preserve retry queue settings (not stored in dashboard config)
			storedCfg.RetryQueueEnabled = cfg.RetryQueueEnabled
			storedCfg.RetryQueueMaxSize = cfg.RetryQueueMaxSize
			storedCfg.RetryQueueFilePath = cfg.RetryQueueFilePath
			storedCfg.RetryQueueRetryBaseSec = cfg.RetryQueueRetryBaseSec
			storedCfg.RetryQueueRetryMaxSec = cfg.RetryQueueRetryMaxSec
			storedCfg.RetryQueueCheckInterval = cfg.RetryQueueCheckInterval
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
	debugRing := icinga.NewDebugRing()
	apiClient.Debug = debugRing

	// ── Validate / auto-create hosts in Icinga2 ─────────────────────
	if err := ensureConfiguredHosts(apiClient, cfg.Targets, cfg.Icinga2HostAutoCreate); err != nil {
		slog.Error("Failed to prepare target hosts", "error", err)
		os.Exit(1)
	}

	serviceCache := cache.NewServiceCache(cfg.CacheTTLMinutes)
	for _, target := range sortedTargets(cfg.Targets) {
		restoreManagedServicesFromIcinga(apiClient, serviceCache, target.HostName)
	}

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
		KeyStore:  keyStore,
		Cache:     serviceCache,
		API:       apiClient,
		History:   historyLogger,
		Targets:   cfg.Targets,
		Limiter:   rateLimiter,
		Metrics:   metricsCollector,
		SSE:        sseBroker,
		DebugRing:  debugRing,
		RetryQueue: retryQueue,
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
		Targets:           cfg.Targets,
		AdminUser:         cfg.AdminUser,
		AdminPass:         cfg.AdminPass,
		Version:           version,
		StartedAt:         startedAt,
		DebugRing:         debugRing,
		ConfigInDashboard: cfg.ConfigInDashboard,
		RetryQueue:        retryQueue,
	}

	adminHandler := &handler.AdminHandler{
		Cache:     serviceCache,
		API:       apiClient,
		Limiter:   rateLimiter,
		History:   historyLogger,
		Metrics:   metricsCollector,
		DebugRing: debugRing,
		Targets:    cfg.Targets,
		User:       cfg.AdminUser,
		Pass:       cfg.AdminPass,
		RetryQueue: retryQueue,
	}

	// ── Auth Middleware ─────────────────────────────────────────────
	requireAuth := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if cfg.AdminPass == "" {
				http.Error(w, `{"error":"admin access not configured"}`, http.StatusForbidden)
				return
			}
			user, pass, ok := r.BasicAuth()
			if !ok ||
				subtle.ConstantTimeCompare([]byte(user), []byte(cfg.AdminUser)) != 1 ||
				subtle.ConstantTimeCompare([]byte(pass), []byte(cfg.AdminPass)) != 1 {
				if metricsCollector != nil && user != "" {
					metricsCollector.RecordAuthFailure(r.RemoteAddr, user)
				}
				w.Header().Set("WWW-Authenticate", `Basic realm="Admin"`)
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			next(w, r)
		}
	}

	// ── Register Routes ─────────────────────────────────────────────
	mux := http.NewServeMux()

	// Health check
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"ok","version":"%s"}`, version)
	})

	// Core endpoints
	mux.Handle("/webhook", webhookHandler)
	mux.Handle("/status/beauty/events", sseBroker)
	mux.Handle("/status/beauty", dashboardHandler)
	mux.HandleFunc("/status/beauty/logout", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Basic realm="Dashboard Admin"`)
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
	mux.HandleFunc("/admin/services/bulk-delete", adminHandler.HandleBulkDelete)
	mux.HandleFunc("/admin/services/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/status") {
			adminHandler.HandleSetServiceStatus(w, r)
		} else {
			adminHandler.HandleDeleteService(w, r)
		}
	})
	mux.HandleFunc("/admin/services", adminHandler.HandleListServices)
	mux.HandleFunc("/admin/ratelimit", adminHandler.HandleRateLimitStats)
	mux.HandleFunc("/admin/queue", adminHandler.HandleQueueStats)
	mux.HandleFunc("/admin/queue/flush", adminHandler.HandleQueueFlush)
	mux.HandleFunc("/admin/history/clear", adminHandler.HandleClearHistory)
	mux.HandleFunc("/admin/debug/toggle", adminHandler.HandleDebugToggle)

	// Settings endpoints (only when CONFIG_IN_DASHBOARD=true)
	if cfg.ConfigInDashboard && cfgStore != nil {
		settingsHandler := &handler.SettingsHandler{
			Store:   cfgStore,
			User:    cfg.AdminUser,
			Pass:    cfg.AdminPass,
			Metrics: metricsCollector,
		}
		settingsHandler.OnReload = func(newCfg *config.Config) {
			slog.Info("Hot-reloading configuration from dashboard store")

			newKeyStore := auth.NewKeyStore(newCfg.WebhookRoutes)
			webhookHandler.KeyStore = newKeyStore
			webhookHandler.Targets = newCfg.Targets

			apiClient.UpdateCredentials(newCfg.Icinga2Host, newCfg.Icinga2User, newCfg.Icinga2Pass, newCfg.Icinga2TLSSkipVerify)

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
				http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
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
	for _, target := range sortedTargets(targets) {
		hostInfo, err := apiClient.GetHostInfo(target.HostName)
		if err != nil {
			slog.Warn("Could not verify host in Icinga2 (will retry on requests)",
				"host", target.HostName, "error", err)
			continue
		}

		if !hostInfo.Exists {
			if !autoCreate {
				return fmt.Errorf("host %s does not exist in Icinga2 — set ICINGA2_HOST_AUTO_CREATE=true to create it automatically, or create it manually", target.HostName)
			}

			slog.Info("Host not found in Icinga2, creating dummy host...",
				"target_id", target.ID,
				"host", target.HostName)
			if err := apiClient.CreateHost(toIcingaHostSpec(target)); err != nil {
				return fmt.Errorf("create host %s: %w", target.HostName, err)
			}
			slog.Info("Dummy host created in Icinga2",
				"target_id", target.ID,
				"host", target.HostName,
				"address", target.HostAddress,
				"managed_by", icinga.ManagedByIAF)
			continue
		}

		if hostInfo.IsManagedByUs() {
			slog.Info("Host validated in Icinga2 (managed by us)",
				"target_id", target.ID,
				"host", target.HostName,
				"check_command", hostInfo.CheckCommand,
				"managed_by", hostInfo.ManagedBy)
			if hostInfo.IsLegacyManagedByUs() {
				slog.Warn("Host still uses legacy managed_by marker",
					"target_id", target.ID,
					"host", target.HostName,
					"managed_by", hostInfo.ManagedBy,
					"expected", icinga.ManagedByIAF)
			}
			continue
		}

		if hostInfo.IsDummy() {
			slog.Info("Host validated in Icinga2 (dummy, not managed by us)",
				"target_id", target.ID,
				"host", target.HostName,
				"display_name", hostInfo.DisplayName)
			continue
		}

		slog.Warn("CONFLICT: Host exists but is NOT a dummy host — it may be managed by Director or manual config. "+
			"Services created by IcingaAlertingForge may conflict with existing configuration!",
			"target_id", target.ID,
			"host", target.HostName,
			"check_command", hostInfo.CheckCommand,
			"display_name", hostInfo.DisplayName,
			"managed_by", hostInfo.ManagedBy)
	}

	return nil
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

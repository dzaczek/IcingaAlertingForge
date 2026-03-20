package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"icinga-webhook-bridge/auth"
	"icinga-webhook-bridge/cache"
	"icinga-webhook-bridge/config"
	"icinga-webhook-bridge/handler"
	"icinga-webhook-bridge/history"
	"icinga-webhook-bridge/icinga"
	"icinga-webhook-bridge/metrics"
)

const version = "1.0.0"

func main() {
	// ── Load Configuration ──────────────────────────────────────────
	cfg := config.Load()

	// ── Setup Structured Logging ────────────────────────────────────
	setupLogging(cfg.LogLevel, cfg.LogFormat)

	slog.Info("Starting Webhook Bridge",
		"version", version,
		"listen", cfg.ListenAddr(),
	)

	// ── Initialize Components ───────────────────────────────────────
	keyStore := auth.NewKeyStore(cfg.WebhookKeys)

	apiClient := icinga.NewAPIClient(
		cfg.Icinga2Host,
		cfg.Icinga2User,
		cfg.Icinga2Pass,
		cfg.Icinga2TLSSkipVerify,
	)

	// ── Validate / auto-create host in Icinga2 ──────────────────────
	hostExists := false
	hostInfo, err := apiClient.GetHostInfo(cfg.Icinga2HostName)
	if err != nil {
		slog.Warn("Could not verify host in Icinga2 (will retry on requests)",
			"host", cfg.Icinga2HostName, "error", err)
	} else if !hostInfo.Exists {
		if cfg.Icinga2HostAutoCreate {
			slog.Info("Host not found in Icinga2, creating dummy host...",
				"host", cfg.Icinga2HostName)
			if err := apiClient.CreateHost(
				cfg.Icinga2HostName,
				cfg.Icinga2HostDisplay,
				cfg.Icinga2HostAddress,
			); err != nil {
				slog.Error("Failed to create host in Icinga2",
					"host", cfg.Icinga2HostName, "error", err)
				os.Exit(1)
			}
			hostExists = true
			slog.Info("Dummy host created in Icinga2",
				"host", cfg.Icinga2HostName,
				"address", cfg.Icinga2HostAddress,
				"managed_by", "webhook-bridge")
		} else {
			slog.Error("Host does not exist in Icinga2 — set ICINGA2_HOST_AUTO_CREATE=true to create it automatically, or create it manually",
				"host", cfg.Icinga2HostName)
			os.Exit(1)
		}
	} else {
		// Host exists — check for conflicts
		hostExists = true
		if hostInfo.IsManagedByUs() {
			slog.Info("Host validated in Icinga2 (managed by webhook-bridge)",
				"host", cfg.Icinga2HostName,
				"check_command", hostInfo.CheckCommand)
		} else if hostInfo.IsDummy() {
			slog.Info("Host validated in Icinga2 (dummy, not managed by us)",
				"host", cfg.Icinga2HostName,
				"display_name", hostInfo.DisplayName)
		} else {
			slog.Warn("CONFLICT: Host exists but is NOT a dummy host — it may be managed by Director or manual config. "+
				"Services created by webhook-bridge may conflict with existing configuration!",
				"host", cfg.Icinga2HostName,
				"check_command", hostInfo.CheckCommand,
				"display_name", hostInfo.DisplayName,
				"managed_by", hostInfo.ManagedBy)
		}
	}

	serviceCache := cache.NewServiceCache(cfg.CacheTTLMinutes)

	historyLogger, err := history.NewLogger(cfg.HistoryFile, cfg.HistoryMaxEntries)
	if err != nil {
		slog.Error("Failed to initialize history logger", "error", err)
		os.Exit(1)
	}

	// Start history maintenance goroutine (rotation checks)
	mainCtx, mainCancel := context.WithCancel(context.Background())
	defer mainCancel()
	historyLogger.StartMaintenance(mainCtx)

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

	// ── Create Handlers ─────────────────────────────────────────────
	webhookHandler := &handler.WebhookHandler{
		KeyStore:   keyStore,
		Cache:      serviceCache,
		API:        apiClient,
		History:    historyLogger,
		HostName:   cfg.Icinga2HostName,
		Limiter:    rateLimiter,
		Metrics:    metricsCollector,
		HostExists: hostExists,
	}

	statusHandler := &handler.StatusHandler{
		Cache:    serviceCache,
		API:      apiClient,
		HostName: cfg.Icinga2HostName,
	}

	historyHandler := history.NewHandler(historyLogger)

	startedAt := time.Now()

	dashboardHandler := &handler.DashboardHandler{
		Cache:     serviceCache,
		History:   historyLogger,
		API:       apiClient,
		Metrics:   metricsCollector,
		HostName:  cfg.Icinga2HostName,
		AdminUser: cfg.AdminUser,
		AdminPass: cfg.AdminPass,
		StartedAt: startedAt,
	}

	adminHandler := &handler.AdminHandler{
		Cache:    serviceCache,
		API:      apiClient,
		Limiter:  rateLimiter,
		HostName: cfg.Icinga2HostName,
		User:     cfg.AdminUser,
		Pass:     cfg.AdminPass,
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
	mux.Handle("/status/beauty", dashboardHandler)
	mux.HandleFunc("/status/beauty/logout", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Basic realm="Dashboard Admin"`)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `<html><head><meta http-equiv="refresh" content="1;url=/status/beauty"></head><body>Logged out. Redirecting...</body></html>`)
	})
	mux.Handle("/status/", statusHandler)

	// History endpoints
	mux.HandleFunc("/history", historyHandler.HandleHistory)
	mux.HandleFunc("/history/export", historyHandler.HandleExport)

	// Admin endpoints (password protected)
	mux.HandleFunc("/admin/services/bulk-delete", adminHandler.HandleBulkDelete)
	mux.HandleFunc("/admin/services/", adminHandler.HandleDeleteService)
	mux.HandleFunc("/admin/services", adminHandler.HandleListServices)
	mux.HandleFunc("/admin/ratelimit", adminHandler.HandleRateLimitStats)

	// ── Start Server ────────────────────────────────────────────────
	slog.Info("Routes registered",
		"endpoints", []string{
			"GET  /health",
			"POST /webhook",
			"GET  /status/beauty",
			"GET  /status/{service_name}",
			"GET  /history",
			"GET  /history/export",
			"GET  /admin/services",
			"DELETE /admin/services/{name}",
			"POST /admin/services/bulk-delete",
			"GET  /admin/ratelimit",
		},
	)

	if cfg.AdminPass == "" {
		slog.Warn("ADMIN_PASS not set — admin endpoints and dashboard management will be disabled")
	}

	server := &http.Server{
		Addr:              cfg.ListenAddr(),
		Handler:           mux,
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

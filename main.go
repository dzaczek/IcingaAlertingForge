package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"icinga-webhook-bridge/auth"
	"icinga-webhook-bridge/cache"
	"icinga-webhook-bridge/config"
	"icinga-webhook-bridge/handler"
	"icinga-webhook-bridge/history"
	"icinga-webhook-bridge/icinga"
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

	serviceCache := cache.NewServiceCache(cfg.CacheTTLMinutes)

	historyLogger, err := history.NewLogger(cfg.HistoryFile, cfg.HistoryMaxEntries)
	if err != nil {
		slog.Error("Failed to initialize history logger", "error", err)
		os.Exit(1)
	}

	// ── Create Handlers ─────────────────────────────────────────────
	webhookHandler := &handler.WebhookHandler{
		KeyStore: keyStore,
		Cache:    serviceCache,
		API:      apiClient,
		History:  historyLogger,
		HostName: cfg.Icinga2HostName,
	}

	statusHandler := &handler.StatusHandler{
		Cache:    serviceCache,
		API:      apiClient,
		HostName: cfg.Icinga2HostName,
	}

	historyHandler := history.NewHandler(historyLogger)

	dashboardHandler := &handler.DashboardHandler{
		Cache:     serviceCache,
		History:   historyLogger,
		StartedAt: time.Now(),
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
	mux.Handle("/status/", statusHandler)

	// History endpoints
	mux.HandleFunc("/history", historyHandler.HandleHistory)
	mux.HandleFunc("/history/export", historyHandler.HandleExport)

	// ── Start Server ────────────────────────────────────────────────
	slog.Info("Routes registered",
		"endpoints", []string{
			"GET  /health",
			"POST /webhook",
			"GET  /status/beauty",
			"GET  /status/{service_name}",
			"GET  /history",
			"GET  /history/export",
		},
	)

	server := &http.Server{
		Addr:         cfg.ListenAddr(),
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	if err := server.ListenAndServe(); err != nil {
		slog.Error("Server failed", "error", err)
		os.Exit(1)
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

package handler

import (
	"net/http"
	"strings"
	"time"

	"icinga-webhook-bridge/cache"
	"icinga-webhook-bridge/icinga"
)

// StatusHandler serves GET /status/{service_name} to check service state.
type StatusHandler struct {
	Cache    *cache.ServiceCache
	API      *icinga.APIClient
	HostName string
}

// ServeHTTP handles GET /status/{service_name}.
func (h *StatusHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	// Extract service name from path: /status/{service_name}
	serviceName := strings.TrimPrefix(r.URL.Path, "/status/")
	if serviceName == "" || serviceName == "beauty" {
		// /status/beauty is handled by the dashboard handler
		return
	}

	cacheState := h.Cache.GetState(serviceName)

	response := map[string]any{
		"service":     serviceName,
		"cache_state": string(cacheState),
	}

	// Try to get current status from Icinga2
	exitStatus, output, checkTime, err := h.API.GetServiceStatus(h.HostName, serviceName)
	if err == nil {
		response["exists_in_icinga"] = true
		response["last_check_result"] = map[string]any{
			"exit_status": exitStatus,
			"output":      output,
			"timestamp":   checkTime.Format(time.RFC3339),
		}
	} else {
		response["exists_in_icinga"] = false
	}

	writeJSON(w, http.StatusOK, response)
}

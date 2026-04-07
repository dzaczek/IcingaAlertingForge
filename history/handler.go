package history

import (
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"time"

	"icinga-webhook-bridge/models"
)

const maxHistoryLimit = 10000

// writeJSON writes a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, statusCode int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		// Just log error; response is already sent
		// slog.Debug("writeJSON encode error", "error", err)
	}
}

// Handler provides HTTP handlers for querying and exporting webhook history.
type Handler struct {
	logger *Logger
}

// NewHandler creates a new history Handler.
func NewHandler(logger *Logger) *Handler {
	return &Handler{logger: logger}
}

// HandleHistory serves GET /history with optional query filters.
func (h *Handler) HandleHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	q := r.URL.Query()

	limit := 100
	if l := q.Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
			if limit > maxHistoryLimit {
				limit = maxHistoryLimit
			}
		}
	}

	filter := QueryFilter{
		Limit:   limit,
		Service: q.Get("service"),
		Source:  q.Get("source"),
		Host:    q.Get("host"),
		Mode:    q.Get("mode"),
	}

	if from := q.Get("from"); from != "" {
		if t, err := time.Parse("2006-01-02", from); err == nil {
			filter.From = t
		} else if t, err := time.Parse(time.RFC3339, from); err == nil {
			filter.From = t
		}
	}

	if to := q.Get("to"); to != "" {
		if t, err := time.Parse("2006-01-02", to); err == nil {
			filter.To = t.Add(24*time.Hour - time.Nanosecond)
		} else if t, err := time.Parse(time.RFC3339, to); err == nil {
			filter.To = t
		}
	}

	entries, err := h.logger.Query(filter)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to query history"})
		return
	}

	if entries == nil {
		entries = make([]models.HistoryEntry, 0)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"entries": entries,
		"count":   len(entries),
		"filters": map[string]any{
			"limit":   limit,
			"service": filter.Service,
			"source":  filter.Source,
			"host":    filter.Host,
			"mode":    filter.Mode,
		},
	})
}

// HandleExport serves GET /history/export — streams the raw JSONL file.
func (h *Handler) HandleExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	filePath := h.logger.FilePath()
	f, err := os.Open(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			w.Header().Set("Content-Type", "application/x-ndjson")
			w.WriteHeader(http.StatusOK)
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to open history file"})
		return
	}
	defer f.Close()

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Content-Disposition", "attachment; filename=webhook-history.jsonl")
	http.ServeContent(w, r, "webhook-history.jsonl", time.Now(), f)
}

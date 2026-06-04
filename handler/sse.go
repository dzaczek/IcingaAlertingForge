package handler

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
)

const (
	sseMaxClients    = 50
	sseChannelBuffer = 10
)

// SSEEvent represents a webhook event broadcast to SSE clients.
type SSEEvent struct {
	Status      string `json:"status"`
	ServiceName string `json:"service"`
	Source      string `json:"source,omitempty"`
	Mode        string `json:"mode,omitempty"`
	RemoteAddr  string `json:"remote_addr,omitempty"`
	Action      string `json:"action,omitempty"`
	Severity    string `json:"severity,omitempty"`
	ExitStatus  int    `json:"exit_status,omitempty"`
	rawMessage  string // if set, sent verbatim (for named events like "debug")
}

// SSEBroker manages SSE client connections and broadcasts webhook events.
type SSEBroker struct {
	mu      sync.RWMutex
	clients map[chan SSEEvent]struct{}
}

// NewSSEBroker creates a new SSE broker.
func NewSSEBroker() *SSEBroker {
	return &SSEBroker{
		clients: make(map[chan SSEEvent]struct{}),
	}
}

// Subscribe registers a new SSE client and returns its event channel.
// Returns nil if the maximum number of clients is reached.
func (b *SSEBroker) Subscribe() chan SSEEvent {
	b.mu.Lock()
	defer b.mu.Unlock()

	if len(b.clients) >= sseMaxClients {
		return nil
	}

	ch := make(chan SSEEvent, sseChannelBuffer)
	b.clients[ch] = struct{}{}
	return ch
}

// Unsubscribe removes a client and closes its channel.
func (b *SSEBroker) Unsubscribe(ch chan SSEEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if _, ok := b.clients[ch]; ok {
		delete(b.clients, ch)
		close(ch)
	}
}

// Publish sends an event to all connected clients (non-blocking).
func (b *SSEBroker) Publish(event SSEEvent) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	for ch := range b.clients {
		select {
		case ch <- event:
		default:
			// Skip slow clients
		}
	}
}

// PublishRaw sends a raw named SSE event (e.g. "debug") to all clients.
func (b *SSEBroker) PublishRaw(eventType string, data []byte) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	// ⚡ Bolt: Fast-path string building instead of fmt.Sprintf to reduce allocation overhead
	var builder strings.Builder
	builder.Grow(15 + len(eventType) + len(data))
	builder.WriteString("event: ")
	builder.WriteString(eventType)
	builder.WriteString("\ndata: ")
	builder.Write(data)
	builder.WriteString("\n\n")
	msg := builder.String()

	for ch := range b.clients {
		select {
		case ch <- SSEEvent{rawMessage: msg}:
		default:
		}
	}
}

// ServeHTTP handles SSE connections at /status/beauty/events.
func (b *SSEBroker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	ch := b.Subscribe()
	if ch == nil {
		http.Error(w, "too many SSE clients", http.StatusServiceUnavailable)
		return
	}
	defer b.Unsubscribe(ch)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher.Flush()

	ctx := r.Context()

	slog.Debug("SSE client connected", "remote_addr", r.RemoteAddr)

	for {
		select {
		case <-ctx.Done():
			slog.Debug("SSE client disconnected", "remote_addr", r.RemoteAddr)
			return
		case event, ok := <-ch:
			if !ok {
				return
			}
			if event.rawMessage != "" {
				var buf bytes.Buffer
				json.HTMLEscape(&buf, []byte(event.rawMessage))
				_, _ = w.Write(buf.Bytes())
			} else {
				data, err := json.Marshal(event)
				if err != nil {
					continue
				}
				_, _ = w.Write([]byte("data: " + string(data) + "\n\n"))
			}
			flusher.Flush()
		}
	}
}

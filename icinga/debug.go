package icinga

import (
	"sync"
	"sync/atomic"
	"time"
)

const debugRingSize = 100

// DebugEntry captures a single API request/response pair.
type DebugEntry struct {
	Timestamp    time.Time `json:"timestamp"`
	Direction    string    `json:"direction"`               // "inbound" (Grafana->IAF) or "outbound" (IAF->Icinga2)
	Method       string    `json:"method"`
	URL          string    `json:"url"`
	RequestBody  string    `json:"request_body,omitempty"`
	StatusCode   int       `json:"status_code"`
	ResponseBody string    `json:"response_body,omitempty"`
	DurationMs   int64     `json:"duration_ms"`
	Error        string    `json:"error,omitempty"`
	Source       string    `json:"source,omitempty"`         // webhook source key (inbound only)
	RemoteAddr   string    `json:"remote_addr,omitempty"`    // client IP (inbound only)
}

// DebugRing is a thread-safe ring buffer that stores recent API interactions.
type DebugRing struct {
	mu       sync.Mutex
	entries  []DebugEntry
	pos      int
	count    int
	enabled  atomic.Bool        // collection only happens when enabled
	listener func(DebugEntry)   // optional real-time callback
}

// NewDebugRing creates a new ring buffer for API debug entries.
func NewDebugRing() *DebugRing {
	return &DebugRing{
		entries: make([]DebugEntry, debugRingSize),
	}
}

// SetListener registers a callback invoked on each new entry (for SSE).
func (r *DebugRing) SetListener(fn func(DebugEntry)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.listener = fn
}

// SetEnabled enables or disables collection.
func (r *DebugRing) SetEnabled(on bool) {
	r.enabled.Store(on)
}

// Enabled returns whether collection is active.
func (r *DebugRing) Enabled() bool {
	return r.enabled.Load()
}

// Push adds an entry to the ring buffer (only if enabled).
func (r *DebugRing) Push(e DebugEntry) {
	if !r.enabled.Load() {
		return
	}
	r.mu.Lock()
	r.entries[r.pos] = e
	r.pos = (r.pos + 1) % debugRingSize
	if r.count < debugRingSize {
		r.count++
	}
	fn := r.listener
	r.mu.Unlock()

	if fn != nil {
		fn(e)
	}
}

// Recent returns the last n entries, newest first.
func (r *DebugRing) Recent(n int) []DebugEntry {
	r.mu.Lock()
	defer r.mu.Unlock()

	if n > r.count {
		n = r.count
	}
	result := make([]DebugEntry, n)
	for i := 0; i < n; i++ {
		idx := (r.pos - 1 - i + debugRingSize) % debugRingSize
		result[i] = r.entries[idx]
	}
	return result
}

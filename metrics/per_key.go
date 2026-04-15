package metrics

import (
	"sync"
	"sync/atomic"
	"time"
)

// PerKeyStats holds real-time counters for a specific API key source.
type PerKeyStats struct {
	Requests atomic.Int64
	Errors   atomic.Int64
	LastSeen atomic.Int64 // Unix nanoseconds
}

// PerKeyCollector tracks per-source statistics in real-time.
type PerKeyCollector struct {
	mu    sync.RWMutex
	stats map[string]*PerKeyStats
}

// NewPerKeyCollector creates a new per-key stats collector.
func NewPerKeyCollector() *PerKeyCollector {
	return &PerKeyCollector{
		stats: make(map[string]*PerKeyStats),
	}
}

// Record increments request/error counters for a source and updates its last seen timestamp.
func (p *PerKeyCollector) Record(source string, isError bool) {
	if source == "" {
		return
	}

	p.mu.RLock()
	stats, ok := p.stats[source]
	p.mu.RUnlock()

	if !ok {
		p.mu.Lock()
		// Double-check after acquiring write lock
		stats, ok = p.stats[source]
		if !ok {
			stats = &PerKeyStats{}
			p.stats[source] = stats
		}
		p.mu.Unlock()
	}

	stats.Requests.Add(1)
	if isError {
		stats.Errors.Add(1)
	}
	stats.LastSeen.Store(time.Now().UnixNano())
}

// GetStats returns a point-in-time copy of all tracked per-key stats.
func (p *PerKeyCollector) GetStats() map[string]*PerKeyStats {
	p.mu.RLock()
	defer p.mu.RUnlock()

	res := make(map[string]*PerKeyStats, len(p.stats))
	for k, v := range p.stats {
		res[k] = v
	}
	return res
}

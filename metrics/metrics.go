package metrics

import (
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// Collector gathers system and application metrics.
type Collector struct {
	startedAt time.Time

	// Request counters
	totalRequests  atomic.Int64
	totalErrors    atomic.Int64
	totalLatencyMs atomic.Int64 // sum of all request latencies for avg calculation

	// Auth security
	mu             sync.RWMutex
	failedAuths    []AuthFailure
	failedAuthTotal atomic.Int64
}

// AuthFailure records a single failed authentication attempt.
type AuthFailure struct {
	Timestamp time.Time `json:"timestamp"`
	IP        string    `json:"ip"`
	KeyPrefix string    `json:"key_prefix"` // first 8 chars of the key used
}

// SystemStats holds a snapshot of system metrics.
type SystemStats struct {
	// Runtime
	GoRoutines   int    `json:"goroutines"`
	MemAllocMB   float64 `json:"mem_alloc_mb"`
	MemSysMB     float64 `json:"mem_sys_mb"`
	MemHeapMB    float64 `json:"mem_heap_mb"`
	MemStackMB   float64 `json:"mem_stack_mb"`
	GCPauseTotalMs float64 `json:"gc_pause_total_ms"`
	GCRuns       uint32 `json:"gc_runs"`
	NumCPU       int    `json:"num_cpu"`

	// App metrics
	Uptime           string  `json:"uptime"`
	UptimeSeconds    float64 `json:"uptime_seconds"`
	TotalRequests    int64   `json:"total_requests"`
	TotalErrors      int64   `json:"total_errors"`
	ErrorRate        float64 `json:"error_rate_pct"`
	AvgLatencyMs     float64 `json:"avg_latency_ms"`
	RequestsPerMin   float64 `json:"requests_per_min"`

	// Security
	FailedAuthTotal  int64          `json:"failed_auth_total"`
	FailedAuthRecent []AuthFailure  `json:"failed_auth_recent"`
	BruteForceIPs    []BruteForceIP `json:"brute_force_ips"`
}

// BruteForceIP tracks IPs with multiple failed auth attempts.
type BruteForceIP struct {
	IP       string `json:"ip"`
	Attempts int    `json:"attempts"`
	LastSeen string `json:"last_seen"`
}

// NewCollector creates a new metrics collector.
func NewCollector() *Collector {
	return &Collector{
		startedAt: time.Now(),
	}
}

// RecordRequest records a successful request with its latency.
func (c *Collector) RecordRequest(latencyMs int64) {
	c.totalRequests.Add(1)
	c.totalLatencyMs.Add(latencyMs)
}

// RecordError records an error.
func (c *Collector) RecordError() {
	c.totalErrors.Add(1)
}

// RecordAuthFailure records a failed authentication attempt.
func (c *Collector) RecordAuthFailure(ip, keyUsed string) {
	c.failedAuthTotal.Add(1)

	prefix := keyUsed
	if len(prefix) > 8 {
		prefix = prefix[:8]
	}
	if prefix == "" {
		prefix = "(empty)"
	}

	failure := AuthFailure{
		Timestamp: time.Now(),
		IP:        ip,
		KeyPrefix: prefix + "...",
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.failedAuths = append(c.failedAuths, failure)

	// Keep only last 100 failures
	if len(c.failedAuths) > 100 {
		c.failedAuths = c.failedAuths[len(c.failedAuths)-100:]
	}
}

// Snapshot returns a point-in-time snapshot of all metrics.
func (c *Collector) Snapshot() SystemStats {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	uptime := time.Since(c.startedAt)
	totalReqs := c.totalRequests.Load()
	totalErrs := c.totalErrors.Load()
	totalLat := c.totalLatencyMs.Load()

	var errorRate float64
	if totalReqs > 0 {
		errorRate = float64(totalErrs) / float64(totalReqs) * 100
	}

	var avgLatency float64
	if totalReqs > 0 {
		avgLatency = float64(totalLat) / float64(totalReqs)
	}

	var reqPerMin float64
	if uptime.Minutes() > 0 {
		reqPerMin = float64(totalReqs) / uptime.Minutes()
	}

	// Build security info
	c.mu.RLock()
	recentFailures := make([]AuthFailure, 0)
	cutoff := time.Now().Add(-1 * time.Hour)
	ipCounts := make(map[string]*BruteForceIP)

	for _, f := range c.failedAuths {
		if f.Timestamp.After(cutoff) {
			recentFailures = append(recentFailures, f)
		}
		if bf, ok := ipCounts[f.IP]; ok {
			bf.Attempts++
			bf.LastSeen = f.Timestamp.Format("2006-01-02 15:04:05")
		} else {
			ipCounts[f.IP] = &BruteForceIP{
				IP:       f.IP,
				Attempts: 1,
				LastSeen: f.Timestamp.Format("2006-01-02 15:04:05"),
			}
		}
	}
	c.mu.RUnlock()

	// Only show last 20 recent failures
	if len(recentFailures) > 20 {
		recentFailures = recentFailures[len(recentFailures)-20:]
	}

	// Filter brute force IPs (3+ attempts)
	var bruteForce []BruteForceIP
	for _, bf := range ipCounts {
		if bf.Attempts >= 3 {
			bruteForce = append(bruteForce, *bf)
		}
	}

	return SystemStats{
		GoRoutines:     runtime.NumGoroutine(),
		MemAllocMB:     float64(mem.Alloc) / 1024 / 1024,
		MemSysMB:       float64(mem.Sys) / 1024 / 1024,
		MemHeapMB:      float64(mem.HeapAlloc) / 1024 / 1024,
		MemStackMB:     float64(mem.StackInuse) / 1024 / 1024,
		GCPauseTotalMs: float64(mem.PauseTotalNs) / 1e6,
		GCRuns:         mem.NumGC,
		NumCPU:         runtime.NumCPU(),

		Uptime:        uptime.Round(time.Second).String(),
		UptimeSeconds: uptime.Seconds(),
		TotalRequests: totalReqs,
		TotalErrors:   totalErrs,
		ErrorRate:     errorRate,
		AvgLatencyMs:  avgLatency,
		RequestsPerMin: reqPerMin,

		FailedAuthTotal:  c.failedAuthTotal.Load(),
		FailedAuthRecent: recentFailures,
		BruteForceIPs:    bruteForce,
	}
}

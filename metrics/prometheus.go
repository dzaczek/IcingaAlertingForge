package metrics

import (
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"icinga-webhook-bridge/health"
	"icinga-webhook-bridge/history"
	"icinga-webhook-bridge/icinga"
	"icinga-webhook-bridge/queue"
)

// PrometheusCollector implements the prometheus.Collector interface.
type PrometheusCollector struct {
	appMetrics  *Collector
	history     *history.Logger
	queue       *queue.Queue
	rateLimiter *icinga.RateLimiter
	health      *health.Checker
	perKey      *PerKeyCollector

	// Descriptors
	uptimeDesc          *prometheus.Desc
	goroutinesDesc      *prometheus.Desc
	memAllocDesc        *prometheus.Desc
	memSysDesc          *prometheus.Desc
	memHeapDesc         *prometheus.Desc
	memStackDesc        *prometheus.Desc
	gcPauseDesc         *prometheus.Desc
	gcRunsDesc          *prometheus.Desc
	requestsTotalDesc   *prometheus.Desc
	errorsTotalDesc     *prometheus.Desc
	requestsPerMinDesc  *prometheus.Desc
	authFailuresDesc    *prometheus.Desc
	bruteForceDesc      *prometheus.Desc
	historyEntriesDesc  *prometheus.Desc
	historyModeDesc     *prometheus.Desc
	historyActionDesc   *prometheus.Desc
	historySeverityDesc *prometheus.Desc
	historyFiringDesc   *prometheus.Desc
	historyErrorsDesc   *prometheus.Desc
	historyDurationDesc *prometheus.Desc
	sourceReqsDesc      *prometheus.Desc
	sourceErrsDesc      *prometheus.Desc
	sourceEntriesDesc   *prometheus.Desc
	sourceLastSeenDesc  *prometheus.Desc
	queueDepthDesc      *prometheus.Desc
	queueMaxSizeDesc    *prometheus.Desc
	queueRetriedDesc    *prometheus.Desc
	queueDroppedDesc    *prometheus.Desc
	queueFailedDesc     *prometheus.Desc
	queueProcessingDesc *prometheus.Desc
	rlSlotsInUseDesc    *prometheus.Desc
	rlSlotsMaxDesc      *prometheus.Desc
	rlQueueDepthDesc    *prometheus.Desc
	rlQueueMaxDesc      *prometheus.Desc
	healthIcingaUpDesc  *prometheus.Desc
	healthFailsDesc     *prometheus.Desc

	scrapeCount atomic.Int64
	lastScrape  atomic.Int64 // Unix timestamp
}

// NewPrometheusCollector creates a new PrometheusCollector.
func NewPrometheusCollector(appMetrics *Collector, history *history.Logger, q *queue.Queue, rl *icinga.RateLimiter, h *health.Checker, pk *PerKeyCollector) *PrometheusCollector {
	return &PrometheusCollector{
		appMetrics:  appMetrics,
		history:     history,
		queue:       q,
		rateLimiter: rl,
		health:      h,
		perKey:      pk,

		uptimeDesc:     prometheus.NewDesc("iaf_uptime_seconds", "Seconds since server start", nil, nil),
		goroutinesDesc: prometheus.NewDesc("iaf_goroutines", "Active goroutines", nil, nil),
		memAllocDesc:   prometheus.NewDesc("iaf_memory_alloc_bytes", "Heap bytes currently allocated", nil, nil),
		memSysDesc:     prometheus.NewDesc("iaf_memory_sys_bytes", "Total bytes obtained from OS", nil, nil),
		memHeapDesc:    prometheus.NewDesc("iaf_memory_heap_bytes", "Heap bytes in use", nil, nil),
		memStackDesc:   prometheus.NewDesc("iaf_memory_stack_bytes", "Stack bytes in use", nil, nil),
		gcPauseDesc:    prometheus.NewDesc("iaf_gc_pause_total_seconds", "Cumulative GC pause duration", nil, nil),
		gcRunsDesc:     prometheus.NewDesc("iaf_gc_runs_total", "Total GC cycles", nil, nil),

		requestsTotalDesc:  prometheus.NewDesc("iaf_requests_total", "Total webhook requests received", nil, nil),
		errorsTotalDesc:    prometheus.NewDesc("iaf_errors_total", "Total requests that resulted in an error", nil, nil),
		requestsPerMinDesc: prometheus.NewDesc("iaf_requests_per_minute", "Rolling requests-per-minute rate", nil, nil),

		authFailuresDesc: prometheus.NewDesc("iaf_auth_failures_total", "Cumulative failed authentication attempts", nil, nil),
		bruteForceDesc:   prometheus.NewDesc("iaf_brute_force_ips_active", "Number of IPs currently flagged for brute-force", nil, nil),

		historyEntriesDesc:  prometheus.NewDesc("iaf_history_entries_total", "Total number of entries in history log", nil, nil),
		historyModeDesc:     prometheus.NewDesc("iaf_history_by_mode", "Entry count per mode", []string{"mode"}, nil),
		historyActionDesc:   prometheus.NewDesc("iaf_history_by_action", "Entry count per action", []string{"action"}, nil),
		historySeverityDesc: prometheus.NewDesc("iaf_history_by_severity", "Entry count per severity level", []string{"severity"}, nil),
		historyFiringDesc:   prometheus.NewDesc("iaf_history_by_severity_firing", "Firing/create-only count per severity", []string{"severity"}, nil),
		historyErrorsDesc:   prometheus.NewDesc("iaf_history_errors_total", "Entries where Icinga API call failed", nil, nil),
		historyDurationDesc: prometheus.NewDesc("iaf_history_avg_duration_milliseconds", "Average Icinga API call duration", nil, nil),

		sourceReqsDesc:     prometheus.NewDesc("iaf_source_requests_total", "Total webhook requests per API key", []string{"source"}, nil),
		sourceErrsDesc:     prometheus.NewDesc("iaf_source_errors_total", "Failed Icinga calls per API key", []string{"source"}, nil),
		sourceEntriesDesc:  prometheus.NewDesc("iaf_source_history_entries", "History entry count per API key", []string{"source"}, nil),
		sourceLastSeenDesc: prometheus.NewDesc("iaf_source_last_seen_timestamp", "Unix timestamp of last webhook from this source", []string{"source"}, nil),

		queueDepthDesc:      prometheus.NewDesc("iaf_queue_depth", "Current number of items in retry queue", nil, nil),
		queueMaxSizeDesc:    prometheus.NewDesc("iaf_queue_max_size", "Configured maximum queue capacity", nil, nil),
		queueRetriedDesc:    prometheus.NewDesc("iaf_queue_retried_total", "Cumulative retry attempts", nil, nil),
		queueDroppedDesc:    prometheus.NewDesc("iaf_queue_dropped_total", "Items dropped due to full queue", nil, nil),
		queueFailedDesc:     prometheus.NewDesc("iaf_queue_failed_total", "Items permanently failed after all retries", nil, nil),
		queueProcessingDesc: prometheus.NewDesc("iaf_queue_processing", "1 if retry processor is active, 0 otherwise", nil, nil),

		rlSlotsInUseDesc: prometheus.NewDesc("iaf_ratelimiter_slots_in_use", "Currently occupied concurrency slots", []string{"type"}, nil),
		rlSlotsMaxDesc:   prometheus.NewDesc("iaf_ratelimiter_slots_max", "Configured maximum slots", []string{"type"}, nil),
		rlQueueDepthDesc: prometheus.NewDesc("iaf_ratelimiter_queue_depth", "Pending status operations", nil, nil),
		rlQueueMaxDesc:   prometheus.NewDesc("iaf_ratelimiter_queue_max", "Max queue depth before rejection", nil, nil),

		healthIcingaUpDesc: prometheus.NewDesc("iaf_health_icinga_up", "1 if Icinga2 is reachable, 0 otherwise", nil, nil),
		healthFailsDesc:    prometheus.NewDesc("iaf_health_consecutive_failures", "Number of consecutive failed health checks", nil, nil),
	}
}

// Describe sends the super-set of all possible descriptors of metrics
// collected by this Collector to the provided channel.
func (c *PrometheusCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.uptimeDesc
	ch <- c.goroutinesDesc
	ch <- c.memAllocDesc
	ch <- c.memSysDesc
	ch <- c.memHeapDesc
	ch <- c.memStackDesc
	ch <- c.gcPauseDesc
	ch <- c.gcRunsDesc
	ch <- c.requestsTotalDesc
	ch <- c.errorsTotalDesc
	ch <- c.requestsPerMinDesc
	ch <- c.authFailuresDesc
	ch <- c.bruteForceDesc
	ch <- c.historyEntriesDesc
	ch <- c.historyModeDesc
	ch <- c.historyActionDesc
	ch <- c.historySeverityDesc
	ch <- c.historyFiringDesc
	ch <- c.historyErrorsDesc
	ch <- c.historyDurationDesc
	ch <- c.sourceReqsDesc
	ch <- c.sourceErrsDesc
	ch <- c.sourceEntriesDesc
	ch <- c.sourceLastSeenDesc
	ch <- c.queueDepthDesc
	ch <- c.queueMaxSizeDesc
	ch <- c.queueRetriedDesc
	ch <- c.queueDroppedDesc
	ch <- c.queueFailedDesc
	ch <- c.queueProcessingDesc
	ch <- c.rlSlotsInUseDesc
	ch <- c.rlSlotsMaxDesc
	ch <- c.rlQueueDepthDesc
	ch <- c.rlQueueMaxDesc
	ch <- c.healthIcingaUpDesc
	ch <- c.healthFailsDesc

	// Histogram
	c.appMetrics.latencyHistogram.Describe(ch)
}

// Collect is called by the Prometheus registry when scraping.
func (c *PrometheusCollector) Collect(ch chan<- prometheus.Metric) {
	c.scrapeCount.Add(1)
	c.lastScrape.Store(time.Now().Unix())

	stats := c.appMetrics.Snapshot()

	// 3a. System / Go runtime
	ch <- prometheus.MustNewConstMetric(c.uptimeDesc, prometheus.GaugeValue, stats.UptimeSeconds)
	ch <- prometheus.MustNewConstMetric(c.goroutinesDesc, prometheus.GaugeValue, float64(stats.GoRoutines))
	ch <- prometheus.MustNewConstMetric(c.memAllocDesc, prometheus.GaugeValue, stats.MemAllocMB*1024*1024)
	ch <- prometheus.MustNewConstMetric(c.memSysDesc, prometheus.GaugeValue, stats.MemSysMB*1024*1024)
	ch <- prometheus.MustNewConstMetric(c.memHeapDesc, prometheus.GaugeValue, stats.MemHeapMB*1024*1024)
	ch <- prometheus.MustNewConstMetric(c.memStackDesc, prometheus.GaugeValue, stats.MemStackMB*1024*1024)
	ch <- prometheus.MustNewConstMetric(c.gcPauseDesc, prometheus.CounterValue, stats.GCPauseTotalMs/1000.0)
	ch <- prometheus.MustNewConstMetric(c.gcRunsDesc, prometheus.CounterValue, float64(stats.GCRuns))

	// 3b. HTTP request metrics
	ch <- prometheus.MustNewConstMetric(c.requestsTotalDesc, prometheus.CounterValue, float64(stats.TotalRequests))
	ch <- prometheus.MustNewConstMetric(c.errorsTotalDesc, prometheus.CounterValue, float64(stats.TotalErrors))
	ch <- prometheus.MustNewConstMetric(c.requestsPerMinDesc, prometheus.GaugeValue, stats.RequestsPerMin)
	c.appMetrics.latencyHistogram.Collect(ch)

	// 3c. Authentication / security
	ch <- prometheus.MustNewConstMetric(c.authFailuresDesc, prometheus.CounterValue, float64(stats.FailedAuthTotal))
	ch <- prometheus.MustNewConstMetric(c.bruteForceDesc, prometheus.GaugeValue, float64(len(stats.BruteForceIPs)))

	// 3d. Webhook history aggregates
	if c.history != nil {
		hStats, err := c.history.Stats()
		if err == nil {
			ch <- prometheus.MustNewConstMetric(c.historyEntriesDesc, prometheus.GaugeValue, float64(hStats.TotalEntries))
			for mode, count := range hStats.ByMode {
				ch <- prometheus.MustNewConstMetric(c.historyModeDesc, prometheus.GaugeValue, float64(count), mode)
			}
			for action, count := range hStats.ByAction {
				ch <- prometheus.MustNewConstMetric(c.historyActionDesc, prometheus.GaugeValue, float64(count), action)
			}
			for sev, count := range hStats.BySeverity {
				ch <- prometheus.MustNewConstMetric(c.historySeverityDesc, prometheus.GaugeValue, float64(count), sev)
			}
			for sev, count := range hStats.BySeverityFiring {
				ch <- prometheus.MustNewConstMetric(c.historyFiringDesc, prometheus.GaugeValue, float64(count), sev)
			}
			ch <- prometheus.MustNewConstMetric(c.historyErrorsDesc, prometheus.GaugeValue, float64(hStats.ErrorCount))
			ch <- prometheus.MustNewConstMetric(c.historyDurationDesc, prometheus.GaugeValue, float64(hStats.AvgDurationMs))

			// 3e. Per-API-key (source_history_entries part)
			for source, count := range hStats.BySource {
				ch <- prometheus.MustNewConstMetric(c.sourceEntriesDesc, prometheus.GaugeValue, float64(count), source)
			}
		}
	}

	// 3e. Per-API-key (real-time part)
	if c.perKey != nil {
		pkStats := c.perKey.GetStats()
		for source, s := range pkStats {
			ch <- prometheus.MustNewConstMetric(c.sourceReqsDesc, prometheus.CounterValue, float64(s.Requests.Load()), source)
			ch <- prometheus.MustNewConstMetric(c.sourceErrsDesc, prometheus.CounterValue, float64(s.Errors.Load()), source)
			lastSeen := float64(s.LastSeen.Load()) / 1e9
			ch <- prometheus.MustNewConstMetric(c.sourceLastSeenDesc, prometheus.GaugeValue, lastSeen, source)
		}
	}

	// 3f. Retry queue
	if c.queue != nil {
		qStats := c.queue.Stats()
		ch <- prometheus.MustNewConstMetric(c.queueDepthDesc, prometheus.GaugeValue, float64(qStats.Depth))
		ch <- prometheus.MustNewConstMetric(c.queueMaxSizeDesc, prometheus.GaugeValue, float64(qStats.MaxSize))
		ch <- prometheus.MustNewConstMetric(c.queueRetriedDesc, prometheus.CounterValue, float64(qStats.TotalRetried))
		ch <- prometheus.MustNewConstMetric(c.queueDroppedDesc, prometheus.CounterValue, float64(qStats.TotalDropped))
		ch <- prometheus.MustNewConstMetric(c.queueFailedDesc, prometheus.CounterValue, float64(qStats.TotalFailed))
		procVal := 0.0
		if qStats.Processing {
			procVal = 1.0
		}
		ch <- prometheus.MustNewConstMetric(c.queueProcessingDesc, prometheus.GaugeValue, procVal)
	}

	// 3g. Icinga2 rate limiter
	if c.rateLimiter != nil {
		mInUse, mMax, sInUse, sMax, q, mq := c.rateLimiter.Stats()
		ch <- prometheus.MustNewConstMetric(c.rlSlotsInUseDesc, prometheus.GaugeValue, float64(mInUse), "mutate")
		ch <- prometheus.MustNewConstMetric(c.rlSlotsMaxDesc, prometheus.GaugeValue, float64(mMax), "mutate")
		ch <- prometheus.MustNewConstMetric(c.rlSlotsInUseDesc, prometheus.GaugeValue, float64(sInUse), "status")
		ch <- prometheus.MustNewConstMetric(c.rlSlotsMaxDesc, prometheus.GaugeValue, float64(sMax), "status")
		ch <- prometheus.MustNewConstMetric(c.rlQueueDepthDesc, prometheus.GaugeValue, float64(q))
		ch <- prometheus.MustNewConstMetric(c.rlQueueMaxDesc, prometheus.GaugeValue, float64(mq))
	}

	// 3h. Health check
	if c.health != nil {
		hStatus := c.health.GetStatus()
		upVal := 0.0
		if hStatus.IcingaUp {
			upVal = 1.0
		}
		ch <- prometheus.MustNewConstMetric(c.healthIcingaUpDesc, prometheus.GaugeValue, upVal)
		ch <- prometheus.MustNewConstMetric(c.healthFailsDesc, prometheus.GaugeValue, float64(hStatus.ConsecutiveFails))
	}
}

// ScrapeStats returns metadata about prometheus scrapes.
func (c *PrometheusCollector) ScrapeStats() (int64, time.Time) {
	return c.scrapeCount.Load(), time.Unix(c.lastScrape.Load(), 0)
}

// UpdateComponents updates the collector with optional components after they are initialized.
func (c *PrometheusCollector) UpdateComponents(q *queue.Queue, rl *icinga.RateLimiter, h *health.Checker) {
	c.queue = q
	c.rateLimiter = rl
	c.health = h
}

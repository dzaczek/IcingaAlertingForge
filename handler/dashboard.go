package handler

import (
	"html/template"
	"net/http"
	"time"

	"icinga-webhook-bridge/cache"
	"icinga-webhook-bridge/history"
	"icinga-webhook-bridge/models"
)

// DashboardHandler serves the GET /status/beauty HTML dashboard
// with live statistics, recent alerts, and error information.
type DashboardHandler struct {
	Cache   *cache.ServiceCache
	History *history.Logger
	StartedAt time.Time
}

// dashboardData is the template context for the beauty dashboard.
type dashboardData struct {
	GeneratedAt   string
	Uptime        string
	Stats         history.HistoryStats
	CachedServices map[string]cache.ServiceState
	RecentAlerts  []dashboardAlert
	RecentErrors  []dashboardAlert
}

type dashboardAlert struct {
	Timestamp   string
	RequestID   string
	Source      string
	Mode        string
	Action      string
	ServiceName string
	Severity    string
	ExitStatus  int
	StatusLabel string
	StatusClass string
	Message     string
	IcingaOK    bool
	DurationMs  int64
	Error       string
}

func toDashboardAlert(e models.HistoryEntry) dashboardAlert {
	statusLabel := "OK"
	statusClass := "ok"
	switch e.ExitStatus {
	case 1:
		statusLabel = "WARNING"
		statusClass = "warning"
	case 2:
		statusLabel = "CRITICAL"
		statusClass = "critical"
	}
	if e.Mode == "test" {
		statusLabel = "TEST"
		statusClass = "test"
	}
	if !e.IcingaOK || e.Error != "" {
		statusClass = "error"
	}

	return dashboardAlert{
		Timestamp:   e.Timestamp.Format("2006-01-02 15:04:05 UTC"),
		RequestID:   e.RequestID,
		Source:      e.SourceKey,
		Mode:        e.Mode,
		Action:      e.Action,
		ServiceName: e.ServiceName,
		Severity:    e.Severity,
		ExitStatus:  e.ExitStatus,
		StatusLabel: statusLabel,
		StatusClass: statusClass,
		Message:     e.Message,
		IcingaOK:    e.IcingaOK,
		DurationMs:  e.DurationMs,
		Error:       e.Error,
	}
}

// ServeHTTP renders the beauty dashboard.
func (h *DashboardHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	stats, err := h.History.Stats()
	if err != nil {
		http.Error(w, "Failed to load statistics", http.StatusInternalServerError)
		return
	}

	var recentAlerts []dashboardAlert
	for _, e := range stats.RecentEntries {
		recentAlerts = append(recentAlerts, toDashboardAlert(e))
	}

	var recentErrors []dashboardAlert
	for _, e := range stats.RecentErrors {
		recentErrors = append(recentErrors, toDashboardAlert(e))
	}

	uptime := time.Since(h.StartedAt).Round(time.Second)

	data := dashboardData{
		GeneratedAt:    time.Now().UTC().Format("2006-01-02 15:04:05 UTC"),
		Uptime:         uptime.String(),
		Stats:          stats,
		CachedServices: h.Cache.All(),
		RecentAlerts:   recentAlerts,
		RecentErrors:   recentErrors,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := dashboardTemplate.Execute(w, data); err != nil {
		http.Error(w, "Failed to render dashboard", http.StatusInternalServerError)
	}
}

var dashboardTemplate = template.Must(template.New("dashboard").Parse(dashboardHTML))

const dashboardHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Webhook Bridge - Dashboard</title>
<meta http-equiv="refresh" content="30">
<style>
  :root {
    --bg: #0f1117;
    --card-bg: #1a1d27;
    --border: #2a2d3a;
    --text: #e1e4ed;
    --text-dim: #8b8fa3;
    --ok: #10b981;
    --ok-bg: rgba(16, 185, 129, 0.12);
    --warning: #f59e0b;
    --warning-bg: rgba(245, 158, 11, 0.12);
    --critical: #ef4444;
    --critical-bg: rgba(239, 68, 68, 0.12);
    --test: #6366f1;
    --test-bg: rgba(99, 102, 241, 0.12);
    --error: #f43f5e;
    --error-bg: rgba(244, 63, 94, 0.12);
    --accent: #3b82f6;
  }
  * { box-sizing: border-box; margin: 0; padding: 0; }
  body {
    font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'Helvetica Neue', sans-serif;
    background: var(--bg);
    color: var(--text);
    line-height: 1.5;
    padding: 24px;
  }
  .header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    margin-bottom: 24px;
    padding-bottom: 16px;
    border-bottom: 1px solid var(--border);
  }
  .header h1 {
    font-size: 22px;
    font-weight: 600;
    display: flex;
    align-items: center;
    gap: 10px;
  }
  .header h1 .dot {
    width: 10px; height: 10px;
    background: var(--ok);
    border-radius: 50%;
    display: inline-block;
    animation: pulse 2s infinite;
  }
  @keyframes pulse {
    0%, 100% { opacity: 1; }
    50% { opacity: 0.4; }
  }
  .header-meta { font-size: 13px; color: var(--text-dim); text-align: right; }
  .grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(200px, 1fr)); gap: 16px; margin-bottom: 24px; }
  .stat-card {
    background: var(--card-bg);
    border: 1px solid var(--border);
    border-radius: 10px;
    padding: 18px 20px;
  }
  .stat-card .label { font-size: 12px; text-transform: uppercase; letter-spacing: 0.05em; color: var(--text-dim); margin-bottom: 4px; }
  .stat-card .value { font-size: 28px; font-weight: 700; }
  .stat-card .value.ok { color: var(--ok); }
  .stat-card .value.warning { color: var(--warning); }
  .stat-card .value.critical { color: var(--critical); }
  .stat-card .value.error { color: var(--error); }

  .section { margin-bottom: 24px; }
  .section h2 {
    font-size: 16px;
    font-weight: 600;
    margin-bottom: 12px;
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .card {
    background: var(--card-bg);
    border: 1px solid var(--border);
    border-radius: 10px;
    overflow: hidden;
  }

  table { width: 100%; border-collapse: collapse; font-size: 13px; }
  thead th {
    text-align: left;
    padding: 10px 14px;
    background: rgba(255,255,255,0.03);
    color: var(--text-dim);
    font-weight: 500;
    font-size: 11px;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    border-bottom: 1px solid var(--border);
  }
  tbody td {
    padding: 9px 14px;
    border-bottom: 1px solid var(--border);
    vertical-align: middle;
  }
  tbody tr:last-child td { border-bottom: none; }
  tbody tr:hover { background: rgba(255,255,255,0.02); }

  .badge {
    display: inline-block;
    padding: 2px 10px;
    border-radius: 20px;
    font-size: 11px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.03em;
  }
  .badge.ok { background: var(--ok-bg); color: var(--ok); }
  .badge.warning { background: var(--warning-bg); color: var(--warning); }
  .badge.critical { background: var(--critical-bg); color: var(--critical); }
  .badge.test { background: var(--test-bg); color: var(--test); }
  .badge.error { background: var(--error-bg); color: var(--error); }

  .bar-chart { display: flex; gap: 6px; align-items: flex-end; height: 60px; margin-top: 8px; }
  .bar-item { display: flex; flex-direction: column; align-items: center; flex: 1; }
  .bar { border-radius: 4px 4px 0 0; min-width: 24px; transition: height 0.3s; }
  .bar-label { font-size: 10px; color: var(--text-dim); margin-top: 4px; }

  .source-list { display: flex; flex-wrap: wrap; gap: 8px; padding: 16px; }
  .source-tag {
    background: rgba(59, 130, 246, 0.1);
    border: 1px solid rgba(59, 130, 246, 0.2);
    color: var(--accent);
    padding: 4px 12px;
    border-radius: 20px;
    font-size: 12px;
    font-weight: 500;
  }
  .source-tag .count { color: var(--text-dim); margin-left: 4px; }

  .cached-services { display: flex; flex-wrap: wrap; gap: 8px; padding: 16px; }
  .service-tag {
    padding: 4px 12px;
    border-radius: 20px;
    font-size: 12px;
    font-weight: 500;
    border: 1px solid;
  }
  .service-tag.ready { background: var(--ok-bg); border-color: rgba(16,185,129,0.3); color: var(--ok); }
  .service-tag.pending { background: var(--warning-bg); border-color: rgba(245,158,11,0.3); color: var(--warning); }
  .service-tag.pending_delete { background: var(--critical-bg); border-color: rgba(239,68,68,0.3); color: var(--critical); }

  .empty-state { padding: 32px; text-align: center; color: var(--text-dim); font-size: 14px; }

  .footer {
    margin-top: 24px;
    padding-top: 16px;
    border-top: 1px solid var(--border);
    font-size: 12px;
    color: var(--text-dim);
    text-align: center;
  }
  .duration { color: var(--text-dim); font-size: 11px; }
  .icinga-ok { color: var(--ok); }
  .icinga-fail { color: var(--error); }
  .mono { font-family: 'SF Mono', SFMono-Regular, Menlo, monospace; font-size: 12px; }
</style>
</head>
<body>

<div class="header">
  <h1><span class="dot"></span> Webhook Bridge Dashboard</h1>
  <div class="header-meta">
    Generated: {{.GeneratedAt}}<br>
    Uptime: {{.Uptime}}<br>
    <em>Auto-refresh every 30s</em>
  </div>
</div>

<!-- ── Summary Statistics ────────────────────────────────── -->
<div class="grid">
  <div class="stat-card">
    <div class="label">Total Webhooks</div>
    <div class="value">{{.Stats.TotalEntries}}</div>
  </div>
  <div class="stat-card">
    <div class="label">Errors</div>
    <div class="value {{if gt .Stats.ErrorCount 0}}error{{else}}ok{{end}}">{{.Stats.ErrorCount}}</div>
  </div>
  <div class="stat-card">
    <div class="label">Avg Duration</div>
    <div class="value">{{.Stats.AvgDurationMs}}ms</div>
  </div>
  <div class="stat-card">
    <div class="label">Cached Services</div>
    <div class="value">{{len .CachedServices}}</div>
  </div>
</div>

<!-- ── Breakdown by Mode / Severity ──────────────────────── -->
<div class="grid">
  <div class="stat-card">
    <div class="label">Work Mode</div>
    <div class="value">{{index .Stats.ByMode "work"}}</div>
  </div>
  <div class="stat-card">
    <div class="label">Test Mode</div>
    <div class="value">{{index .Stats.ByMode "test"}}</div>
  </div>
  <div class="stat-card">
    <div class="label">Critical Alerts</div>
    <div class="value critical">{{index .Stats.BySeverity "critical"}}</div>
  </div>
  <div class="stat-card">
    <div class="label">Warning Alerts</div>
    <div class="value warning">{{index .Stats.BySeverity "warning"}}</div>
  </div>
</div>

<!-- ── Sources ───────────────────────────────────────────── -->
<div class="section">
  <h2>Sources</h2>
  <div class="card">
    {{if .Stats.BySource}}
    <div class="source-list">
      {{range $source, $count := .Stats.BySource}}
      <span class="source-tag">{{$source}} <span class="count">({{$count}})</span></span>
      {{end}}
    </div>
    {{else}}
    <div class="empty-state">No sources recorded yet</div>
    {{end}}
  </div>
</div>

<!-- ── Cached Services ───────────────────────────────────── -->
<div class="section">
  <h2>Cached Services</h2>
  <div class="card">
    {{if .CachedServices}}
    <div class="cached-services">
      {{range $name, $state := .CachedServices}}
      <span class="service-tag {{$state}}">{{$name}} ({{$state}})</span>
      {{end}}
    </div>
    {{else}}
    <div class="empty-state">No services cached</div>
    {{end}}
  </div>
</div>

<!-- ── Recent Alerts ─────────────────────────────────────── -->
<div class="section">
  <h2>Recent Alerts (last 20)</h2>
  <div class="card">
    {{if .RecentAlerts}}
    <table>
      <thead>
        <tr>
          <th>Time</th>
          <th>Status</th>
          <th>Mode</th>
          <th>Action</th>
          <th>Service</th>
          <th>Source</th>
          <th>Icinga</th>
          <th>Duration</th>
        </tr>
      </thead>
      <tbody>
        {{range .RecentAlerts}}
        <tr>
          <td class="mono">{{.Timestamp}}</td>
          <td><span class="badge {{.StatusClass}}">{{.StatusLabel}}</span></td>
          <td>{{.Mode}}</td>
          <td>{{.Action}}</td>
          <td><strong>{{.ServiceName}}</strong></td>
          <td class="mono">{{.Source}}</td>
          <td>{{if .IcingaOK}}<span class="icinga-ok">OK</span>{{else}}<span class="icinga-fail">FAIL</span>{{end}}</td>
          <td class="duration">{{.DurationMs}}ms</td>
        </tr>
        {{end}}
      </tbody>
    </table>
    {{else}}
    <div class="empty-state">No alerts recorded yet</div>
    {{end}}
  </div>
</div>

<!-- ── Recent Errors ─────────────────────────────────────── -->
<div class="section">
  <h2>Recent Errors (last 10)</h2>
  <div class="card">
    {{if .RecentErrors}}
    <table>
      <thead>
        <tr>
          <th>Time</th>
          <th>Service</th>
          <th>Action</th>
          <th>Source</th>
          <th>Error / Message</th>
          <th>Duration</th>
        </tr>
      </thead>
      <tbody>
        {{range .RecentErrors}}
        <tr>
          <td class="mono">{{.Timestamp}}</td>
          <td><strong>{{.ServiceName}}</strong></td>
          <td>{{.Action}}</td>
          <td class="mono">{{.Source}}</td>
          <td style="color: var(--error); max-width: 400px; overflow: hidden; text-overflow: ellipsis;">{{if .Error}}{{.Error}}{{else}}{{.Message}}{{end}}</td>
          <td class="duration">{{.DurationMs}}ms</td>
        </tr>
        {{end}}
      </tbody>
    </table>
    {{else}}
    <div class="empty-state" style="color: var(--ok);">No errors - everything is running smoothly</div>
    {{end}}
  </div>
</div>

<div class="footer">
  IcingaAlertForge &middot; Grafana &rarr; Webhook Bridge &rarr; Icinga2 &middot; v1.0
</div>

</body>
</html>`

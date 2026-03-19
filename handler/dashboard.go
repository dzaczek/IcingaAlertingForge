package handler

import (
	"crypto/subtle"
	"html/template"
	"log/slog"
	"net/http"
	"time"

	"icinga-webhook-bridge/cache"
	"icinga-webhook-bridge/history"
	"icinga-webhook-bridge/icinga"
	"icinga-webhook-bridge/metrics"
	"icinga-webhook-bridge/models"
)

// DashboardHandler serves the GET /status/beauty HTML dashboard
// with live statistics, recent alerts, and error information.
type DashboardHandler struct {
	Cache     *cache.ServiceCache
	History   *history.Logger
	API       *icinga.APIClient
	Metrics   *metrics.Collector
	HostName  string
	AdminUser string
	AdminPass string
	StartedAt time.Time
}

// dashboardData is the template context for the beauty dashboard.
type dashboardData struct {
	GeneratedAt    string
	Uptime         string
	Stats          history.HistoryStats
	CachedServices map[string]cache.ServiceState
	RecentAlerts   []dashboardAlert
	RecentErrors   []dashboardAlert
	IsAdmin        bool
	IcingaServices []icinga.ServiceInfo
	HostName       string
	SysStats       metrics.SystemStats
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

// isAdmin checks if the request has valid admin credentials.
func (h *DashboardHandler) isAdmin(r *http.Request) bool {
	if h.AdminPass == "" {
		return false
	}
	user, pass, ok := r.BasicAuth()
	if !ok {
		return false
	}
	userOK := subtle.ConstantTimeCompare([]byte(user), []byte(h.AdminUser)) == 1
	passOK := subtle.ConstantTimeCompare([]byte(pass), []byte(h.AdminPass)) == 1
	return userOK && passOK
}

// ServeHTTP renders the beauty dashboard.
func (h *DashboardHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	// Check if admin login was requested
	isAdmin := h.isAdmin(r)
	if r.URL.Query().Get("admin") == "1" && !isAdmin {
		w.Header().Set("WWW-Authenticate", `Basic realm="Dashboard Admin"`)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
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

	// If admin, fetch live services from Icinga2
	var icingaServices []icinga.ServiceInfo
	if isAdmin {
		svcs, err := h.API.ListServices(h.HostName)
		if err != nil {
			slog.Error("Dashboard: failed to list Icinga2 services", "error", err)
		} else {
			icingaServices = svcs
		}
	}

	var sysStats metrics.SystemStats
	if h.Metrics != nil {
		sysStats = h.Metrics.Snapshot()
	}

	data := dashboardData{
		GeneratedAt:    time.Now().UTC().Format("2006-01-02 15:04:05 UTC"),
		Uptime:         uptime.String(),
		Stats:          stats,
		CachedServices: h.Cache.All(),
		RecentAlerts:   recentAlerts,
		RecentErrors:   recentErrors,
		IsAdmin:        isAdmin,
		IcingaServices: icingaServices,
		HostName:       h.HostName,
		SysStats:       sysStats,
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
  .header-meta a { color: var(--accent); text-decoration: none; }
  .header-meta a:hover { text-decoration: underline; }
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
    cursor: pointer;
    user-select: none;
    white-space: nowrap;
  }
  thead th:hover { color: var(--text); }
  thead th .sort-arrow { margin-left: 4px; font-size: 10px; }
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
  .badge.unknown { background: rgba(107,114,128,0.12); color: #9ca3af; }

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

  /* Admin styles */
  .btn {
    padding: 4px 12px;
    border: none;
    border-radius: 6px;
    font-size: 12px;
    font-weight: 500;
    cursor: pointer;
    transition: opacity 0.2s;
  }
  .btn:hover { opacity: 0.85; }
  .btn:disabled { opacity: 0.4; cursor: not-allowed; }
  .btn-danger { background: var(--critical); color: white; }
  .btn-primary { background: var(--accent); color: white; }
  .btn-sm { padding: 2px 8px; font-size: 11px; }
  .toolbar { display: flex; gap: 8px; align-items: center; padding: 12px 16px; border-bottom: 1px solid var(--border); }
  .toolbar label { font-size: 12px; color: var(--text-dim); }
  .checkbox-cell { width: 30px; text-align: center; }
  input[type="checkbox"] { cursor: pointer; accent-color: var(--accent); }
  .admin-badge {
    background: var(--accent);
    color: white;
    padding: 2px 8px;
    border-radius: 4px;
    font-size: 11px;
    font-weight: 600;
    margin-left: 8px;
  }
  .toast {
    position: fixed;
    bottom: 24px;
    right: 24px;
    padding: 12px 20px;
    border-radius: 8px;
    font-size: 13px;
    font-weight: 500;
    z-index: 1000;
    opacity: 0;
    transition: opacity 0.3s;
    max-width: 400px;
  }
  .toast.show { opacity: 1; }
  .toast.success { background: var(--ok); color: white; }
  .toast.error { background: var(--critical); color: white; }
</style>
</head>
<body>

<div class="header">
  <h1>
    <span class="dot"></span> Webhook Bridge Dashboard
    {{if .IsAdmin}}<span class="admin-badge">ADMIN</span>{{end}}
  </h1>
  <div class="header-meta">
    Generated: {{.GeneratedAt}}<br>
    Uptime: {{.Uptime}}<br>
    <em>Auto-refresh every 30s</em><br>
    {{if not .IsAdmin}}<a href="?admin=1">Admin Login</a>{{else}}<a href="/status/beauty">Logout</a>{{end}}
  </div>
</div>

<!-- Summary Statistics -->
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

<!-- Breakdown by Mode / Severity -->
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

<!-- Sources -->
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

<!-- Cached Services -->
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

{{if .IsAdmin}}
<!-- Admin: System Health -->
<div class="section">
  <h2>System Health</h2>
  <div class="grid">
    <div class="stat-card">
      <div class="label">CPU Cores</div>
      <div class="value">{{.SysStats.NumCPU}}</div>
    </div>
    <div class="stat-card">
      <div class="label">Goroutines</div>
      <div class="value {{if gt .SysStats.GoRoutines 100}}warning{{else}}ok{{end}}">{{.SysStats.GoRoutines}}</div>
    </div>
    <div class="stat-card">
      <div class="label">Memory (Alloc)</div>
      <div class="value">{{printf "%.1f" .SysStats.MemAllocMB}} MB</div>
    </div>
    <div class="stat-card">
      <div class="label">Memory (Sys)</div>
      <div class="value">{{printf "%.1f" .SysStats.MemSysMB}} MB</div>
    </div>
    <div class="stat-card">
      <div class="label">Heap</div>
      <div class="value">{{printf "%.1f" .SysStats.MemHeapMB}} MB</div>
    </div>
    <div class="stat-card">
      <div class="label">Stack</div>
      <div class="value">{{printf "%.2f" .SysStats.MemStackMB}} MB</div>
    </div>
    <div class="stat-card">
      <div class="label">GC Runs</div>
      <div class="value">{{.SysStats.GCRuns}}</div>
    </div>
    <div class="stat-card">
      <div class="label">GC Pause Total</div>
      <div class="value">{{printf "%.1f" .SysStats.GCPauseTotalMs}} ms</div>
    </div>
  </div>
</div>

<!-- Admin: Performance Metrics -->
<div class="section">
  <h2>Performance</h2>
  <div class="grid">
    <div class="stat-card">
      <div class="label">Total Requests</div>
      <div class="value">{{.SysStats.TotalRequests}}</div>
    </div>
    <div class="stat-card">
      <div class="label">Total Errors</div>
      <div class="value {{if gt .SysStats.TotalErrors 0}}error{{else}}ok{{end}}">{{.SysStats.TotalErrors}}</div>
    </div>
    <div class="stat-card">
      <div class="label">Error Rate</div>
      <div class="value {{if gt .SysStats.ErrorRate 5.0}}critical{{else if gt .SysStats.ErrorRate 1.0}}warning{{else}}ok{{end}}">{{printf "%.1f" .SysStats.ErrorRate}}%</div>
    </div>
    <div class="stat-card">
      <div class="label">Avg Latency</div>
      <div class="value {{if gt .SysStats.AvgLatencyMs 500.0}}critical{{else if gt .SysStats.AvgLatencyMs 200.0}}warning{{else}}ok{{end}}">{{printf "%.0f" .SysStats.AvgLatencyMs}} ms</div>
    </div>
    <div class="stat-card">
      <div class="label">Requests/min</div>
      <div class="value">{{printf "%.1f" .SysStats.RequestsPerMin}}</div>
    </div>
    <div class="stat-card">
      <div class="label">Uptime</div>
      <div class="value" style="font-size:18px;">{{.SysStats.Uptime}}</div>
    </div>
  </div>
</div>

<!-- Admin: Security -->
<div class="section">
  <h2>Security</h2>
  <div class="grid">
    <div class="stat-card">
      <div class="label">Failed Auth (total)</div>
      <div class="value {{if gt .SysStats.FailedAuthTotal 0}}warning{{else}}ok{{end}}">{{.SysStats.FailedAuthTotal}}</div>
    </div>
    <div class="stat-card">
      <div class="label">Brute Force IPs</div>
      <div class="value {{if .SysStats.BruteForceIPs}}critical{{else}}ok{{end}}">{{len .SysStats.BruteForceIPs}}</div>
    </div>
  </div>

  {{if .SysStats.BruteForceIPs}}
  <div style="margin-top:12px;">
    <h2 style="color:var(--critical);">Brute Force Detected</h2>
    <div class="card">
      <table>
        <thead>
          <tr>
            <th>IP Address</th>
            <th>Attempts</th>
            <th>Last Seen</th>
          </tr>
        </thead>
        <tbody>
          {{range .SysStats.BruteForceIPs}}
          <tr>
            <td class="mono" style="color:var(--critical);"><strong>{{.IP}}</strong></td>
            <td><span class="badge critical">{{.Attempts}}</span></td>
            <td class="mono">{{.LastSeen}}</td>
          </tr>
          {{end}}
        </tbody>
      </table>
    </div>
  </div>
  {{end}}

  {{if .SysStats.FailedAuthRecent}}
  <div style="margin-top:12px;">
    <h2>Recent Failed Auth (last hour)</h2>
    <div class="card">
      <table>
        <thead>
          <tr>
            <th>Time</th>
            <th>IP Address</th>
            <th>Key Used</th>
          </tr>
        </thead>
        <tbody>
          {{range .SysStats.FailedAuthRecent}}
          <tr>
            <td class="mono">{{.Timestamp.Format "2006-01-02 15:04:05"}}</td>
            <td class="mono">{{.IP}}</td>
            <td class="mono" style="color:var(--warning);">{{.KeyPrefix}}</td>
          </tr>
          {{end}}
        </tbody>
      </table>
    </div>
  </div>
  {{end}}
</div>

<!-- Admin: Icinga2 Services Management -->
<div class="section">
  <h2>Icinga2 Services on "{{.HostName}}" (Admin)</h2>
  <div class="card">
    {{if .IcingaServices}}
    <div class="toolbar">
      <input type="checkbox" id="selectAll" onclick="toggleAll(this)">
      <label for="selectAll">Select All</label>
      <button class="btn btn-danger btn-sm" onclick="deleteSelected()" id="btnDeleteSelected" disabled>Delete Selected</button>
      <span style="margin-left:auto; font-size:12px; color:var(--text-dim);">{{len .IcingaServices}} service(s)</span>
    </div>
    <table id="servicesTable">
      <thead>
        <tr>
          <th class="checkbox-cell"></th>
          <th onclick="sortTable(1,'string')">Name <span class="sort-arrow"></span></th>
          <th onclick="sortTable(2,'string')">Display Name <span class="sort-arrow"></span></th>
          <th onclick="sortTable(3,'string')">Status <span class="sort-arrow"></span></th>
          <th onclick="sortTable(4,'string')">Output <span class="sort-arrow"></span></th>
          <th onclick="sortTable(5,'date')">Last Check <span class="sort-arrow"></span></th>
          <th>Actions</th>
        </tr>
      </thead>
      <tbody>
        {{range .IcingaServices}}
        <tr data-service="{{.Name}}">
          <td class="checkbox-cell"><input type="checkbox" class="svc-check" value="{{.Name}}"></td>
          <td><strong>{{.Name}}</strong></td>
          <td>{{.DisplayName}}</td>
          <td>
            {{if .HasCheckResult}}
              {{if eq .ExitStatus 0}}<span class="badge ok">OK</span>
              {{else if eq .ExitStatus 1}}<span class="badge warning">WARNING</span>
              {{else if eq .ExitStatus 2}}<span class="badge critical">CRITICAL</span>
              {{else}}<span class="badge unknown">UNKNOWN</span>
              {{end}}
            {{else}}<span class="badge unknown">PENDING</span>
            {{end}}
          </td>
          <td style="max-width:300px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;" title="{{.Output}}">{{.Output}}</td>
          <td class="mono">{{if .HasCheckResult}}{{.LastCheck.Format "2006-01-02 15:04:05"}}{{else}}-{{end}}</td>
          <td><button class="btn btn-danger btn-sm" onclick="deleteService('{{.Name}}', this)">Delete</button></td>
        </tr>
        {{end}}
      </tbody>
    </table>
    {{else}}
    <div class="empty-state">No services found on host "{{.HostName}}"</div>
    {{end}}
  </div>
</div>
{{end}}

<!-- Recent Alerts -->
<div class="section">
  <h2>Recent Alerts (last 20)</h2>
  <div class="card">
    {{if .RecentAlerts}}
    <table id="alertsTable">
      <thead>
        <tr>
          <th onclick="sortTable(0,'date',this.closest('table'))">Time <span class="sort-arrow"></span></th>
          <th onclick="sortTable(1,'string',this.closest('table'))">Status <span class="sort-arrow"></span></th>
          <th onclick="sortTable(2,'string',this.closest('table'))">Mode <span class="sort-arrow"></span></th>
          <th onclick="sortTable(3,'string',this.closest('table'))">Action <span class="sort-arrow"></span></th>
          <th onclick="sortTable(4,'string',this.closest('table'))">Service <span class="sort-arrow"></span></th>
          <th onclick="sortTable(5,'string',this.closest('table'))">Source <span class="sort-arrow"></span></th>
          <th>Icinga</th>
          <th onclick="sortTable(7,'number',this.closest('table'))">Duration <span class="sort-arrow"></span></th>
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

<!-- Recent Errors -->
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

<div class="toast" id="toast"></div>

<script>
// Table sorting
function sortTable(colIdx, type, tableEl) {
  const table = tableEl || document.getElementById('servicesTable');
  if (!table) return;
  const tbody = table.querySelector('tbody');
  const rows = Array.from(tbody.querySelectorAll('tr'));
  const th = table.querySelectorAll('thead th')[colIdx];

  // Toggle sort direction
  const dir = th.dataset.sortDir === 'asc' ? 'desc' : 'asc';
  table.querySelectorAll('thead th').forEach(h => { h.dataset.sortDir = ''; });
  th.dataset.sortDir = dir;

  // Update arrows
  table.querySelectorAll('.sort-arrow').forEach(a => a.textContent = '');
  const arrow = th.querySelector('.sort-arrow');
  if (arrow) arrow.textContent = dir === 'asc' ? ' \u25B2' : ' \u25BC';

  rows.sort((a, b) => {
    let va = a.cells[colIdx]?.textContent.trim() || '';
    let vb = b.cells[colIdx]?.textContent.trim() || '';
    let cmp = 0;
    if (type === 'number') {
      cmp = parseFloat(va) - parseFloat(vb);
    } else if (type === 'date') {
      cmp = new Date(va) - new Date(vb);
    } else {
      cmp = va.localeCompare(vb);
    }
    return dir === 'asc' ? cmp : -cmp;
  });

  rows.forEach(r => tbody.appendChild(r));
}

{{if .IsAdmin}}
// Admin functions
function getAuthHeader() {
  // Re-use current Basic Auth credentials from the page load
  // The browser sends them automatically for same-origin requests
  return {};
}

function showToast(msg, type) {
  const t = document.getElementById('toast');
  t.textContent = msg;
  t.className = 'toast show ' + type;
  setTimeout(() => t.className = 'toast', 3000);
}

function deleteService(name, btn) {
  if (!confirm('Delete service "' + name + '" from Icinga2?')) return;
  btn.disabled = true;
  btn.textContent = '...';

  fetch('/admin/services/' + encodeURIComponent(name), {
    method: 'DELETE',
  }).then(r => r.json()).then(data => {
    if (data.status === 'deleted') {
      showToast('Deleted: ' + name, 'success');
      const row = btn.closest('tr');
      if (row) row.remove();
    } else {
      showToast('Error: ' + (data.error || 'unknown'), 'error');
      btn.disabled = false;
      btn.textContent = 'Delete';
    }
  }).catch(err => {
    showToast('Error: ' + err.message, 'error');
    btn.disabled = false;
    btn.textContent = 'Delete';
  });
}

function toggleAll(master) {
  document.querySelectorAll('.svc-check').forEach(cb => cb.checked = master.checked);
  updateBulkBtn();
}

function updateBulkBtn() {
  const checked = document.querySelectorAll('.svc-check:checked').length;
  const btn = document.getElementById('btnDeleteSelected');
  if (btn) {
    btn.disabled = checked === 0;
    btn.textContent = checked > 0 ? 'Delete Selected (' + checked + ')' : 'Delete Selected';
  }
}

// Listen for checkbox changes
document.addEventListener('change', function(e) {
  if (e.target.classList.contains('svc-check')) updateBulkBtn();
});

function deleteSelected() {
  const checked = Array.from(document.querySelectorAll('.svc-check:checked'));
  const names = checked.map(cb => cb.value);
  if (names.length === 0) return;
  if (!confirm('Delete ' + names.length + ' service(s) from Icinga2?')) return;

  const btn = document.getElementById('btnDeleteSelected');
  btn.disabled = true;
  btn.textContent = 'Deleting...';

  fetch('/admin/services/bulk-delete', {
    method: 'POST',
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify({services: names}),
  }).then(r => r.json()).then(data => {
    let deleted = 0, errors = 0;
    (data.results || []).forEach(r => {
      if (r.status === 'deleted') {
        deleted++;
        const row = document.querySelector('tr[data-service="' + r.service + '"]');
        if (row) row.remove();
      } else {
        errors++;
      }
    });
    if (errors > 0) {
      showToast('Deleted ' + deleted + ', errors: ' + errors, 'error');
    } else {
      showToast('Deleted ' + deleted + ' service(s)', 'success');
    }
    updateBulkBtn();
  }).catch(err => {
    showToast('Error: ' + err.message, 'error');
    btn.disabled = false;
    btn.textContent = 'Delete Selected';
  });
}
{{end}}
</script>

</body>
</html>`

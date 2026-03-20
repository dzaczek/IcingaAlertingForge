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
<title>LCARS - IcingaAlertForge</title>
<meta http-equiv="refresh" content="30">
<link href="https://fonts.googleapis.com/css2?family=Antonio:wght@400;700&family=Orbitron:wght@400;700&display=swap" rel="stylesheet">
<style>
  :root {
    --lcars-orange: #ff9900;
    --lcars-tan: #ffcc99;
    --lcars-peach: #ff9966;
    --lcars-lavender: #cc99cc;
    --lcars-purple: #9999ff;
    --lcars-blue: #99ccff;
    --lcars-blue-dark: #6688cc;
    --lcars-red: #cc6666;
    --lcars-gold: #cc9933;
    --lcars-bg: #000000;
    --lcars-text: #ff9900;
    --lcars-text-light: #ffcc99;
    --lcars-ok: #99cc66;
    --lcars-warning: #ff9900;
    --lcars-critical: #cc6666;
    --lcars-test: #9999ff;
    --lcars-error: #cc6666;
    --lcars-sidebar-w: 180px;
    --lcars-header-h: 52px;
    --lcars-radius: 40px;
    --lcars-gap: 6px;
  }
  * { box-sizing: border-box; margin: 0; padding: 0; }
  body {
    font-family: 'Antonio', 'Helvetica Neue', sans-serif;
    background: var(--lcars-bg);
    color: var(--lcars-text);
    min-height: 100vh;
    overflow-x: hidden;
  }

  /* ── LCARS Frame ── */
  .lcars-frame {
    display: grid;
    grid-template-columns: var(--lcars-sidebar-w) 1fr;
    grid-template-rows: var(--lcars-header-h) 1fr auto;
    min-height: 100vh;
    gap: var(--lcars-gap);
    padding: var(--lcars-gap);
  }

  /* ── Top Header Bar ── */
  .lcars-header {
    grid-column: 1 / -1;
    display: flex;
    align-items: stretch;
    gap: var(--lcars-gap);
  }
  .lcars-header-cap {
    background: var(--lcars-orange);
    width: var(--lcars-sidebar-w);
    border-radius: var(--lcars-radius) 0 0 0;
    display: flex;
    align-items: center;
    justify-content: center;
    min-width: var(--lcars-sidebar-w);
  }
  .lcars-header-cap span {
    font-family: 'Orbitron', sans-serif;
    font-size: 11px;
    font-weight: 700;
    color: #000;
    letter-spacing: 2px;
    text-transform: uppercase;
  }
  .lcars-header-bar {
    flex: 1;
    display: flex;
    align-items: center;
    gap: var(--lcars-gap);
  }
  .lcars-header-bar .bar-segment {
    height: 100%;
    border-radius: 0;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 0 16px;
    font-size: 14px;
    font-weight: 700;
    letter-spacing: 1.5px;
    text-transform: uppercase;
    color: #000;
    white-space: nowrap;
    cursor: default;
  }
  .bar-seg-1 { background: var(--lcars-tan); flex: 2; border-radius: 0; }
  .bar-seg-2 { background: var(--lcars-lavender); flex: 1; }
  .bar-seg-3 { background: var(--lcars-blue); flex: 3; font-size: 18px; letter-spacing: 4px; }
  .bar-seg-4 { background: var(--lcars-purple); flex: 1; }
  .bar-seg-5 { background: var(--lcars-peach); flex: 1; border-radius: 0 var(--lcars-radius) 0 0; }

  /* ── Left Sidebar ── */
  .lcars-sidebar {
    display: flex;
    flex-direction: column;
    gap: var(--lcars-gap);
  }
  .lcars-sidebar .sidebar-btn {
    background: var(--lcars-orange);
    border: none;
    border-radius: var(--lcars-radius) 0 0 var(--lcars-radius);
    padding: 12px 14px;
    font-family: 'Antonio', sans-serif;
    font-size: 14px;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 1px;
    color: #000;
    cursor: pointer;
    text-align: right;
    transition: filter 0.15s;
    white-space: nowrap;
  }
  .lcars-sidebar .sidebar-btn:hover { filter: brightness(1.2); }
  .lcars-sidebar .sidebar-btn.active { background: var(--lcars-blue); }
  .lcars-sidebar .sidebar-btn.purple { background: var(--lcars-lavender); }
  .lcars-sidebar .sidebar-btn.tan { background: var(--lcars-tan); }
  .lcars-sidebar .sidebar-btn.blue { background: var(--lcars-blue); }
  .lcars-sidebar .sidebar-btn.gold { background: var(--lcars-gold); }
  .lcars-sidebar .sidebar-btn.peach { background: var(--lcars-peach); }
  .lcars-sidebar .sidebar-spacer {
    flex: 1;
    background: var(--lcars-orange);
    border-radius: var(--lcars-radius) 0 0 var(--lcars-radius);
    min-height: 20px;
    opacity: 0.4;
  }
  .lcars-sidebar .sidebar-decoration {
    background: var(--lcars-tan);
    height: 16px;
    border-radius: var(--lcars-radius) 0 0 var(--lcars-radius);
  }
  .lcars-sidebar .sidebar-decoration.purple { background: var(--lcars-lavender); }
  .lcars-sidebar .sidebar-decoration.blue { background: var(--lcars-blue); }

  /* ── Main Content Area ── */
  .lcars-content {
    padding: 16px 20px;
    overflow-y: auto;
  }

  /* ── Footer ── */
  .lcars-footer {
    grid-column: 1 / -1;
    display: flex;
    align-items: stretch;
    gap: var(--lcars-gap);
    height: 32px;
  }
  .lcars-footer-cap {
    background: var(--lcars-orange);
    width: var(--lcars-sidebar-w);
    border-radius: 0 0 0 var(--lcars-radius);
    min-width: var(--lcars-sidebar-w);
  }
  .lcars-footer-bar {
    flex: 1;
    display: flex;
    gap: var(--lcars-gap);
    align-items: stretch;
  }
  .lcars-footer-bar > div { height: 100%; }
  .foot-1 { background: var(--lcars-blue); flex: 3; border-radius: 0; display:flex; align-items:center; padding:0 12px; font-size:11px; color:#000; letter-spacing:1px; text-transform:uppercase; font-weight:700; }
  .foot-2 { background: var(--lcars-lavender); flex: 2; }
  .foot-3 { background: var(--lcars-tan); flex: 1; }
  .foot-4 { background: var(--lcars-orange); flex: 1; border-radius: 0 0 var(--lcars-radius) 0; }

  /* ── LCARS Panel (card replacement) ── */
  .lcars-panel {
    margin-bottom: 20px;
    border: 2px solid var(--lcars-orange);
    border-radius: 20px;
    overflow: hidden;
    background: rgba(255,153,0,0.03);
  }
  .lcars-panel-header {
    display: flex;
    align-items: stretch;
    gap: 0;
  }
  .lcars-panel-elbow {
    width: 60px;
    min-height: 38px;
    position: relative;
    overflow: hidden;
  }
  .lcars-panel-elbow::before {
    content: '';
    position: absolute;
    top: 0; left: 0;
    width: 60px; height: 100%;
    background: var(--lcars-orange);
    border-radius: 0 0 20px 0;
  }
  .lcars-panel-elbow.purple::before { background: var(--lcars-lavender); }
  .lcars-panel-elbow.blue::before { background: var(--lcars-blue); }
  .lcars-panel-elbow.tan::before { background: var(--lcars-tan); }
  .lcars-panel-elbow.red::before { background: var(--lcars-red); }
  .lcars-panel-elbow.gold::before { background: var(--lcars-gold); }

  .lcars-panel-title-bar {
    flex: 1;
    display: flex;
    align-items: center;
    padding: 0 16px;
    min-height: 38px;
  }
  .lcars-panel-title-bar .bar-fill {
    flex: 1;
    height: 6px;
    background: var(--lcars-orange);
    border-radius: 3px;
    margin-right: 12px;
    opacity: 0.5;
  }
  .lcars-panel-title-bar .title-text {
    font-family: 'Antonio', sans-serif;
    font-size: 16px;
    font-weight: 700;
    letter-spacing: 2px;
    text-transform: uppercase;
    color: var(--lcars-orange);
    white-space: nowrap;
  }
  .lcars-panel-title-bar .title-text.purple { color: var(--lcars-lavender); }
  .lcars-panel-title-bar .title-text.blue { color: var(--lcars-blue); }
  .lcars-panel-title-bar .title-text.red { color: var(--lcars-red); }
  .lcars-panel-title-bar .title-text.tan { color: var(--lcars-tan); }

  .lcars-panel.purple { border-color: var(--lcars-lavender); }
  .lcars-panel.blue { border-color: var(--lcars-blue); }
  .lcars-panel.red { border-color: var(--lcars-red); }
  .lcars-panel.tan { border-color: var(--lcars-tan); }
  .lcars-panel.purple .lcars-panel-title-bar .bar-fill { background: var(--lcars-lavender); }
  .lcars-panel.blue .lcars-panel-title-bar .bar-fill { background: var(--lcars-blue); }
  .lcars-panel.red .lcars-panel-title-bar .bar-fill { background: var(--lcars-red); }

  .lcars-panel-body { padding: 16px 20px 16px 80px; }

  /* ── Stat Grid ── */
  .stat-grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
    gap: 12px;
    margin-bottom: 16px;
  }
  .stat-cell {
    background: rgba(255,153,0,0.06);
    border: 1px solid rgba(255,153,0,0.2);
    border-radius: 12px;
    padding: 14px 16px;
    position: relative;
    overflow: hidden;
  }
  .stat-cell::before {
    content: '';
    position: absolute;
    top: 0; left: 0;
    width: 4px; height: 100%;
    background: var(--lcars-orange);
    border-radius: 2px;
  }
  .stat-cell.ok::before { background: var(--lcars-ok); }
  .stat-cell.warning::before { background: var(--lcars-warning); }
  .stat-cell.critical::before { background: var(--lcars-critical); }
  .stat-cell.purple::before { background: var(--lcars-lavender); }
  .stat-cell.blue::before { background: var(--lcars-blue); }

  .stat-cell .stat-label {
    font-size: 11px;
    text-transform: uppercase;
    letter-spacing: 1.5px;
    color: var(--lcars-tan);
    margin-bottom: 4px;
  }
  .stat-cell .stat-value {
    font-family: 'Orbitron', sans-serif;
    font-size: 28px;
    font-weight: 700;
    color: var(--lcars-orange);
  }
  .stat-cell .stat-value.ok { color: var(--lcars-ok); }
  .stat-cell .stat-value.warning { color: var(--lcars-warning); }
  .stat-cell .stat-value.critical { color: var(--lcars-critical); }
  .stat-cell .stat-value.error { color: var(--lcars-red); }
  .stat-cell .stat-value.purple { color: var(--lcars-purple); }
  .stat-cell .stat-value.blue { color: var(--lcars-blue); }

  /* ── Tables ── */
  table { width: 100%; border-collapse: collapse; font-size: 13px; }
  thead th {
    text-align: left;
    padding: 8px 12px;
    background: rgba(255,153,0,0.08);
    color: var(--lcars-tan);
    font-weight: 700;
    font-size: 11px;
    text-transform: uppercase;
    letter-spacing: 1.5px;
    border-bottom: 2px solid var(--lcars-orange);
    cursor: pointer;
    user-select: none;
    white-space: nowrap;
  }
  thead th:hover { color: var(--lcars-orange); }
  thead th .sort-arrow { margin-left: 4px; font-size: 10px; }
  tbody td {
    padding: 7px 12px;
    border-bottom: 1px solid rgba(255,153,0,0.1);
    vertical-align: middle;
    color: var(--lcars-text-light);
  }
  tbody tr:last-child td { border-bottom: none; }
  tbody tr:hover { background: rgba(255,153,0,0.05); }

  /* ── Badges ── */
  .badge {
    display: inline-block;
    padding: 2px 12px;
    border-radius: 20px;
    font-size: 11px;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 1px;
    font-family: 'Antonio', sans-serif;
  }
  .badge.ok { background: var(--lcars-ok); color: #000; }
  .badge.warning { background: var(--lcars-warning); color: #000; }
  .badge.critical { background: var(--lcars-critical); color: #000; }
  .badge.test { background: var(--lcars-test); color: #000; }
  .badge.error { background: var(--lcars-error); color: #000; }
  .badge.unknown { background: #666; color: #000; }

  /* ── Tags ── */
  .tag-list { display: flex; flex-wrap: wrap; gap: 8px; padding: 4px 0; }
  .source-tag {
    background: rgba(153,204,255,0.1);
    border: 1px solid var(--lcars-blue);
    color: var(--lcars-blue);
    padding: 4px 14px;
    border-radius: 20px;
    font-size: 13px;
    font-weight: 700;
    letter-spacing: 1px;
    text-transform: uppercase;
  }
  .source-tag .count { color: var(--lcars-tan); margin-left: 6px; }
  .service-tag {
    padding: 4px 14px;
    border-radius: 20px;
    font-size: 13px;
    font-weight: 700;
    border: 1px solid;
    letter-spacing: 1px;
    text-transform: uppercase;
  }
  .service-tag.ready { background: rgba(153,204,102,0.1); border-color: var(--lcars-ok); color: var(--lcars-ok); }
  .service-tag.pending { background: rgba(255,153,0,0.1); border-color: var(--lcars-warning); color: var(--lcars-warning); }
  .service-tag.pending_delete { background: rgba(204,102,102,0.1); border-color: var(--lcars-critical); color: var(--lcars-critical); }

  .empty-state { padding: 24px; text-align: center; color: var(--lcars-tan); font-size: 14px; letter-spacing: 1px; }

  .mono { font-family: 'SF Mono', SFMono-Regular, Menlo, monospace; font-size: 12px; }
  .duration { color: var(--lcars-tan); font-size: 11px; }
  .icinga-ok { color: var(--lcars-ok); font-weight: 700; }
  .icinga-fail { color: var(--lcars-red); font-weight: 700; }

  /* ── Buttons ── */
  .btn {
    padding: 6px 16px;
    border: none;
    border-radius: 20px;
    font-family: 'Antonio', sans-serif;
    font-size: 13px;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 1px;
    cursor: pointer;
    transition: filter 0.15s;
    color: #000;
  }
  .btn:hover { filter: brightness(1.2); }
  .btn:disabled { opacity: 0.3; cursor: not-allowed; }
  .btn-danger { background: var(--lcars-red); }
  .btn-primary { background: var(--lcars-blue); }
  .btn-sm { padding: 3px 10px; font-size: 11px; }

  .toolbar {
    display: flex;
    gap: 8px;
    align-items: center;
    padding: 10px 0;
    border-bottom: 1px solid rgba(255,153,0,0.15);
    margin-bottom: 8px;
  }
  .toolbar label { font-size: 12px; color: var(--lcars-tan); letter-spacing: 1px; text-transform: uppercase; }
  .checkbox-cell { width: 30px; text-align: center; }
  input[type="checkbox"] { cursor: pointer; accent-color: var(--lcars-orange); }

  .admin-badge {
    background: var(--lcars-blue);
    color: #000;
    padding: 2px 10px;
    border-radius: 10px;
    font-size: 11px;
    font-weight: 700;
    letter-spacing: 2px;
    text-transform: uppercase;
    margin-left: 8px;
  }

  /* ── Toast ── */
  .toast {
    position: fixed;
    bottom: 48px;
    right: 24px;
    padding: 12px 24px;
    border-radius: 20px;
    font-family: 'Antonio', sans-serif;
    font-size: 14px;
    font-weight: 700;
    letter-spacing: 1px;
    text-transform: uppercase;
    z-index: 1000;
    opacity: 0;
    transition: opacity 0.3s;
    max-width: 400px;
    color: #000;
  }
  .toast.show { opacity: 1; }
  .toast.success { background: var(--lcars-ok); }
  .toast.error { background: var(--lcars-red); }

  /* ── Blinking indicators ── */
  @keyframes lcars-blink {
    0%, 100% { opacity: 1; }
    50% { opacity: 0.3; }
  }
  .blink { animation: lcars-blink 2s infinite; }
  .blink-fast { animation: lcars-blink 0.8s infinite; }

  /* ── Activity bar (mini chart) ── */
  .activity-bar {
    display: flex;
    align-items: flex-end;
    gap: 2px;
    height: 40px;
    padding: 4px 0;
  }
  .activity-bar .bar {
    flex: 1;
    background: var(--lcars-orange);
    border-radius: 2px 2px 0 0;
    min-width: 4px;
    transition: height 0.3s;
    opacity: 0.7;
  }
  .activity-bar .bar:hover { opacity: 1; }

  /* ── Horizontal scanner line ── */
  .scanner-line {
    width: 100%;
    height: 2px;
    background: linear-gradient(90deg, transparent 0%, var(--lcars-blue) 40%, var(--lcars-blue) 60%, transparent 100%);
    opacity: 0.4;
    margin: 12px 0;
    animation: scanner-sweep 3s ease-in-out infinite;
  }
  @keyframes scanner-sweep {
    0%, 100% { opacity: 0.1; }
    50% { opacity: 0.6; }
  }

  /* ── Status indicator dots ── */
  .status-dot {
    width: 8px; height: 8px;
    border-radius: 50%;
    display: inline-block;
    margin-right: 6px;
  }
  .status-dot.green { background: var(--lcars-ok); }
  .status-dot.orange { background: var(--lcars-orange); }
  .status-dot.red { background: var(--lcars-red); animation: lcars-blink 1s infinite; }

  /* ── Responsive ── */
  @media (max-width: 768px) {
    .lcars-frame {
      grid-template-columns: 1fr;
      grid-template-rows: auto 1fr auto;
    }
    .lcars-sidebar { display: none; }
    .lcars-header-cap { display: none; }
    .lcars-footer-cap { display: none; }
    .lcars-panel-elbow { width: 30px; }
    .lcars-panel-body { padding-left: 40px; }
    :root { --lcars-sidebar-w: 0px; }
  }

  /* ── Nav sections ── */
  .nav-section { display: none; }
  .nav-section.active { display: block; }
</style>
</head>
<body>

<div class="lcars-frame">

  <!-- ══════ TOP HEADER BAR ══════ -->
  <div class="lcars-header">
    <div class="lcars-header-cap"><span>LCARS</span></div>
    <div class="lcars-header-bar">
      <div class="bar-segment bar-seg-1">Stardate {{.GeneratedAt}}</div>
      <div class="bar-segment bar-seg-2">{{.Uptime}}</div>
      <div class="bar-segment bar-seg-3">IcingaAlertForge{{if .IsAdmin}} <span style="font-size:12px; letter-spacing:2px;">[COMMAND ACCESS]</span>{{end}}</div>
      <div class="bar-segment bar-seg-4">
        {{if not .IsAdmin}}<a href="?admin=1" style="color:#000;text-decoration:none;">AUTH</a>{{else}}<a href="#" onclick="doLogout();return false;" style="color:#000;text-decoration:none;">LOGOUT</a>{{end}}
      </div>
      <div class="bar-segment bar-seg-5">V1.0</div>
    </div>
  </div>

  <!-- ══════ LEFT SIDEBAR ══════ -->
  <div class="lcars-sidebar">
    <button class="sidebar-btn active" onclick="showSection('overview')">Overview</button>
    <button class="sidebar-btn tan" onclick="showSection('alerts')">Alerts</button>
    <button class="sidebar-btn purple" onclick="showSection('errors')">Errors</button>
    <button class="sidebar-btn blue" onclick="showSection('services')">Services</button>
    {{if .IsAdmin}}
    <div class="sidebar-decoration"></div>
    <button class="sidebar-btn gold" onclick="showSection('system')">System</button>
    <button class="sidebar-btn peach" onclick="showSection('security')">Security</button>
    <button class="sidebar-btn" onclick="showSection('icinga')">Icinga Mgmt</button>
    {{end}}
    <div class="sidebar-decoration purple"></div>
    <div class="sidebar-spacer"></div>
    <div class="sidebar-decoration blue"></div>
    <button class="sidebar-btn tan" style="font-size:11px;padding:8px 10px;">Auto-Refresh 30s</button>
  </div>

  <!-- ══════ MAIN CONTENT ══════ -->
  <div class="lcars-content">

    <!-- ── OVERVIEW SECTION ── -->
    <div class="nav-section active" id="sec-overview">

      <!-- Summary Stats -->
      <div class="lcars-panel">
        <div class="lcars-panel-header">
          <div class="lcars-panel-elbow"></div>
          <div class="lcars-panel-title-bar">
            <div class="bar-fill"></div>
            <span class="title-text">Operations Summary</span>
          </div>
        </div>
        <div class="lcars-panel-body">
          <div class="stat-grid">
            <div class="stat-cell">
              <div class="stat-label">Total Webhooks</div>
              <div class="stat-value">{{.Stats.TotalEntries}}</div>
            </div>
            <div class="stat-cell {{if gt .Stats.ErrorCount 0}}critical{{end}}">
              <div class="stat-label">Errors</div>
              <div class="stat-value {{if gt .Stats.ErrorCount 0}}error{{else}}ok{{end}}">{{.Stats.ErrorCount}}</div>
            </div>
            <div class="stat-cell blue">
              <div class="stat-label">Avg Duration</div>
              <div class="stat-value blue">{{.Stats.AvgDurationMs}}ms</div>
            </div>
            <div class="stat-cell purple">
              <div class="stat-label">Cached Services</div>
              <div class="stat-value purple">{{len .CachedServices}}</div>
            </div>
          </div>
        </div>
      </div>

      <!-- Mode / Severity Breakdown -->
      <div class="lcars-panel purple">
        <div class="lcars-panel-header">
          <div class="lcars-panel-elbow purple"></div>
          <div class="lcars-panel-title-bar">
            <div class="bar-fill"></div>
            <span class="title-text purple">Tactical Analysis</span>
          </div>
        </div>
        <div class="lcars-panel-body">
          <div class="stat-grid">
            <div class="stat-cell ok">
              <div class="stat-label">Work Mode</div>
              <div class="stat-value ok">{{index .Stats.ByMode "work"}}</div>
            </div>
            <div class="stat-cell purple">
              <div class="stat-label">Test Mode</div>
              <div class="stat-value purple">{{index .Stats.ByMode "test"}}</div>
            </div>
            <div class="stat-cell critical">
              <div class="stat-label">Critical Alerts</div>
              <div class="stat-value critical">{{index .Stats.BySeverity "critical"}}</div>
            </div>
            <div class="stat-cell warning">
              <div class="stat-label">Warning Alerts</div>
              <div class="stat-value warning">{{index .Stats.BySeverity "warning"}}</div>
            </div>
          </div>
          <div class="scanner-line"></div>
          <div style="display:flex;gap:20px;font-size:13px;color:var(--lcars-tan);letter-spacing:1px;text-transform:uppercase;">
            <span><span class="status-dot green"></span> Systems Nominal</span>
            <span><span class="status-dot {{if gt .Stats.ErrorCount 0}}red{{else}}green{{end}}"></span> Error Detection</span>
            <span><span class="status-dot orange blink"></span> Monitoring Active</span>
          </div>
        </div>
      </div>

      <!-- Sources -->
      <div class="lcars-panel blue">
        <div class="lcars-panel-header">
          <div class="lcars-panel-elbow blue"></div>
          <div class="lcars-panel-title-bar">
            <div class="bar-fill"></div>
            <span class="title-text blue">Signal Sources</span>
          </div>
        </div>
        <div class="lcars-panel-body">
          {{if .Stats.BySource}}
          <div class="tag-list">
            {{range $source, $count := .Stats.BySource}}
            <span class="source-tag">{{$source}} <span class="count">({{$count}})</span></span>
            {{end}}
          </div>
          {{else}}
          <div class="empty-state">No signal sources detected</div>
          {{end}}
        </div>
      </div>

    </div><!-- /overview -->

    <!-- ── ALERTS SECTION ── -->
    <div class="nav-section" id="sec-alerts">
      <div class="lcars-panel tan">
        <div class="lcars-panel-header">
          <div class="lcars-panel-elbow tan"></div>
          <div class="lcars-panel-title-bar">
            <div class="bar-fill"></div>
            <span class="title-text tan">Recent Transmissions (Last 20)</span>
          </div>
        </div>
        <div class="lcars-panel-body">
          {{if .RecentAlerts}}
          <table id="alertsTable">
            <thead>
              <tr>
                <th onclick="sortTable(0,'date',this.closest('table'))">Stardate <span class="sort-arrow"></span></th>
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
          <div class="empty-state">No transmissions recorded</div>
          {{end}}
        </div>
      </div>
    </div><!-- /alerts -->

    <!-- ── ERRORS SECTION ── -->
    <div class="nav-section" id="sec-errors">
      <div class="lcars-panel red">
        <div class="lcars-panel-header">
          <div class="lcars-panel-elbow red"></div>
          <div class="lcars-panel-title-bar">
            <div class="bar-fill"></div>
            <span class="title-text red">Anomaly Log (Last 10)</span>
          </div>
        </div>
        <div class="lcars-panel-body">
          {{if .RecentErrors}}
          <table>
            <thead>
              <tr>
                <th>Stardate</th>
                <th>Service</th>
                <th>Action</th>
                <th>Source</th>
                <th>Anomaly Details</th>
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
                <td style="color: var(--lcars-red); max-width: 400px; overflow: hidden; text-overflow: ellipsis;">{{if .Error}}{{.Error}}{{else}}{{.Message}}{{end}}</td>
                <td class="duration">{{.DurationMs}}ms</td>
              </tr>
              {{end}}
            </tbody>
          </table>
          {{else}}
          <div class="empty-state" style="color: var(--lcars-ok);">All systems operating within normal parameters</div>
          {{end}}
        </div>
      </div>
    </div><!-- /errors -->

    <!-- ── SERVICES SECTION ── -->
    <div class="nav-section" id="sec-services">
      <div class="lcars-panel blue">
        <div class="lcars-panel-header">
          <div class="lcars-panel-elbow blue"></div>
          <div class="lcars-panel-title-bar">
            <div class="bar-fill"></div>
            <span class="title-text blue">Service Cache Registry</span>
          </div>
        </div>
        <div class="lcars-panel-body">
          {{if .CachedServices}}
          <div class="tag-list">
            {{range $name, $state := .CachedServices}}
            <span class="service-tag {{$state}}">{{$name}} ({{$state}})</span>
            {{end}}
          </div>
          {{else}}
          <div class="empty-state">No services in cache memory</div>
          {{end}}
        </div>
      </div>
    </div><!-- /services -->

    {{if .IsAdmin}}
    <!-- ── SYSTEM SECTION ── -->
    <div class="nav-section" id="sec-system">
      <div class="lcars-panel gold">
        <div class="lcars-panel-header">
          <div class="lcars-panel-elbow gold"></div>
          <div class="lcars-panel-title-bar">
            <div class="bar-fill" style="background:var(--lcars-gold);"></div>
            <span class="title-text" style="color:var(--lcars-gold);">Engineering - Core Systems</span>
          </div>
        </div>
        <div class="lcars-panel-body">
          <div class="stat-grid">
            <div class="stat-cell">
              <div class="stat-label">CPU Cores</div>
              <div class="stat-value">{{.SysStats.NumCPU}}</div>
            </div>
            <div class="stat-cell {{if gt .SysStats.GoRoutines 100}}warning{{end}}">
              <div class="stat-label">Goroutines</div>
              <div class="stat-value {{if gt .SysStats.GoRoutines 100}}warning{{else}}ok{{end}}">{{.SysStats.GoRoutines}}</div>
            </div>
            <div class="stat-cell blue">
              <div class="stat-label">Memory (Alloc)</div>
              <div class="stat-value blue">{{printf "%.1f" .SysStats.MemAllocMB}} MB</div>
            </div>
            <div class="stat-cell blue">
              <div class="stat-label">Memory (Sys)</div>
              <div class="stat-value blue">{{printf "%.1f" .SysStats.MemSysMB}} MB</div>
            </div>
            <div class="stat-cell purple">
              <div class="stat-label">Heap</div>
              <div class="stat-value purple">{{printf "%.1f" .SysStats.MemHeapMB}} MB</div>
            </div>
            <div class="stat-cell purple">
              <div class="stat-label">Stack</div>
              <div class="stat-value purple">{{printf "%.2f" .SysStats.MemStackMB}} MB</div>
            </div>
            <div class="stat-cell">
              <div class="stat-label">GC Cycles</div>
              <div class="stat-value">{{.SysStats.GCRuns}}</div>
            </div>
            <div class="stat-cell">
              <div class="stat-label">GC Pause Total</div>
              <div class="stat-value">{{printf "%.1f" .SysStats.GCPauseTotalMs}} ms</div>
            </div>
          </div>
          <div class="scanner-line"></div>
        </div>
      </div>

      <div class="lcars-panel">
        <div class="lcars-panel-header">
          <div class="lcars-panel-elbow"></div>
          <div class="lcars-panel-title-bar">
            <div class="bar-fill"></div>
            <span class="title-text">Performance Telemetry</span>
          </div>
        </div>
        <div class="lcars-panel-body">
          <div class="stat-grid">
            <div class="stat-cell">
              <div class="stat-label">Total Requests</div>
              <div class="stat-value">{{.SysStats.TotalRequests}}</div>
            </div>
            <div class="stat-cell {{if gt .SysStats.TotalErrors 0}}critical{{end}}">
              <div class="stat-label">Total Errors</div>
              <div class="stat-value {{if gt .SysStats.TotalErrors 0}}error{{else}}ok{{end}}">{{.SysStats.TotalErrors}}</div>
            </div>
            <div class="stat-cell {{if gt .SysStats.ErrorRate 5.0}}critical{{else if gt .SysStats.ErrorRate 1.0}}warning{{end}}">
              <div class="stat-label">Error Rate</div>
              <div class="stat-value {{if gt .SysStats.ErrorRate 5.0}}critical{{else if gt .SysStats.ErrorRate 1.0}}warning{{else}}ok{{end}}">{{printf "%.1f" .SysStats.ErrorRate}}%</div>
            </div>
            <div class="stat-cell {{if gt .SysStats.AvgLatencyMs 500.0}}critical{{else if gt .SysStats.AvgLatencyMs 200.0}}warning{{end}}">
              <div class="stat-label">Avg Latency</div>
              <div class="stat-value {{if gt .SysStats.AvgLatencyMs 500.0}}critical{{else if gt .SysStats.AvgLatencyMs 200.0}}warning{{else}}ok{{end}}">{{printf "%.0f" .SysStats.AvgLatencyMs}} ms</div>
            </div>
            <div class="stat-cell blue">
              <div class="stat-label">Requests/min</div>
              <div class="stat-value blue">{{printf "%.1f" .SysStats.RequestsPerMin}}</div>
            </div>
            <div class="stat-cell">
              <div class="stat-label">Uptime</div>
              <div class="stat-value" style="font-size:16px;">{{.SysStats.Uptime}}</div>
            </div>
          </div>
        </div>
      </div>
    </div><!-- /system -->

    <!-- ── SECURITY SECTION ── -->
    <div class="nav-section" id="sec-security">
      <div class="lcars-panel red">
        <div class="lcars-panel-header">
          <div class="lcars-panel-elbow red"></div>
          <div class="lcars-panel-title-bar">
            <div class="bar-fill"></div>
            <span class="title-text red">Tactical Security</span>
          </div>
        </div>
        <div class="lcars-panel-body">
          <div class="stat-grid">
            <div class="stat-cell {{if gt .SysStats.FailedAuthTotal 0}}warning{{end}}">
              <div class="stat-label">Failed Auth (Total)</div>
              <div class="stat-value {{if gt .SysStats.FailedAuthTotal 0}}warning{{else}}ok{{end}}">{{.SysStats.FailedAuthTotal}}</div>
            </div>
            <div class="stat-cell {{if .SysStats.BruteForceIPs}}critical{{end}}">
              <div class="stat-label">Brute Force IPs</div>
              <div class="stat-value {{if .SysStats.BruteForceIPs}}critical{{else}}ok{{end}}">{{len .SysStats.BruteForceIPs}}</div>
            </div>
          </div>

          {{if .SysStats.BruteForceIPs}}
          <div class="scanner-line" style="background:linear-gradient(90deg,transparent,var(--lcars-red),transparent);"></div>
          <h3 style="color:var(--lcars-red);font-size:14px;letter-spacing:2px;text-transform:uppercase;margin-bottom:8px;">Intruder Alert</h3>
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
                <td class="mono" style="color:var(--lcars-red);"><strong>{{.IP}}</strong></td>
                <td><span class="badge critical">{{.Attempts}}</span></td>
                <td class="mono">{{.LastSeen}}</td>
              </tr>
              {{end}}
            </tbody>
          </table>
          {{end}}

          {{if .SysStats.FailedAuthRecent}}
          <div style="margin-top:16px;">
            <h3 style="color:var(--lcars-tan);font-size:14px;letter-spacing:2px;text-transform:uppercase;margin-bottom:8px;">Recent Auth Failures (Last Hour)</h3>
            <table>
              <thead>
                <tr>
                  <th>Stardate</th>
                  <th>IP Address</th>
                  <th>Key Used</th>
                </tr>
              </thead>
              <tbody>
                {{range .SysStats.FailedAuthRecent}}
                <tr>
                  <td class="mono">{{.Timestamp.Format "2006-01-02 15:04:05"}}</td>
                  <td class="mono">{{.IP}}</td>
                  <td class="mono" style="color:var(--lcars-warning);">{{.KeyPrefix}}</td>
                </tr>
                {{end}}
              </tbody>
            </table>
          </div>
          {{end}}
        </div>
      </div>
    </div><!-- /security -->

    <!-- ── ICINGA MANAGEMENT SECTION ── -->
    <div class="nav-section" id="sec-icinga">
      <div class="lcars-panel">
        <div class="lcars-panel-header">
          <div class="lcars-panel-elbow"></div>
          <div class="lcars-panel-title-bar">
            <div class="bar-fill"></div>
            <span class="title-text">Icinga2 Services - "{{.HostName}}" [Command Level]</span>
          </div>
        </div>
        <div class="lcars-panel-body">
          {{if .IcingaServices}}
          <div class="toolbar">
            <input type="checkbox" id="selectAll" onclick="toggleAll(this)">
            <label for="selectAll">Select All</label>
            <button class="btn btn-danger btn-sm" onclick="deleteSelected()" id="btnDeleteSelected" disabled>Delete Selected</button>
            <span style="margin-left:auto; font-size:12px; color:var(--lcars-tan); letter-spacing:1px;">{{len .IcingaServices}} service(s) registered</span>
          </div>
          <table id="servicesTable">
            <thead>
              <tr>
                <th class="checkbox-cell"></th>
                <th onclick="sortTable(1,'string')">Designation <span class="sort-arrow"></span></th>
                <th onclick="sortTable(2,'string')">Display <span class="sort-arrow"></span></th>
                <th onclick="sortTable(3,'string')">Status <span class="sort-arrow"></span></th>
                <th onclick="sortTable(4,'string')">Output <span class="sort-arrow"></span></th>
                <th onclick="sortTable(5,'date')">Last Scan <span class="sort-arrow"></span></th>
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
                <td><button class="btn btn-danger btn-sm" onclick="deleteService('{{.Name}}', this)">Purge</button></td>
              </tr>
              {{end}}
            </tbody>
          </table>
          {{else}}
          <div class="empty-state">No services registered on host "{{.HostName}}"</div>
          {{end}}
        </div>
      </div>
    </div><!-- /icinga -->
    {{end}}

  </div><!-- /lcars-content -->

  <!-- ══════ FOOTER BAR ══════ -->
  <div class="lcars-footer">
    <div class="lcars-footer-cap"></div>
    <div class="lcars-footer-bar">
      <div class="foot-1">IcingaAlertForge // Grafana > Webhook Bridge > Icinga2</div>
      <div class="foot-2"></div>
      <div class="foot-3"></div>
      <div class="foot-4"></div>
    </div>
  </div>

</div><!-- /lcars-frame -->

<div class="toast" id="toast"></div>

<script>
// ── Navigation ──
function showSection(name) {
  document.querySelectorAll('.nav-section').forEach(s => s.classList.remove('active'));
  const sec = document.getElementById('sec-' + name);
  if (sec) sec.classList.add('active');

  document.querySelectorAll('.lcars-sidebar .sidebar-btn').forEach(b => b.classList.remove('active'));
  event.target.classList.add('active');
}

// ── Table sorting ──
function sortTable(colIdx, type, tableEl) {
  const table = tableEl || document.getElementById('servicesTable');
  if (!table) return;
  const tbody = table.querySelector('tbody');
  const rows = Array.from(tbody.querySelectorAll('tr'));
  const th = table.querySelectorAll('thead th')[colIdx];

  const dir = th.dataset.sortDir === 'asc' ? 'desc' : 'asc';
  table.querySelectorAll('thead th').forEach(h => { h.dataset.sortDir = ''; });
  th.dataset.sortDir = dir;

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
function showToast(msg, type) {
  const t = document.getElementById('toast');
  t.textContent = msg;
  t.className = 'toast show ' + type;
  setTimeout(() => t.className = 'toast', 3000);
}

function deleteService(name, btn) {
  if (!confirm('Confirm purge of service "' + name + '" from Icinga2?')) return;
  btn.disabled = true;
  btn.textContent = '...';

  fetch('/admin/services/' + encodeURIComponent(name), {
    method: 'DELETE',
  }).then(r => r.json()).then(data => {
    if (data.status === 'deleted') {
      showToast('Purged: ' + name, 'success');
      const row = btn.closest('tr');
      if (row) row.remove();
    } else {
      showToast('Error: ' + (data.error || 'unknown'), 'error');
      btn.disabled = false;
      btn.textContent = 'Purge';
    }
  }).catch(err => {
    showToast('Error: ' + err.message, 'error');
    btn.disabled = false;
    btn.textContent = 'Purge';
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
    btn.textContent = checked > 0 ? 'Purge Selected (' + checked + ')' : 'Delete Selected';
  }
}

document.addEventListener('change', function(e) {
  if (e.target.classList.contains('svc-check')) updateBulkBtn();
});

function deleteSelected() {
  const checked = Array.from(document.querySelectorAll('.svc-check:checked'));
  const names = checked.map(cb => cb.value);
  if (names.length === 0) return;
  if (!confirm('Confirm purge of ' + names.length + ' service(s) from Icinga2?')) return;

  const btn = document.getElementById('btnDeleteSelected');
  btn.disabled = true;
  btn.textContent = 'Purging...';

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
      showToast('Purged ' + deleted + ', errors: ' + errors, 'error');
    } else {
      showToast('Purged ' + deleted + ' service(s)', 'success');
    }
    updateBulkBtn();
  }).catch(err => {
    showToast('Error: ' + err.message, 'error');
    btn.disabled = false;
    btn.textContent = 'Delete Selected';
  });
}
{{end}}

function doLogout() {
  window.location.href = '/status/beauty/logout';
}

// ── Preserve section on auto-refresh ──
(function() {
  const hash = window.location.hash.replace('#','');
  if (hash) {
    const sec = document.getElementById('sec-' + hash);
    if (sec) {
      document.querySelectorAll('.nav-section').forEach(s => s.classList.remove('active'));
      sec.classList.add('active');
    }
  }
  document.querySelectorAll('.lcars-sidebar .sidebar-btn[onclick]').forEach(btn => {
    const orig = btn.getAttribute('onclick');
    const m = orig.match(/showSection\('(\w+)'\)/);
    if (m) {
      btn.addEventListener('click', () => { window.location.hash = m[1]; });
    }
  });
})();
</script>

</body>
</html>`

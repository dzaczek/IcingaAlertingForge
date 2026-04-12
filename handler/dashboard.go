package handler

import (
	"bytes"
	"crypto/subtle"
	"fmt"
	"html/template"
	"icinga-webhook-bridge/httputil"
	"log/slog"
	"net/http"
	"sort"
	"sync"
	"time"

	"icinga-webhook-bridge/audit"
	"icinga-webhook-bridge/cache"
	"icinga-webhook-bridge/config"
	"icinga-webhook-bridge/health"
	"icinga-webhook-bridge/history"
	"icinga-webhook-bridge/icinga"
	"icinga-webhook-bridge/metrics"
	"icinga-webhook-bridge/models"
	"icinga-webhook-bridge/queue"
	"icinga-webhook-bridge/rbac"
)

// DashboardHandler serves the GET /status/beauty HTML dashboard
// with live statistics, recent alerts, and error information.
type DashboardHandler struct {
	Cache             *cache.ServiceCache
	History           *history.Logger
	API               *icinga.APIClient
	Metrics           *metrics.Collector
	Targets           map[string]config.TargetConfig
	AdminUser         string
	AdminPass         string
	Version           string
	StartedAt         time.Time
	DebugRing         *icinga.DebugRing
	ConfigInDashboard bool
	RetryQueue        *queue.Queue
	HealthChecker     *health.Checker
	Audit             *audit.Logger
	RBAC              *rbac.Manager
}

// ipEntry represents one IP address entry for template display.
type ipEntry struct {
	IP       string
	Count    int
	LastSeen string
}

// dashboardData is the template context for the beauty dashboard.
type dashboardData struct {
	GeneratedAt       string
	Uptime            string
	Version           string
	Stats             history.HistoryStats
	SourceIPs         map[string]map[string]int
	SourceTopIPs      map[string][]ipEntry // top 10 by count
	SourceLastIPs     map[string][]ipEntry // last 10 by time
	CachedServices    []cache.CacheEntry
	RecentAlerts      []dashboardAlert
	RecentErrors      []dashboardAlert
	IsAdmin           bool
	IsOperator        bool
	CanManageConfig   bool
	CanDeleteService  bool
	CanChangeStatus   bool
	CanManageUsers    bool
	PrimaryAdmin      string
	ConfigInDashboard bool
	IcingaServices    []icinga.ServiceInfo
	HostLabel         string
	SysStats          metrics.SystemStats
	QueueStats        *queue.Stats
	HealthStatus      *health.Status
	AuditEnabled      bool
	UserRole          string
}

type dashboardAlert struct {
	Timestamp   string
	RequestID   string
	Source      string
	HostName    string
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
	if e.Mode == "manual" {
		statusLabel = statusLabel + " [MANUAL]"
		statusClass = statusClass + " manual"
	}
	if !e.IcingaOK || e.Error != "" {
		statusClass = "error"
	}

	return dashboardAlert{
		Timestamp:   e.Timestamp.Format("2006-01-02 15:04:05 UTC"),
		RequestID:   e.RequestID,
		Source:      e.SourceKey,
		HostName:    e.HostName,
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

// isAdmin checks if the request has valid credentials (primary admin or RBAC user).
func (h *DashboardHandler) isAdmin(r *http.Request) bool {
	if h.AdminPass == "" {
		return false
	}
	user, pass, ok := r.BasicAuth()
	if !ok {
		return false
	}
	// Check primary admin credentials
	userOK := subtle.ConstantTimeCompare([]byte(user), []byte(h.AdminUser)) == 1
	passOK := subtle.ConstantTimeCompare([]byte(pass), []byte(h.AdminPass)) == 1
	if userOK && passOK {
		return true
	}
	// Check RBAC users
	if h.RBAC != nil {
		if _, authenticated := h.RBAC.Authenticate(user, pass); authenticated {
			return true
		}
	}
	return false
}

// buildSourceIPLists creates Top 10 (by count) and Last 10 (by time) IP lists per source.
func buildSourceIPLists(stats history.HistoryStats) (topIPs, lastIPs map[string][]ipEntry) {
	topIPs = make(map[string][]ipEntry)
	lastIPs = make(map[string][]ipEntry)

	for source, ipCounts := range stats.BySourceIP {
		var entries []ipEntry
		for ip, count := range ipCounts {
			lastSeen := ""
			if ts, ok := stats.BySourceIPLastSeen[source][ip]; ok {
				lastSeen = ts.Format("2006-01-02 15:04:05 UTC")
			}
			entries = append(entries, ipEntry{IP: ip, Count: count, LastSeen: lastSeen})
		}

		// Top 10 by count (descending)
		top := make([]ipEntry, len(entries))
		copy(top, entries)
		sort.Slice(top, func(i, j int) bool { return top[i].Count > top[j].Count })
		if len(top) > 10 {
			top = top[:10]
		}
		topIPs[source] = top

		// Last 10 by time (most recent first)
		last := make([]ipEntry, len(entries))
		copy(last, entries)
		sort.Slice(last, func(i, j int) bool { return last[i].LastSeen > last[j].LastSeen })
		if len(last) > 10 {
			last = last[:10]
		}
		lastIPs[source] = last
	}

	return topIPs, lastIPs
}

// ServeHTTP renders the beauty dashboard.
func (h *DashboardHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httputil.WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	// Admin mode requires both ?admin=1 query param AND valid credentials.
	// A "_logged_out" cookie is set on logout to force a fresh 401 prompt
	// even when the browser re-sends cached Basic Auth credentials.
	wantAdmin := r.URL.Query().Get("admin") == "1"
	isAdmin := false
	if wantAdmin {
		// If user just logged out, force 401 to get fresh credentials
		if c, err := r.Cookie("_logged_out"); err == nil && c.Value == "1" {
			// Clear the logout cookie so next attempt works normally
			http.SetCookie(w, &http.Cookie{
				Name:     "_logged_out",
				Value:    "",
				Path:     "/",
				MaxAge:   -1,
				HttpOnly: true,
				Secure:   true,
			})
			w.Header().Set("WWW-Authenticate", `Basic realm="Dashboard Admin"`)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `<html><body style="background:#000;color:#fc0;font-family:monospace;padding:40px;text-align:center;"><h2>Enter credentials</h2><p>Authenticate to access command panel.</p></body></html>`)
			return
		}
		if h.isAdmin(r) {
			isAdmin = true
		} else {
			user, _, _ := r.BasicAuth()
			if h.Metrics != nil && user != "" {
				h.Metrics.RecordAuthFailure(r.RemoteAddr, user)
			}
			w.Header().Set("WWW-Authenticate", `Basic realm="Dashboard Admin"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
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
		var mu sync.Mutex
		var wg sync.WaitGroup

		for _, target := range sortedTargets(h.Targets) {
			wg.Add(1)
			go func(targetHostName string) {
				defer wg.Done()
				svcs, err := h.API.ListServices(targetHostName)

				mu.Lock()
				defer mu.Unlock()

				if err != nil {
					slog.Error("Dashboard: failed to list Icinga2 services", "host", targetHostName, "error", err)
					return
				}
				icingaServices = append(icingaServices, svcs...)
			}(target.HostName)
		}
		wg.Wait()

		sort.Slice(icingaServices, func(i, j int) bool {
			if icingaServices[i].HostName == icingaServices[j].HostName {
				return icingaServices[i].Name < icingaServices[j].Name
			}
			return icingaServices[i].HostName < icingaServices[j].HostName
		})
	}

	var sysStats metrics.SystemStats
	if h.Metrics != nil {
		sysStats = h.Metrics.Snapshot()
	}

	sourceTopIPs, sourceLastIPs := buildSourceIPLists(stats)

	// Determine user role and permissions
	var userRole rbac.Role
	if isAdmin {
		authUser, _, _ := r.BasicAuth()
		isPrimaryAdmin := subtle.ConstantTimeCompare([]byte(authUser), []byte(h.AdminUser)) == 1
		if isPrimaryAdmin {
			userRole = rbac.RoleAdmin
		} else if h.RBAC != nil {
			if u, ok := h.RBAC.GetUser(authUser); ok {
				userRole = u.Role
			}
		}
	}

	canManageConfig := userRole == rbac.RoleAdmin
	canDeleteService := userRole == rbac.RoleAdmin
	canChangeStatus := userRole == rbac.RoleAdmin || userRole == rbac.RoleOperator
	canManageUsers := userRole == rbac.RoleAdmin

	data := dashboardData{
		GeneratedAt:       time.Now().UTC().Format("2006-01-02 15:04:05 UTC"),
		Uptime:            uptime.String(),
		Version:           h.Version,
		Stats:             stats,
		SourceIPs:         stats.BySourceIP,
		SourceTopIPs:      sourceTopIPs,
		SourceLastIPs:     sourceLastIPs,
		CachedServices:    h.Cache.AllEntries(),
		RecentAlerts:      recentAlerts,
		RecentErrors:      recentErrors,
		IsAdmin:           isAdmin,
		IsOperator:        canChangeStatus,
		CanManageConfig:   canManageConfig,
		CanDeleteService:  canDeleteService,
		CanChangeStatus:   canChangeStatus,
		CanManageUsers:    canManageUsers,
		PrimaryAdmin:      h.AdminUser,
		ConfigInDashboard: h.ConfigInDashboard,
		IcingaServices:    icingaServices,
		HostLabel:         firstHostName(targetHostNames(h.Targets)),
		SysStats:          sysStats,
		UserRole:          string(userRole),
	}

	if h.RetryQueue != nil {
		qs := h.RetryQueue.Stats()
		data.QueueStats = &qs
	}

	if h.HealthChecker != nil {
		hs := h.HealthChecker.GetStatus()
		data.HealthStatus = &hs
	}

	if h.Audit != nil {
		data.AuditEnabled = h.Audit.Enabled()
	}

	var buf bytes.Buffer
	if err := dashboardTemplate.Execute(&buf, data); err != nil {
		slog.Error("Dashboard: template render failed", "error", err)
		http.Error(w, "Failed to render dashboard", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Write(buf.Bytes())
}

var dashboardTemplate = template.Must(template.New("dashboard").Parse(dashboardHTML))

const dashboardHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>IAF - IcingaAlertForge</title>
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
    grid-template-rows: var(--lcars-header-h) auto 1fr auto;
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
  .source-tag.clickable { cursor: pointer; transition: filter 0.15s, background 0.15s; }
  .source-tag.clickable:hover { filter: brightness(1.3); background: rgba(153,204,255,0.2); }
  .source-detail {
    margin-top: 8px;
    padding: 10px 16px;
    background: rgba(153,204,255,0.05);
    border: 1px solid rgba(153,204,255,0.15);
    border-radius: 12px;
    margin-bottom: 8px;
  }
  .source-detail-title {
    font-size: 12px;
    text-transform: uppercase;
    letter-spacing: 1.5px;
    color: var(--lcars-blue);
    margin-bottom: 6px;
  }
  .ip-tabs {
    display: flex;
    gap: 4px;
    margin-bottom: 8px;
  }
  .ip-tab {
    padding: 3px 14px;
    border-radius: 12px;
    font-size: 11px;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 1px;
    color: var(--lcars-tan);
    border: 1px solid rgba(153,204,255,0.2);
    cursor: pointer;
    transition: all 0.15s;
  }
  .ip-tab:hover { border-color: var(--lcars-blue); color: var(--lcars-blue); }
  .ip-tab.active { background: rgba(153,204,255,0.15); border-color: var(--lcars-blue); color: var(--lcars-blue); }
  .ip-entry {
    font-size: 13px;
    color: var(--lcars-tan);
    padding: 3px 0;
    letter-spacing: 0.5px;
    display: flex;
    align-items: center;
    gap: 12px;
  }
  .ip-addr {
    font-family: 'SF Mono', SFMono-Regular, Menlo, monospace;
    font-size: 12px;
    color: var(--lcars-blue);
    min-width: 160px;
  }
  .ip-meta {
    font-size: 11px;
    color: rgba(255,204,153,0.6);
    flex: 1;
  }
  .ip-count {
    font-family: 'Orbitron', sans-serif;
    font-size: 11px;
    font-weight: 700;
    color: var(--lcars-orange);
  }
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

  /* ── Service Registry Table ── */
  .svc-registry-table { width: 100%; border-collapse: collapse; font-size: 13px; }
  .svc-registry-table thead th {
    text-align: left;
    padding: 8px 12px;
    color: var(--lcars-blue);
    border-bottom: 2px solid var(--lcars-blue);
    font-weight: 700;
    letter-spacing: 2px;
    text-transform: uppercase;
    font-size: 11px;
    cursor: pointer;
    user-select: none;
  }
  .svc-registry-table thead th:hover { color: var(--lcars-orange); }
  .svc-registry-table thead th .sort-arrow { margin-left: 4px; font-size: 10px; }
  .svc-registry-table tbody td {
    padding: 6px 12px;
    border-bottom: 1px solid rgba(153,204,255,0.08);
    vertical-align: middle;
  }
  .svc-registry-table tbody tr { cursor: pointer; transition: background 0.15s; }
  .svc-registry-table tbody tr:hover { background: rgba(153,204,255,0.08); }
  tr.frozen-row { background: rgba(100,180,255,0.13) !important; }
  tr.frozen-row:hover { background: rgba(100,180,255,0.2) !important; }
  .frozen-inline-badge {
    display: inline-block;
    font-size: 11px;
    margin-left: 5px;
    color: var(--lcars-blue);
    vertical-align: middle;
  }
  .frozen-unfreeze-btn {
    font-family: 'Okuda','Antonio',sans-serif;
    font-size: 10px;
    letter-spacing: 1px;
    text-transform: uppercase;
    background: var(--lcars-yellow);
    color: #000;
    border: none;
    border-radius: 10px;
    padding: 3px 10px;
    cursor: pointer;
    font-weight: 700;
    margin-left: 6px;
    vertical-align: middle;
    transition: opacity 0.15s;
  }
  .frozen-unfreeze-btn:hover { opacity: 0.8; }
  .frozen-unfreeze-btn:disabled { opacity: 0.4; cursor: not-allowed; }
  .svc-registry-table tbody tr:last-child td { border-bottom: none; }
  .svc-registry-table .svc-host-cell { color: var(--lcars-tan); font-family: 'Okuda','Antonio',monospace; font-size: 12px; }
  .svc-registry-table .svc-name-cell { color: var(--lcars-blue); font-weight: 700; letter-spacing: 1px; }
  .svc-registry-table .svc-state-badge {
    display: inline-block;
    padding: 2px 12px;
    border-radius: 12px;
    font-size: 11px;
    font-weight: 700;
    letter-spacing: 1.5px;
    text-transform: uppercase;
    border: 1px solid;
  }
  .svc-state-badge.ready { background: rgba(153,204,102,0.15); border-color: var(--lcars-ok); color: var(--lcars-ok); }
  .svc-state-badge.pending { background: rgba(255,153,0,0.15); border-color: var(--lcars-warning); color: var(--lcars-warning); }
  .svc-state-badge.pending_delete { background: rgba(204,102,102,0.15); border-color: var(--lcars-critical); color: var(--lcars-critical); }
  .svc-registry-count {
    text-align: right;
    padding: 8px 12px 0 0;
    color: var(--lcars-tan);
    font-size: 11px;
    letter-spacing: 1px;
  }
  .svc-host-divider td {
    padding: 10px 12px 4px 12px;
    color: var(--lcars-gold);
    font-weight: 700;
    font-size: 12px;
    letter-spacing: 2px;
    text-transform: uppercase;
    border-bottom: 1px solid rgba(255,168,0,0.3);
  }

  .empty-state { padding: 24px; text-align: center; color: var(--lcars-tan); font-size: 14px; letter-spacing: 1px; }

  /* ── Service History Popup ── */
  .svc-history-overlay {
    position: fixed;
    top: 0; left: 0; right: 0; bottom: 0;
    background: rgba(0,0,0,0.7);
    z-index: 9999;
    display: flex;
    align-items: center;
    justify-content: center;
  }
  .svc-history-panel {
    background: var(--lcars-bg);
    border: 2px solid var(--lcars-blue);
    border-radius: 16px;
    width: 720px;
    max-width: 92vw;
    max-height: 85vh;
    display: flex;
    flex-direction: column;
    padding: 0;
  }
  .svc-history-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 12px 20px;
    border-bottom: 2px solid var(--lcars-blue);
    background: rgba(153,153,255,0.08);
    border-radius: 14px 14px 0 0;
  }
  .svc-history-title {
    color: var(--lcars-blue);
    font-size: 16px;
    font-weight: 700;
    letter-spacing: 1px;
    text-transform: uppercase;
  }
  .svc-history-close {
    background: var(--lcars-critical);
    color: #000;
    border: none;
    border-radius: 6px;
    padding: 4px 14px;
    font-weight: 700;
    font-size: 12px;
    cursor: pointer;
    text-transform: uppercase;
    letter-spacing: 1px;
  }
  .svc-history-close:hover { opacity: 0.8; }
  .svc-history-body {
    padding: 16px 20px;
    overflow-y: auto;
    flex: 1;
    min-height: 0;
  }
  .svc-history-body-title {
    color: var(--lcars-tan);
    font-size: 11px;
    letter-spacing: 2px;
    text-transform: uppercase;
    font-weight: 700;
    padding: 0 0 8px 0;
    border-bottom: 1px solid rgba(255,168,0,0.2);
    margin-bottom: 6px;
  }
  /* ── Service Detail Block ── */
  .svc-detail-block {
    padding: 12px 20px;
    border-bottom: 1px solid rgba(153,204,255,0.15);
    flex-shrink: 0;
  }
  .svc-detail-grid {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 6px 20px;
  }
  .svc-detail-item {
    display: flex;
    gap: 8px;
    align-items: baseline;
  }
  .svc-detail-label {
    color: var(--lcars-tan);
    font-size: 10px;
    letter-spacing: 1.5px;
    text-transform: uppercase;
    white-space: nowrap;
    min-width: 80px;
  }
  .svc-detail-value {
    color: var(--lcars-blue);
    font-size: 13px;
    font-weight: 700;
    letter-spacing: 0.5px;
    word-break: break-word;
  }
  .svc-detail-value.ok { color: var(--lcars-ok); }
  .svc-detail-value.warning { color: var(--lcars-warning); }
  .svc-detail-value.critical { color: var(--lcars-critical); }
  .svc-detail-value.unknown { color: var(--lcars-purple); }
  .svc-detail-loading {
    color: var(--lcars-blue);
    font-size: 12px;
    letter-spacing: 1px;
    padding: 8px 0;
  }
  .svc-history-row {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 8px 0;
    border-bottom: 1px solid rgba(153,153,255,0.1);
    flex-wrap: wrap;
  }
  .svc-history-row:last-child { border-bottom: none; }
  .svc-history-time {
    color: rgba(255,204,153,0.6);
    font-size: 11px;
    min-width: 140px;
  }
  .svc-history-action {
    font-weight: 700;
    font-size: 11px;
    padding: 2px 8px;
    border-radius: 4px;
    text-transform: uppercase;
    letter-spacing: 0.5px;
  }
  .svc-history-action-firing { background: var(--lcars-critical); color: #000; }
  .svc-history-action-resolved { background: var(--lcars-ok); color: #000; }
  .svc-history-action-create { background: var(--lcars-blue); color: #000; }
  .svc-history-action-delete { background: var(--lcars-warning); color: #000; }
  .svc-history-action-status_change { background: var(--lcars-purple); color: #fff; }
  .svc-history-manual {
    font-size: 10px;
    letter-spacing: 1px;
    color: var(--lcars-purple);
    border: 1px solid var(--lcars-purple);
    border-radius: 6px;
    padding: 1px 6px;
    margin-left: 4px;
  }
  .badge.manual { border: 2px solid var(--lcars-purple); }
  .alerts-manual-tag {
    font-size: 10px;
    letter-spacing: 1px;
    color: var(--lcars-purple);
    font-weight: 700;
  }
  .svc-link {
    cursor: pointer;
    color: var(--lcars-blue);
    text-decoration: underline dotted;
    transition: color 0.15s;
  }
  .svc-link:hover { color: var(--lcars-yellow); }
  .svc-history-msg {
    color: var(--lcars-text-light);
    font-size: 12px;
    flex: 1;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .svc-history-exit {
    font-size: 11px;
    font-weight: 700;
    padding: 1px 6px;
    border-radius: 4px;
  }
  .svc-history-exit-0 { color: var(--lcars-ok); border: 1px solid var(--lcars-ok); }
  .svc-history-exit-1 { color: var(--lcars-warning); border: 1px solid var(--lcars-warning); }
  .svc-history-exit-2 { color: var(--lcars-critical); border: 1px solid var(--lcars-critical); }
  .svc-history-exit-3 { color: var(--lcars-purple); border: 1px solid var(--lcars-purple); }
  .svc-history-empty {
    text-align: center;
    color: var(--lcars-tan);
    padding: 20px;
    font-size: 13px;
    letter-spacing: 1px;
  }
  .svc-history-loading {
    text-align: center;
    color: var(--lcars-blue);
    padding: 20px;
    font-size: 13px;
  }

  /* ── Service Status Buttons ── */
  .svc-status-buttons {
    display: flex;
    gap: 8px;
    padding: 12px 0 4px 0;
    border-top: 1px solid rgba(255,168,0,0.2);
    margin-top: 10px;
    justify-content: center;
  }
  .svc-status-btn {
    font-family: 'Okuda', 'Antonio', sans-serif;
    font-size: 13px;
    letter-spacing: 2px;
    text-transform: uppercase;
    border: none;
    border-radius: 20px;
    padding: 8px 22px;
    cursor: pointer;
    transition: opacity 0.2s, transform 0.1s;
    color: #000;
    font-weight: 700;
  }
  .svc-status-btn:hover { opacity: 0.85; transform: scale(1.04); }
  .svc-status-btn:active { transform: scale(0.96); }
  .svc-status-btn:disabled { opacity: 0.4; cursor: not-allowed; transform: none; }
  .svc-status-btn-ok { background: var(--lcars-ok); }
  .svc-status-btn-warning { background: var(--lcars-warning); }
  .svc-status-btn-critical { background: var(--lcars-critical); }
  .svc-status-result {
    text-align: center;
    font-size: 12px;
    letter-spacing: 1px;
    padding: 6px 0 0 0;
    min-height: 18px;
  }

  /* ── Service Freeze Controls ── */
  .svc-freeze-controls {
    display: flex;
    gap: 8px;
    padding: 10px 0 4px 0;
    border-top: 1px solid rgba(100,180,255,0.2);
    margin-top: 4px;
    align-items: center;
    flex-wrap: wrap;
    justify-content: center;
  }
  .svc-freeze-btn {
    font-family: 'Okuda', 'Antonio', sans-serif;
    font-size: 12px;
    letter-spacing: 2px;
    text-transform: uppercase;
    border: none;
    border-radius: 20px;
    padding: 7px 18px;
    cursor: pointer;
    transition: opacity 0.2s, transform 0.1s;
    font-weight: 700;
    color: #000;
    background: var(--lcars-blue);
  }
  .svc-freeze-btn:hover { opacity: 0.85; transform: scale(1.04); }
  .svc-freeze-btn:active { transform: scale(0.96); }
  .svc-freeze-btn:disabled { opacity: 0.4; cursor: not-allowed; transform: none; }
  .svc-freeze-btn-unfreeze { background: var(--lcars-yellow); }
  .svc-freeze-select {
    font-family: 'Okuda', 'Antonio', sans-serif;
    font-size: 12px;
    letter-spacing: 1px;
    background: #1a1a2e;
    color: var(--lcars-blue);
    border: 1px solid var(--lcars-blue);
    border-radius: 8px;
    padding: 6px 10px;
    cursor: pointer;
  }
  .svc-frozen-badge {
    display: inline-block;
    background: var(--lcars-blue);
    color: #000;
    font-size: 10px;
    font-weight: 700;
    letter-spacing: 2px;
    padding: 2px 8px;
    border-radius: 10px;
    text-transform: uppercase;
    margin-left: 6px;
  }
  .svc-detail-frozen-row {
    grid-column: 1 / -1;
    background: rgba(100,180,255,0.08);
    border: 1px solid rgba(100,180,255,0.3);
    border-radius: 6px;
    padding: 6px 10px;
    font-size: 12px;
    color: var(--lcars-blue);
    letter-spacing: 1px;
  }

  /* ── Table Filter ── */
  .table-filter {
    display: flex;
    align-items: center;
    gap: 10px;
    margin-bottom: 10px;
  }
  .table-filter input {
    flex: 1;
    background: rgba(0,0,0,0.4);
    border: 1px solid var(--lcars-blue);
    border-left: 4px solid var(--lcars-gold);
    border-radius: 4px;
    padding: 8px 14px;
    color: var(--lcars-blue);
    font-family: 'Okuda','Antonio',sans-serif;
    font-size: 13px;
    letter-spacing: 1px;
    outline: none;
    transition: border-color 0.2s;
    -webkit-appearance: none;
    appearance: none;
  }
  .table-filter input::placeholder { color: rgba(153,204,255,0.35); letter-spacing: 2px; }
  .table-filter input:focus { border-color: var(--lcars-orange); border-left-color: var(--lcars-orange); }
  .table-filter-count {
    color: var(--lcars-tan);
    font-size: 11px;
    letter-spacing: 1px;
    white-space: nowrap;
  }

  /* ── Session Timeout Popup ── */
  .session-popup-overlay {
    position: fixed;
    top: 0; left: 0; right: 0; bottom: 0;
    background: rgba(0,0,0,0.75);
    z-index: 99999;
    display: flex;
    align-items: center;
    justify-content: center;
  }
  .session-popup {
    background: var(--lcars-bg);
    border: 2px solid var(--lcars-warning);
    border-radius: 16px;
    width: 420px;
    max-width: 90vw;
    padding: 0;
    overflow: hidden;
    animation: sessionPulse 2s ease-in-out infinite;
  }
  @keyframes sessionPulse {
    0%, 100% { box-shadow: 0 0 20px rgba(255,168,0,0.3); }
    50% { box-shadow: 0 0 40px rgba(255,168,0,0.6); }
  }
  .session-popup-header {
    background: var(--lcars-warning);
    color: #000;
    padding: 12px 20px;
    font-weight: 700;
    font-size: 14px;
    letter-spacing: 3px;
    text-transform: uppercase;
    display: flex;
    align-items: center;
    justify-content: space-between;
  }
  .session-popup-body {
    padding: 20px;
    text-align: center;
  }
  .session-popup-body p {
    color: var(--lcars-text);
    font-size: 14px;
    letter-spacing: 1px;
    margin: 0 0 16px 0;
  }
  .session-countdown {
    font-size: 48px;
    font-weight: 700;
    color: var(--lcars-warning);
    letter-spacing: 4px;
    font-family: 'Okuda','Antonio',monospace;
    margin: 10px 0 16px 0;
  }
  .session-popup-actions {
    display: flex;
    gap: 10px;
    justify-content: center;
    padding-bottom: 4px;
  }
  .session-btn {
    font-family: 'Okuda','Antonio',sans-serif;
    font-size: 13px;
    letter-spacing: 2px;
    text-transform: uppercase;
    border: none;
    border-radius: 20px;
    padding: 8px 24px;
    cursor: pointer;
    font-weight: 700;
    transition: opacity 0.2s;
  }
  .session-btn:hover { opacity: 0.85; }
  .session-btn-extend { background: var(--lcars-ok); color: #000; }
  .session-btn-logout { background: var(--lcars-critical); color: #000; }

  /* ── About Section ── */
  .about-section { padding: 10px 0; }
  .about-section h3 {
    color: var(--lcars-gold);
    font-size: 14px;
    letter-spacing: 3px;
    text-transform: uppercase;
    margin: 20px 0 10px 0;
    padding-bottom: 6px;
    border-bottom: 1px solid rgba(255,168,0,0.25);
  }
  .about-section h3:first-child { margin-top: 0; }
  .about-section p, .about-section li {
    color: var(--lcars-text);
    font-size: 13px;
    line-height: 1.7;
    letter-spacing: 0.5px;
  }
  .about-section ul { padding-left: 20px; margin: 6px 0; }
  .about-section li { margin: 4px 0; }
  .about-section code {
    background: rgba(153,204,255,0.08);
    border: 1px solid rgba(153,204,255,0.15);
    border-radius: 4px;
    padding: 1px 6px;
    font-size: 12px;
    color: var(--lcars-blue);
    font-family: 'Okuda','Antonio',monospace;
  }
  .about-section pre {
    background: rgba(0,0,0,0.4);
    border: 1px solid rgba(153,204,255,0.15);
    border-radius: 8px;
    padding: 12px 16px;
    font-size: 12px;
    color: var(--lcars-blue);
    overflow-x: auto;
    line-height: 1.6;
    margin: 8px 0;
  }
  .about-logo {
    color: var(--lcars-gold);
    font-size: 28px;
    letter-spacing: 4px;
    font-weight: 700;
    text-transform: uppercase;
  }
  .about-version {
    color: var(--lcars-tan);
    font-size: 12px;
    letter-spacing: 2px;
    margin-top: 2px;
  }
  .about-author {
    color: var(--lcars-purple);
    font-weight: 700;
  }
  .about-link {
    color: var(--lcars-blue);
    text-decoration: none;
    border-bottom: 1px solid rgba(153,204,255,0.3);
  }
  .about-link:hover { color: var(--lcars-orange); border-color: var(--lcars-orange); }
  .about-step-num {
    display: inline-block;
    background: var(--lcars-gold);
    color: #000;
    border-radius: 50%;
    width: 20px;
    height: 20px;
    text-align: center;
    font-size: 11px;
    font-weight: 700;
    line-height: 20px;
    margin-right: 6px;
  }
  .foot-2 a {
    color: var(--lcars-blue);
    text-decoration: none;
  }
  .foot-2 a:hover { color: var(--lcars-orange); }

  /* ── Add Target Popup ── */
  .target-popup-overlay {
    position: fixed;
    top: 0; left: 0; right: 0; bottom: 0;
    background: rgba(0,0,0,0.75);
    z-index: 9999;
    display: flex;
    align-items: center;
    justify-content: center;
    animation: info-fade-in 0.2s ease;
  }
  .target-popup {
    background: var(--lcars-bg);
    border: 2px solid var(--lcars-gold);
    border-radius: 20px;
    width: 480px;
    max-width: 92vw;
    overflow: hidden;
    animation: info-scale-in 0.2s ease;
  }
  .target-popup-header {
    background: var(--lcars-gold);
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 10px 20px;
    border-radius: 18px 18px 0 0;
  }
  .target-popup-header span {
    font-family: 'Orbitron', sans-serif;
    font-size: 11px;
    font-weight: 700;
    color: #000;
    letter-spacing: 1.5px;
    text-transform: uppercase;
  }
  .target-popup-close {
    background: none;
    border: none;
    color: #000;
    font-size: 18px;
    cursor: pointer;
    font-weight: 700;
    line-height: 1;
    padding: 0 4px;
  }
  .target-popup-close:hover { opacity: 0.6; }
  .target-popup-body {
    padding: 20px 24px;
  }
  .target-popup-row {
    margin-bottom: 14px;
  }
  .target-popup-label {
    display: block;
    color: var(--lcars-gold);
    font-family: 'Orbitron', sans-serif;
    font-size: 9px;
    font-weight: 700;
    letter-spacing: 1.5px;
    text-transform: uppercase;
    margin-bottom: 4px;
  }
  .target-popup-input {
    width: 100%;
    box-sizing: border-box;
    background: rgba(255,204,153,0.06);
    border: 1px solid rgba(204,153,0,0.3);
    border-radius: 8px;
    color: var(--lcars-text-light);
    font-family: 'Antonio', sans-serif;
    font-size: 15px;
    letter-spacing: 0.5px;
    padding: 8px 12px;
    outline: none;
    transition: border-color 0.2s;
  }
  .target-popup-input:focus {
    border-color: var(--lcars-gold);
    box-shadow: 0 0 8px rgba(204,153,0,0.25);
  }
  .target-popup-input::placeholder {
    color: rgba(255,204,153,0.3);
  }
  .target-popup-hint {
    color: rgba(255,204,153,0.4);
    font-size: 10px;
    margin-top: 2px;
    letter-spacing: 0.5px;
  }
  .target-popup-actions {
    display: flex;
    gap: 10px;
    justify-content: flex-end;
    padding: 0 24px 20px;
  }
  .target-popup-btn {
    padding: 8px 24px;
    border: none;
    border-radius: 20px;
    font-family: 'Antonio', sans-serif;
    font-size: 13px;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 1px;
    cursor: pointer;
    transition: opacity 0.2s;
  }
  .target-popup-btn:hover { opacity: 0.8; }
  .target-popup-btn.confirm { background: var(--lcars-gold); color: #000; }
  .target-popup-btn.cancel { background: var(--lcars-critical); color: #000; }
  .target-popup-divider {
    height: 1px;
    background: linear-gradient(90deg, transparent, rgba(204,153,0,0.3), transparent);
    margin: 4px 0 14px;
  }

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

  /* ── Webhook Flow Line ── */
  .webhook-flow-line {
    grid-column: 1 / -1;
    position: relative;
    height: 3px;
    background: linear-gradient(90deg, transparent 0%, var(--lcars-blue-dark) 15%, var(--lcars-blue) 50%, var(--lcars-blue-dark) 85%, transparent 100%);
    opacity: 0.6;
    overflow: visible;
    z-index: 10;
  }

  @keyframes orb-traverse {
    0% { left: -20px; opacity: 0; }
    5% { opacity: 0.9; }
    90% { opacity: 0.9; }
    100% { left: calc(100% + 20px); opacity: 0; }
  }

  .webhook-orb {
    position: absolute;
    top: 50%;
    transform: translateY(-50%);
    width: 14px;
    height: 14px;
    border-radius: 50%;
    filter: blur(1.5px);
    animation: orb-traverse 4s linear forwards;
    pointer-events: none;
  }
  .webhook-orb.critical {
    background: radial-gradient(circle at 40% 40%, #ff9999, var(--lcars-critical) 50%, rgba(204,102,102,0.2));
    box-shadow: 0 0 8px 3px rgba(204,102,102,0.5), 0 0 16px 6px rgba(204,102,102,0.2);
  }
  .webhook-orb.warning {
    background: radial-gradient(circle at 40% 40%, #ffcc66, var(--lcars-warning) 50%, rgba(255,153,0,0.2));
    box-shadow: 0 0 8px 3px rgba(255,153,0,0.5), 0 0 16px 6px rgba(255,153,0,0.2);
  }
  .webhook-orb.ok {
    background: radial-gradient(circle at 40% 40%, #ccff99, var(--lcars-ok) 50%, rgba(153,204,102,0.2));
    box-shadow: 0 0 8px 3px rgba(153,204,102,0.5), 0 0 16px 6px rgba(153,204,102,0.2);
  }

  /* -- Settings Panel -- */
  .settings-section { margin-bottom: 20px; }
  .settings-section h3 {
    color: var(--lcars-gold);
    font-size: 14px;
    letter-spacing: 2px;
    text-transform: uppercase;
    margin-bottom: 12px;
    border-bottom: 2px solid rgba(255,204,153,0.2);
    padding-bottom: 6px;
  }
  .settings-grid {
    display: grid;
    grid-template-columns: 180px 1fr;
    gap: 8px 16px;
    align-items: center;
  }
  .settings-label {
    color: var(--lcars-tan);
    font-size: 12px;
    text-transform: uppercase;
    letter-spacing: 1px;
    text-align: right;
  }
  .settings-input {
    background: rgba(255,204,153,0.05);
    border: 1px solid rgba(255,204,153,0.2);
    border-radius: 6px;
    padding: 6px 12px;
    color: var(--lcars-text-light);
    font-family: 'Courier New', monospace;
    font-size: 13px;
    width: 100%;
    box-sizing: border-box;
  }
  .settings-input:focus {
    outline: none;
    border-color: var(--lcars-gold);
    background: rgba(255,204,153,0.1);
  }
  .settings-input[type="checkbox"] {
    width: auto;
    accent-color: var(--lcars-gold);
  }
  .settings-select {
    background: rgba(255,204,153,0.05);
    border: 1px solid rgba(255,204,153,0.2);
    border-radius: 6px;
    padding: 6px 12px;
    color: var(--lcars-text-light);
    font-size: 13px;
  }
  .settings-btn {
    background: var(--lcars-gold);
    color: #000;
    border: none;
    border-radius: 6px;
    padding: 8px 20px;
    font-weight: 700;
    font-size: 12px;
    cursor: pointer;
    text-transform: uppercase;
    letter-spacing: 1px;
  }
  .settings-btn:hover { filter: brightness(1.2); }
  .settings-btn.danger { background: var(--lcars-critical); }
  .settings-btn.blue { background: var(--lcars-blue); }
  .settings-btn.ok { background: var(--lcars-ok); }
  .settings-btn.purple { background: var(--lcars-lavender); }
  .settings-btn-sm {
    padding: 4px 12px;
    font-size: 11px;
  }
  .settings-target-card {
    background: rgba(153,153,255,0.05);
    border: 1px solid rgba(153,153,255,0.15);
    border-radius: 10px;
    padding: 16px;
    margin-bottom: 12px;
  }
  .settings-target-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 12px;
  }
  .settings-target-title {
    color: var(--lcars-blue);
    font-size: 16px;
    font-weight: 700;
    letter-spacing: 1px;
    text-transform: uppercase;
  }
  .settings-key-list {
    display: flex;
    flex-direction: column;
    gap: 4px;
  }
  .settings-key-item {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .settings-key-value {
    font-family: 'Courier New', monospace;
    font-size: 11px;
    color: var(--lcars-tan);
    background: rgba(0,0,0,0.3);
    padding: 3px 8px;
    border-radius: 4px;
    flex: 1;
  }
  .settings-status {
    padding: 8px 16px;
    border-radius: 6px;
    font-size: 12px;
    font-weight: 700;
    letter-spacing: 1px;
    text-transform: uppercase;
    margin-top: 12px;
    display: none;
  }
  .settings-status.ok { display: block; background: rgba(153,204,102,0.15); color: var(--lcars-ok); border: 1px solid var(--lcars-ok); }
  .settings-status.err { display: block; background: rgba(204,102,102,0.15); color: var(--lcars-critical); border: 1px solid var(--lcars-critical); }

  /* ── Dev Panel ── */
  .dev-entry {
    border-bottom: 1px solid rgba(204,153,204,0.15);
    padding: 8px 0;
  }
  .dev-entry:last-child { border-bottom: none; }
  .dev-entry-header {
    display: flex;
    align-items: center;
    gap: 10px;
    flex-wrap: wrap;
  }
  .dev-method {
    display: inline-block;
    padding: 1px 8px;
    border-radius: 4px;
    font-weight: 700;
    font-size: 11px;
    letter-spacing: 1px;
    color: #000;
    min-width: 55px;
    text-align: center;
  }
  .dev-method-GET { background: var(--lcars-blue); }
  .dev-method-POST { background: var(--lcars-ok); }
  .dev-method-PUT { background: var(--lcars-warning); }
  .dev-method-DELETE { background: var(--lcars-critical); }
  .dev-dir {
    display: inline-block;
    padding: 1px 6px;
    border-radius: 4px;
    font-size: 10px;
    font-weight: 700;
    letter-spacing: 1px;
    text-transform: uppercase;
  }
  .dev-dir-inbound { background: var(--lcars-purple); color: #000; }
  .dev-dir-outbound { background: var(--lcars-gold); color: #000; }
  .dev-source {
    font-size: 11px;
    color: var(--lcars-blue);
    font-weight: 700;
    letter-spacing: 0.5px;
  }
  .dev-remote {
    font-size: 11px;
    color: rgba(255,204,153,0.5);
  }
  .dev-url {
    color: var(--lcars-tan);
    flex: 1;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    min-width: 200px;
  }
  .dev-status {
    font-weight: 700;
    padding: 1px 8px;
    border-radius: 4px;
    font-size: 11px;
  }
  .dev-status-ok { color: var(--lcars-ok); border: 1px solid var(--lcars-ok); }
  .dev-status-err { color: var(--lcars-critical); border: 1px solid var(--lcars-critical); }
  .dev-duration { color: var(--lcars-lavender); font-size: 11px; }
  .dev-time { color: rgba(255,204,153,0.5); font-size: 11px; }
  .dev-error-tag {
    background: var(--lcars-critical);
    color: #000;
    padding: 1px 6px;
    border-radius: 4px;
    font-size: 10px;
    font-weight: 700;
  }
  .dev-details {
    margin: 4px 0 4px 65px;
  }
  .dev-details summary {
    cursor: pointer;
    color: var(--lcars-lavender);
    font-size: 11px;
    text-transform: uppercase;
    letter-spacing: 1px;
    padding: 2px 0;
  }
  .dev-details summary:hover { color: var(--lcars-purple); }
  .dev-pre-wrap {
    position: relative;
  }
  .dev-pre {
    background: rgba(153,153,255,0.05);
    border: 1px solid rgba(153,153,255,0.15);
    border-radius: 6px;
    padding: 8px 12px;
    overflow-x: auto;
    max-height: 400px;
    overflow-y: auto;
    white-space: pre-wrap;
    word-break: break-all;
    color: var(--lcars-text-light);
    font-size: 11px;
    line-height: 1.4;
  }
  .dev-copy-btn {
    position: absolute;
    top: 4px;
    right: 4px;
    background: var(--lcars-purple);
    color: #000;
    border: none;
    border-radius: 4px;
    padding: 2px 8px;
    font-size: 10px;
    font-weight: 700;
    cursor: pointer;
    letter-spacing: 0.5px;
    text-transform: uppercase;
    opacity: 0.7;
    z-index: 1;
  }
  .dev-copy-btn:hover { opacity: 1; }
  .j-key { color: var(--lcars-blue); }
  .j-str { color: var(--lcars-ok); }
  .j-num { color: var(--lcars-warning); }
  .j-bool { color: var(--lcars-lavender); }
  .j-null { color: var(--lcars-red); opacity: 0.7; }
  .j-brace { color: var(--lcars-tan); opacity: 0.6;
  }
  .dev-error-msg {
    margin: 4px 0 0 65px;
    color: var(--lcars-critical);
    font-size: 12px;
  }

  /* ── Nav sections ── */
  .nav-section { display: none; }
  .nav-section.active { display: block; }

  /* ── Info Trigger & Popup ── */
  .info-trigger {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 18px;
    height: 18px;
    border-radius: 50%;
    border: 1px solid var(--lcars-blue);
    background: transparent;
    color: var(--lcars-blue);
    font-family: 'Orbitron', sans-serif;
    font-size: 10px;
    font-weight: 700;
    cursor: pointer;
    transition: background 0.15s, color 0.15s;
    position: absolute;
    top: 6px;
    right: 6px;
    z-index: 5;
    line-height: 1;
  }
  .info-trigger:hover {
    background: var(--lcars-blue);
    color: #000;
  }
  .lcars-panel-title-bar .info-trigger {
    position: relative;
    top: auto;
    right: auto;
    margin-left: 10px;
    flex-shrink: 0;
  }
  .lcars-sidebar .sidebar-btn .info-trigger {
    position: relative;
    top: auto;
    right: auto;
    display: inline-flex;
    margin-left: 6px;
    width: 16px;
    height: 16px;
    font-size: 9px;
    border-color: rgba(0,0,0,0.4);
    color: rgba(0,0,0,0.5);
    vertical-align: middle;
  }
  .lcars-sidebar .sidebar-btn .info-trigger:hover {
    background: rgba(0,0,0,0.2);
    color: #000;
  }
  .info-overlay {
    position: fixed;
    top: 0; left: 0;
    width: 100%; height: 100%;
    background: #000000aa;
    z-index: 999;
    animation: info-fade-in 0.2s ease;
  }
  .info-popup {
    position: fixed;
    top: 50%; left: 50%;
    transform: translate(-50%, -50%);
    z-index: 1000;
    background: #000;
    border: 2px solid var(--lcars-blue);
    border-radius: 20px;
    max-width: 400px;
    width: 90%;
    overflow: hidden;
    animation: info-scale-in 0.2s ease;
  }
  .info-popup-header {
    background: var(--lcars-blue);
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 8px 16px;
  }
  .info-popup-header span {
    font-family: 'Orbitron', sans-serif;
    font-size: 11px;
    font-weight: 700;
    color: #000;
    letter-spacing: 1.5px;
    text-transform: uppercase;
  }
  .info-popup-close {
    background: none;
    border: none;
    color: #000;
    font-size: 18px;
    cursor: pointer;
    font-weight: 700;
    line-height: 1;
    padding: 0 4px;
  }
  .info-popup-close:hover { opacity: 0.6; }
  .info-popup-body {
    padding: 16px 20px;
    color: var(--lcars-tan);
    font-family: 'Antonio', sans-serif;
    font-size: 14px;
    letter-spacing: 0.5px;
    line-height: 1.5;
  }
  @keyframes info-fade-in {
    from { opacity: 0; }
    to { opacity: 1; }
  }
  @keyframes info-scale-in {
    from { opacity: 0; transform: translate(-50%, -50%) scale(0.9); }
    to { opacity: 1; transform: translate(-50%, -50%) scale(1); }
  }
</style>
</head>
<body>

<div class="lcars-frame">

  <!-- ══════ TOP HEADER BAR ══════ -->
  <div class="lcars-header">
    <div class="lcars-header-cap"><span>IAF</span></div>
    <div class="lcars-header-bar">
      <div class="bar-segment bar-seg-1">Stardate {{.GeneratedAt}}</div>
      <div class="bar-segment bar-seg-2">{{.Uptime}}</div>
      <div class="bar-segment bar-seg-3">IcingaAlertForge{{if .IsAdmin}} <span style="font-size:12px; letter-spacing:2px;">[COMMAND ACCESS]</span>{{end}}</div>
      <div class="bar-segment bar-seg-4">
        {{if not .IsAdmin}}<a href="/status/beauty?admin=1" style="color:#000;text-decoration:none;">AUTH</a>{{else}}<a href="#" onclick="doLogout();return false;" style="color:#000;text-decoration:none;">LOGOUT</a>{{end}}
      </div>
      <div class="bar-segment bar-seg-5">{{.Version}}</div>
    </div>
  </div>

  <!-- ══════ WEBHOOK FLOW LINE ══════ -->
  <div class="webhook-flow-line" id="webhookFlowLine"><span class="info-trigger" data-info="webhook_flow_line" style="position:absolute;top:-8px;right:12px;">?</span></div>

  <!-- ══════ LEFT SIDEBAR ══════ -->
  <div class="lcars-sidebar">
    <button class="sidebar-btn active" data-section="overview" onclick="showSection('overview', this, true)">Overview <span class="info-trigger" data-info="overview">?</span></button>
    <button class="sidebar-btn gold" data-section="system" onclick="showSection('system', this, true)">System <span class="info-trigger" data-info="diagnostics">?</span></button>
    {{if .IsAdmin}}
    <div class="sidebar-decoration"></div>
    <button class="sidebar-btn tan" data-section="alerts" onclick="showSection('alerts', this, true)">Alerts <span class="info-trigger" data-info="alerts">?</span></button>
    <button class="sidebar-btn purple" data-section="errors" onclick="showSection('errors', this, true)">Errors <span class="info-trigger" data-info="errors">?</span></button>
    <button class="sidebar-btn blue" data-section="services" onclick="showSection('services', this, true)">Services <span class="info-trigger" data-info="services">?</span></button>
    <button class="sidebar-btn" style="background:var(--lcars-blue);color:#000;" data-section="frozen" onclick="showSection('frozen', this, true)">Frozen <span id="frozen-count-badge" style="display:none;background:#000;color:var(--lcars-blue);border-radius:10px;padding:1px 6px;font-size:11px;margin-left:4px;"></span></button>
    <div class="sidebar-decoration"></div>
    <button class="sidebar-btn peach" data-section="security" onclick="showSection('security', this, true)">Security <span class="info-trigger" data-info="diagnostics">?</span></button>
    <button class="sidebar-btn" data-section="icinga" onclick="showSection('icinga', this, true)">Icinga Mgmt <span class="info-trigger" data-info="management">?</span></button>
    {{if and .CanManageConfig .ConfigInDashboard}}<button class="sidebar-btn gold" data-section="settings" onclick="showSection('settings', this, true)">Settings <span class="info-trigger" data-info="settings_panel">?</span></button>{{end}}
    <button class="sidebar-btn purple" data-section="devpanel" onclick="showSection('devpanel', this, true)">Dev Panel <span class="info-trigger" data-info="dev_panel">?</span></button>
    {{end}}
    <div class="sidebar-decoration purple"></div>
    <div class="sidebar-spacer"></div>
    <div class="sidebar-decoration blue"></div>
    <button class="sidebar-btn" data-section="about" onclick="showSection('about', this, true)">About</button>
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
            <span class="info-trigger" data-info="system_diagnostics">?</span>
          </div>
        </div>
        <div class="lcars-panel-body">
          <div class="stat-grid">
            <div class="stat-cell">
              <span class="info-trigger" data-info="total_since_start">?</span>
              <div class="stat-label">Total (since start)</div>
              <div class="stat-value" id="stat-total">{{.SysStats.TotalRequests}}</div>
            </div>
            <div class="stat-cell">
              <span class="info-trigger" data-info="history_entries">?</span>
              <div class="stat-label">History Entries</div>
              <div class="stat-value">{{.Stats.TotalEntries}}</div>
              {{if .IsOperator}}<button class="btn btn-danger btn-sm" style="margin-top:6px" onclick="clearHistory()">Clear</button> <span class="info-trigger" data-info="clear_history_button" style="position:relative;top:auto;right:auto;display:inline-flex;vertical-align:middle;">?</span>{{end}}
            </div>
            <div class="stat-cell {{if gt .Stats.ErrorCount 0}}critical{{end}}">
              <span class="info-trigger" data-info="errors">?</span>
              <div class="stat-label">Errors</div>
              <div class="stat-value {{if gt .Stats.ErrorCount 0}}error{{else}}ok{{end}}" id="stat-errors">{{.Stats.ErrorCount}}</div>
            </div>
            <div class="stat-cell blue">
              <span class="info-trigger" data-info="avg_duration">?</span>
              <div class="stat-label">Avg Duration</div>
              <div class="stat-value blue">{{.Stats.AvgDurationMs}}ms</div>
            </div>
            <div class="stat-cell purple">
              <span class="info-trigger" data-info="cached_services">?</span>
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
            <span class="info-trigger" data-info="system_diagnostics">?</span>
          </div>
        </div>
        <div class="lcars-panel-body">
          <div class="stat-grid">
            <div class="stat-cell ok">
              <span class="info-trigger" data-info="firing">?</span>
              <div class="stat-label">Firing</div>
              <div class="stat-value ok" id="stat-work">{{index .Stats.ByAction "firing"}}</div>
            </div>
            <div class="stat-cell blue">
              <span class="info-trigger" data-info="resolved">?</span>
              <div class="stat-label">Resolved</div>
              <div class="stat-value blue">{{index .Stats.ByAction "resolved"}}</div>
            </div>
            <div class="stat-cell purple">
              <span class="info-trigger" data-info="test_mode">?</span>
              <div class="stat-label">Test Mode</div>
              <div class="stat-value purple" id="stat-test">{{index .Stats.ByMode "test"}}</div>
            </div>
            <div class="stat-cell critical">
              <span class="info-trigger" data-info="critical_firing">?</span>
              <div class="stat-label">Critical (firing)</div>
              <div class="stat-value critical" id="stat-critical">{{index .Stats.BySeverityFiring "critical"}}</div>
            </div>
            <div class="stat-cell warning">
              <span class="info-trigger" data-info="warning_firing">?</span>
              <div class="stat-label">Warning (firing)</div>
              <div class="stat-value warning" id="stat-warning">{{index .Stats.BySeverityFiring "warning"}}</div>
            </div>
          </div>
          <div class="scanner-line"></div>
          <div style="display:flex;gap:20px;font-size:13px;color:var(--lcars-tan);letter-spacing:1px;text-transform:uppercase;flex-wrap:wrap;">
            <span><span class="status-dot green"></span> Systems Nominal</span>
            <span><span class="status-dot {{if gt .Stats.ErrorCount 0}}red{{else}}green{{end}}"></span> Error Detection</span>
            <span><span class="status-dot orange blink"></span> Monitoring Active</span>
          </div>
        </div>
      </div>

      <!-- Enterprise Features Status -->
      <div class="lcars-panel gold">
        <div class="lcars-panel-header">
          <div class="lcars-panel-elbow gold"></div>
          <div class="lcars-panel-title-bar">
            <div class="bar-fill"></div>
            <span class="title-text" style="color:var(--lcars-gold);">Enterprise Subsystems</span>
          </div>
        </div>
        <div class="lcars-panel-body">
          <div class="stat-grid">
            {{if .HealthStatus}}
            <div class="stat-cell {{if not .HealthStatus.Healthy}}critical{{end}}">
              <div class="stat-label">Icinga2 Link</div>
              <div class="stat-value {{if .HealthStatus.Healthy}}ok{{else}}critical{{end}}">{{if .HealthStatus.Healthy}}ONLINE{{else}}OFFLINE ({{.HealthStatus.ConsecutiveFails}} fails){{end}}</div>
            </div>
            {{end}}
            {{if .QueueStats}}
            <div class="stat-cell {{if gt .QueueStats.Depth 0}}warning{{end}}">
              <div class="stat-label">Retry Queue</div>
              <div class="stat-value {{if gt .QueueStats.Depth 0}}warning{{else}}ok{{end}}">{{.QueueStats.Depth}} / {{.QueueStats.MaxSize}}</div>
            </div>
            <div class="stat-cell">
              <div class="stat-label">Retried / Dropped</div>
              <div class="stat-value">{{.QueueStats.TotalRetried}} / {{.QueueStats.TotalDropped}}</div>
            </div>
            {{end}}
            <div class="stat-cell">
              <div class="stat-label">Audit Log</div>
              <div class="stat-value {{if .AuditEnabled}}ok{{else}}warning{{end}}">{{if .AuditEnabled}}ACTIVE{{else}}DISABLED{{end}}</div>
            </div>
            {{if .UserRole}}
            <div class="stat-cell">
              <div class="stat-label">RBAC Role</div>
              <div class="stat-value purple">{{.UserRole}}</div>
            </div>
            {{end}}
            <div class="stat-cell">
              <div class="stat-label">Multi-Source</div>
              <div class="stat-value ok">ACTIVE</div>
            </div>
          </div>
        </div>
      </div>

      {{if .IsAdmin}}
      <!-- Sources (admin only — exposes IPs) -->
      <div class="lcars-panel blue">
        <div class="lcars-panel-header">
          <div class="lcars-panel-elbow blue"></div>
          <div class="lcars-panel-title-bar">
            <div class="bar-fill"></div>
            <span class="title-text blue">Signal Sources</span>
            <span class="info-trigger" data-info="signal_sources">?</span>
          </div>
        </div>
        <div class="lcars-panel-body">
          {{if .Stats.BySource}}
          <div class="tag-list">
            {{range $source, $count := .Stats.BySource}}
            <span class="source-tag clickable js-source-toggle" data-source="{{$source}}">{{$source}} <span class="count">({{$count}})</span></span>
            {{end}}
          </div>
          {{range $source, $count := .Stats.BySource}}
          <div class="source-detail" id="source-detail-{{$source}}" style="display:none">
            <div class="ip-tabs">
              <span class="ip-tab active js-ip-tab" data-source="{{$source}}" data-tab="last">Last 10</span>
              <span class="ip-tab js-ip-tab" data-source="{{$source}}" data-tab="top">Top 10</span>
            </div>
            <div class="ip-tab-content" id="ip-last-{{$source}}">
              <div class="source-detail-title">Recent connections</div>
              {{with index $.SourceLastIPs $source}}{{range .}}
              <div class="ip-entry"><span class="ip-addr">{{.IP}}</span><span class="ip-meta">{{.LastSeen}}</span><span class="ip-count">{{.Count}}x</span></div>
              {{end}}{{else}}<div class="ip-entry">No data</div>{{end}}
            </div>
            <div class="ip-tab-content" id="ip-top-{{$source}}" style="display:none">
              <div class="source-detail-title">Most active</div>
              {{with index $.SourceTopIPs $source}}{{range .}}
              <div class="ip-entry"><span class="ip-addr">{{.IP}}</span><span class="ip-count">{{.Count}} webhooks</span></div>
              {{end}}{{else}}<div class="ip-entry">No data</div>{{end}}
            </div>
          </div>
          {{end}}
          {{else}}
          <div class="empty-state">No signal sources detected</div>
          {{end}}
        </div>
      </div>
      {{end}}

    </div><!-- /overview -->

    {{if .IsAdmin}}
    <!-- ── ALERTS SECTION (admin only) ── -->
    <div class="nav-section" id="sec-alerts">
      <div class="lcars-panel tan">
        <div class="lcars-panel-header">
          <div class="lcars-panel-elbow tan"></div>
          <div class="lcars-panel-title-bar">
            <div class="bar-fill"></div>
            <span class="title-text tan">Recent Transmissions (Last 20)</span>
            <span class="info-trigger" data-info="recent_alerts">?</span>
          </div>
        </div>
        <div class="lcars-panel-body">
          {{if .RecentAlerts}}
          <div class="table-filter">
            <input type="text" id="filterAlerts" placeholder="Filter alerts..." oninput="filterTable('alertsTable', this.value, 'filterAlertsCount')">
            <span class="table-filter-count" id="filterAlertsCount"></span>
          </div>
          <table id="alertsTable">
            <thead>
              <tr>
                <th onclick="sortTable(0,'date',this.closest('table'))">Stardate <span class="sort-arrow"></span></th>
                <th onclick="sortTable(1,'string',this.closest('table'))">Status <span class="sort-arrow"></span></th>
                <th onclick="sortTable(2,'string',this.closest('table'))">Mode <span class="sort-arrow"></span></th>
                <th onclick="sortTable(3,'string',this.closest('table'))">Action <span class="sort-arrow"></span></th>
                <th onclick="sortTable(4,'string',this.closest('table'))">Host <span class="sort-arrow"></span></th>
                <th onclick="sortTable(5,'string',this.closest('table'))">Service <span class="sort-arrow"></span></th>
                <th onclick="sortTable(6,'string',this.closest('table'))">Source <span class="sort-arrow"></span></th>
                <th>Icinga</th>
                <th onclick="sortTable(8,'number',this.closest('table'))">Duration <span class="sort-arrow"></span></th>
              </tr>
            </thead>
            <tbody>
              {{range .RecentAlerts}}
              <tr data-service="{{.ServiceName}}" data-host="{{.HostName}}">
                <td class="mono">{{.Timestamp}}</td>
                <td><span class="badge {{.StatusClass}}">{{.StatusLabel}}</span></td>
                <td>{{.Mode}}{{if eq .Mode "manual"}} <span class="alerts-manual-tag">ADMIN</span>{{end}}</td>
                <td>{{.Action}}</td>
                <td class="mono">{{if .HostName}}{{.HostName}}{{else}}-{{end}}</td>
                <td><strong class="svc-link js-service-history-trigger">{{.ServiceName}}</strong></td>
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
            <span class="info-trigger" data-info="recent_errors">?</span>
          </div>
        </div>
        <div class="lcars-panel-body">
          {{if .RecentErrors}}
          <table>
            <thead>
              <tr>
                <th>Stardate</th>
                <th>Host</th>
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
                <td class="mono">{{if .HostName}}{{.HostName}}{{else}}-{{end}}</td>
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
            <span class="info-trigger" data-info="cached_services_section">?</span>
          </div>
        </div>
        <div class="lcars-panel-body">
          {{if .CachedServices}}
          <div class="table-filter">
            <input type="text" id="filterServices" placeholder="Filter services..." oninput="filterTable('svcRegistryTable', this.value, 'filterServicesCount')">
            <span class="table-filter-count" id="filterServicesCount">{{len .CachedServices}} registered</span>
          </div>
          <table class="svc-registry-table" id="svcRegistryTable">
            <thead>
              <tr>
                <th onclick="sortTable(0,'string',this.closest('table'))">Host <span class="sort-arrow"></span></th>
                <th onclick="sortTable(1,'string',this.closest('table'))">Service Designation <span class="sort-arrow"></span></th>
                <th onclick="sortTable(2,'string',this.closest('table'))">State <span class="sort-arrow"></span></th>
              </tr>
            </thead>
            <tbody>
              {{$prevHost := ""}}
              {{range .CachedServices}}
              {{if and .Host (ne .Host $prevHost)}}
              <tr class="svc-host-divider"><td colspan="3">{{.Host}}</td></tr>
              {{end}}
              <tr class="js-service-history" data-service="{{.Service}}" data-host="{{.Host}}">
                <td class="svc-host-cell">{{if .Host}}{{.Host}}{{else}}-{{end}}</td>
                <td class="svc-name-cell">{{.Service}}</td>
                <td><span class="svc-state-badge {{.State}}">{{.State}}</span></td>
              </tr>
              {{$prevHost = .Host}}
              {{end}}
            </tbody>
          </table>
          {{else}}
          <div class="empty-state">No services in cache memory</div>
          {{end}}
        </div>
      </div>
    </div><!-- /services -->
    {{end}}

    <!-- ── SYSTEM SECTION (public) ── -->
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
            <div class="stat-cell">
              <div class="stat-label">Uptime</div>
              <div class="stat-value" style="font-size:16px;">{{.SysStats.Uptime}}</div>
            </div>
            {{if .QueueStats}}
            <div class="stat-cell {{if gt .QueueStats.Depth 0}}warning{{end}}">
              <div class="stat-label">Retry Queue</div>
              <div class="stat-value {{if gt .QueueStats.Depth 0}}warning{{else}}ok{{end}}">{{.QueueStats.Depth}} / {{.QueueStats.MaxSize}}</div>
            </div>
            <div class="stat-cell">
              <div class="stat-label">Retried / Dropped</div>
              <div class="stat-value">{{.QueueStats.TotalRetried}} / {{.QueueStats.TotalDropped}}</div>
            </div>
            {{end}}
            {{if .HealthStatus}}
            <div class="stat-cell {{if not .HealthStatus.Healthy}}critical{{end}}">
              <div class="stat-label">Icinga2 Link</div>
              <div class="stat-value {{if .HealthStatus.Healthy}}ok{{else}}critical{{end}}">{{if .HealthStatus.Healthy}}UP{{else}}DOWN ({{.HealthStatus.ConsecutiveFails}}){{end}}</div>
            </div>
            {{end}}
            <div class="stat-cell">
              <div class="stat-label">Audit Log</div>
              <div class="stat-value {{if .AuditEnabled}}ok{{else}}warning{{end}}">{{if .AuditEnabled}}ACTIVE{{else}}OFF{{end}}</div>
            </div>
            {{if .UserRole}}
            <div class="stat-cell">
              <div class="stat-label">RBAC Role</div>
              <div class="stat-value">{{.UserRole}}</div>
            </div>
            {{end}}
          </div>
          <div class="scanner-line"></div>
        </div>
      </div>

      {{if .IsAdmin}}
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
          </div>
        </div>
      </div>
      {{end}}
    </div><!-- /system -->

    <!-- -- SETTINGS SECTION -- -->
    {{if and .CanManageConfig .ConfigInDashboard}}
    <div class="nav-section" id="sec-settings">
      <div class="lcars-panel gold">
        <div class="lcars-panel-header">
          <div class="lcars-panel-elbow" style="background:var(--lcars-gold);"></div>
          <div class="lcars-panel-title-bar">
            <div class="bar-fill" style="background:var(--lcars-gold);"></div>
            <span class="title-text" style="color:var(--lcars-gold);">Configuration Management</span>
          </div>
        </div>
        <div class="lcars-panel-body">
          <div id="settingsStatus" class="settings-status"></div>

          <!-- Icinga2 Connection -->
          <div class="settings-section">
            <h3>Icinga2 Connection</h3>
            <div class="settings-grid">
              <span class="settings-label">API Host</span>
              <input type="text" class="settings-input" id="cfg-icinga2-host" placeholder="https://icinga2:5665">
              <span class="settings-label">API User</span>
              <input type="text" class="settings-input" id="cfg-icinga2-user" placeholder="apiuser">
              <span class="settings-label">API Password</span>
              <input type="password" class="settings-input" id="cfg-icinga2-pass" placeholder="***">
              <span class="settings-label">TLS Skip Verify</span>
              <input type="checkbox" class="settings-input" id="cfg-icinga2-tls-skip">
              <span class="settings-label">Auto-Create Hosts</span>
              <input type="checkbox" class="settings-input" id="cfg-icinga2-auto-create">
              <span class="settings-label">Conflict Policy</span>
              <select class="settings-select settings-input" id="cfg-icinga2-conflict-policy">
                <option value="skip">skip</option>
                <option value="warn">warn</option>
                <option value="fail">fail</option>
              </select>
              <span class="settings-label">Force Mode</span>
              <input type="checkbox" class="settings-input" id="cfg-icinga2-force">
            </div>
            <div style="margin-top:10px;">
              <button class="settings-btn blue settings-btn-sm" onclick="testIcingaConnection()">Test Connection</button>
              <span id="icingaTestResult" style="margin-left:10px;font-size:12px;"></span>
            </div>
          </div>

          <!-- Targets / Webhooks -->
          <div class="settings-section">
            <h3>Targets &amp; Webhooks</h3>
            <div id="settingsTargets"></div>
            <button class="settings-btn purple settings-btn-sm" onclick="addNewTarget()" style="margin-top:10px;">+ Add Target</button>
          </div>

          <!-- User Management (RBAC) -->
          <div class="settings-section">
            <h3>User Management (RBAC)</h3>
            <p style="font-size:12px;color:var(--lcars-tan);margin-bottom:10px;">Manage dashboard users with role-based access: <strong>viewer</strong> (read-only), <strong>operator</strong> (status changes, queue flush), <strong>admin</strong> (full access).</p>
            <table style="width:100%;border-collapse:collapse;margin-bottom:10px;" id="rbacUsersTable">
              <thead>
                <tr style="border-bottom:1px solid var(--lcars-purple);text-align:left;">
                  <th style="padding:6px;color:var(--lcars-purple);font-size:12px;letter-spacing:1px;">USERNAME</th>
                  <th style="padding:6px;color:var(--lcars-purple);font-size:12px;letter-spacing:1px;">ROLE</th>
                  <th style="padding:6px;color:var(--lcars-purple);font-size:12px;letter-spacing:1px;">ACTIONS</th>
                </tr>
              </thead>
              <tbody id="rbacUsersBody">
                <tr><td colspan="3" style="padding:10px;color:var(--lcars-tan);font-size:12px;">Loading...</td></tr>
              </tbody>
            </table>
            <div style="display:flex;gap:8px;flex-wrap:wrap;align-items:center;margin-top:8px;">
              <input type="text" class="settings-input" id="rbac-new-user" placeholder="Username" style="width:140px;">
              <input type="password" class="settings-input" id="rbac-new-pass" placeholder="Password" style="width:140px;">
              <select class="settings-select settings-input" id="rbac-new-role" style="width:120px;">
                <option value="viewer">viewer</option>
                <option value="operator">operator</option>
                <option value="admin">admin</option>
              </select>
              <button class="settings-btn purple settings-btn-sm" onclick="rbacAddUser()">+ Add User</button>
            </div>
          </div>

          <!-- History & Cache -->
          <div class="settings-section">
            <h3>History &amp; Cache</h3>
            <div class="settings-grid">
              <span class="settings-label">History File</span>
              <input type="text" class="settings-input" id="cfg-history-file" placeholder="/var/log/webhook-bridge/history.jsonl">
              <span class="settings-label">Max Entries</span>
              <input type="number" class="settings-input" id="cfg-history-max" placeholder="10000">
              <span class="settings-label">Cache TTL (min)</span>
              <input type="number" class="settings-input" id="cfg-cache-ttl" placeholder="60">
            </div>
          </div>

          <!-- Logging -->
          <div class="settings-section">
            <h3>Logging</h3>
            <div class="settings-grid">
              <span class="settings-label">Log Level</span>
              <select class="settings-select settings-input" id="cfg-log-level">
                <option value="debug">debug</option>
                <option value="info">info</option>
                <option value="warn">warn</option>
                <option value="error">error</option>
              </select>
              <span class="settings-label">Log Format</span>
              <select class="settings-select settings-input" id="cfg-log-format">
                <option value="json">json</option>
                <option value="text">text</option>
              </select>
            </div>
          </div>

          <!-- Rate Limiting -->
          <div class="settings-section">
            <h3>Rate Limiting</h3>
            <div class="settings-grid">
              <span class="settings-label">Mutate Max</span>
              <input type="number" class="settings-input" id="cfg-rl-mutate" placeholder="5">
              <span class="settings-label">Status Max</span>
              <input type="number" class="settings-input" id="cfg-rl-status" placeholder="20">
              <span class="settings-label">Queue Max</span>
              <input type="number" class="settings-input" id="cfg-rl-queue" placeholder="100">
            </div>
          </div>

          <!-- Action buttons -->
          <div style="margin-top:20px;display:flex;gap:10px;flex-wrap:wrap;align-items:center;">
            <button class="settings-btn ok" onclick="saveSettings()">Save Configuration</button>
            <button class="settings-btn blue" onclick="exportSettings()">Export Backup</button>
            <button class="settings-btn purple" onclick="document.getElementById('importFileInput').click()">Import Backup</button>
            <input type="file" id="importFileInput" accept=".json" style="display:none;" onchange="importSettings(this)">
            <button class="settings-btn" onclick="loadSettings()">Reload</button>
          </div>
        </div>
      </div>
    </div>
    {{end}}

    {{if .IsAdmin}}
    <!-- ── SECURITY SECTION (admin only) ── -->
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
            <span class="title-text">Icinga2 Services - "{{.HostLabel}}" [Command Level]</span>
            <span class="info-trigger" data-info="service_management">?</span>
          </div>
        </div>
        <div class="lcars-panel-body">
          {{if .IcingaServices}}
          <div class="table-filter">
            <input type="text" id="filterIcinga" placeholder="Filter Icinga services..." oninput="filterTable('servicesTable', this.value, 'filterIcingaCount')">
            <span class="table-filter-count" id="filterIcingaCount">{{len .IcingaServices}} registered</span>
          </div>
          {{if .CanDeleteService}}<div class="toolbar">
            <input type="checkbox" id="selectAll" onclick="toggleAll(this)">
            <label for="selectAll">Select All</label>
            <button class="btn btn-danger btn-sm" onclick="deleteSelected()" id="btnDeleteSelected" disabled>Delete Selected</button>
          </div>{{end}}
          <table id="servicesTable">
            <thead>
              <tr>
                <th class="checkbox-cell"></th>
                <th onclick="sortTable(1,'string')">Host <span class="sort-arrow"></span></th>
                <th onclick="sortTable(2,'string')">Designation <span class="sort-arrow"></span></th>
                <th onclick="sortTable(3,'string')">Display <span class="sort-arrow"></span></th>
                <th onclick="sortTable(4,'string')">Status <span class="sort-arrow"></span></th>
                <th onclick="sortTable(5,'string')">Output <span class="sort-arrow"></span></th>
                <th onclick="sortTable(6,'date')">Last Scan <span class="sort-arrow"></span></th>
                <th>Actions</th>
              </tr>
            </thead>
            <tbody>
              {{range .IcingaServices}}
              <tr data-service="{{.Name}}" data-host="{{.HostName}}">
                <td class="checkbox-cell">{{if $.CanDeleteService}}<input type="checkbox" class="svc-check" value="{{.Name}}" data-host="{{.HostName}}">{{end}}</td>
                <td class="mono">{{.HostName}}</td>
                <td><strong style="cursor:pointer;color:var(--lcars-blue);" class="js-service-history-trigger">{{.Name}}</strong></td>
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
                <td>{{if $.CanDeleteService}}<button class="btn btn-danger btn-sm" onclick="deleteService(this)">Purge</button>{{end}}</td>
              </tr>
              {{end}}
            </tbody>
          </table>
          {{else}}
          <div class="empty-state">No services registered for "{{.HostLabel}}"</div>
          {{end}}
        </div>
      </div>
    </div><!-- /icinga -->

    <!-- ── FROZEN SECTION ── -->
    <div class="nav-section" id="sec-frozen">
      <div class="lcars-panel blue">
        <div class="lcars-panel-header">
          <div class="lcars-panel-elbow blue"></div>
          <div class="lcars-panel-title-bar">
            <div class="bar-fill"></div>
            <span class="title-text blue">Frozen Services</span>
            <button class="btn btn-sm" onclick="loadFrozenList()" style="margin-left:12px;">Refresh</button>
          </div>
        </div>
        <div class="lcars-panel-body">
          <div id="frozen-list-container">
            <div class="empty-state">Loading...</div>
          </div>
        </div>
      </div>
    </div><!-- /frozen -->

    <!-- ── DEV PANEL SECTION ── -->
    <div class="nav-section" id="sec-devpanel">
      <div class="lcars-panel purple">
        <div class="lcars-panel-header">
          <div class="lcars-panel-elbow purple"></div>
          <div class="lcars-panel-title-bar">
            <div class="bar-fill"></div>
            <span class="title-text purple">Icinga2 API Traffic Inspector</span>
            <span class="info-trigger" data-info="dev_panel">?</span>
          </div>
        </div>
        <div class="lcars-panel-body">
          <div class="toolbar">
            <label style="flex:1;">Real-time API traffic inspector</label>
            <button class="btn btn-sm" id="devToggleBtn" onclick="devToggle()" style="background:var(--lcars-ok);min-width:70px;">OFF</button>
            <button class="btn btn-sm" id="devPauseBtn" onclick="devPause()" style="background:var(--lcars-lavender);min-width:70px;display:none;">PAUSE</button>
            <button class="btn btn-primary btn-sm" onclick="clearDevLog()">Clear</button>
          </div>
          <div id="devLogContainer" style="max-height:600px;overflow-y:auto;font-family:'SF Mono',SFMono-Regular,Menlo,monospace;font-size:12px;">
            <div class="empty-state" id="devEmptyState">Debug collection is OFF. Click the toggle to start capturing API traffic.</div>
          </div>
        </div>
      </div>
    </div><!-- /devpanel -->
    {{end}}

    <!-- ── ABOUT SECTION ── -->
    <div class="nav-section" id="sec-about">
      <div class="lcars-panel gold">
        <div class="lcars-panel-header">
          <div class="lcars-panel-elbow gold"></div>
          <div class="lcars-panel-title-bar">
            <div class="bar-fill" style="background:var(--lcars-gold);"></div>
            <span class="title-text" style="color:var(--lcars-gold);">About IcingaAlertForge</span>
          </div>
        </div>
        <div class="lcars-panel-body">
          <div class="about-section">
            <div class="about-logo">IcingaAlertForge</div>
            <div class="about-version">Version {{.Version}} // Grafana &gt; Webhook Bridge &gt; Icinga2</div>

            <p>IcingaAlertForge is a lightweight webhook bridge that receives alert notifications from <strong>Grafana</strong> (or any compatible source) and translates them into passive check results for <strong>Icinga2</strong>. It enables seamless integration between modern alerting pipelines and enterprise monitoring infrastructure.</p>

            <h3>Key Features</h3>
            <ul>
              <li>Multi-target webhook routing with API key authentication</li>
              <li>Automatic Icinga2 host and service creation</li>
              <li>Real-time LCARS dashboard with SSE streaming</li>
              <li>Manual service status control (OK / WARNING / CRITICAL)</li>
              <li>Encrypted configuration store with dashboard management</li>
              <li>Rate limiting, brute-force detection, security headers</li>
              <li>Full audit history with JSONL logging and export</li>
            </ul>

            <h3>Grafana Setup - Step by Step</h3>
            <p><span class="about-step-num">1</span> In Grafana, go to <strong>Alerting &gt; Contact Points</strong></p>
            <p><span class="about-step-num">2</span> Click <strong>New Contact Point</strong>, select type <strong>Webhook</strong></p>
            <p><span class="about-step-num">3</span> Set the URL to your IcingaAlertForge instance:</p>
            <pre>http://your-host:8080/webhook</pre>
            <p><span class="about-step-num">4</span> In <strong>Optional Webhook Settings</strong>, add HTTP header:</p>
            <pre>X-API-Key: your-api-key-here</pre>
            <p><span class="about-step-num">5</span> Click <strong>Test</strong> to send a test notification, then <strong>Save</strong></p>
            <p><span class="about-step-num">6</span> Go to <strong>Alerting &gt; Notification Policies</strong> and route desired alerts to your new contact point</p>

            <h3>Icinga2 Setup - API User</h3>
            <p><span class="about-step-num">1</span> Create an API user in <code>/etc/icinga2/conf.d/api-users.conf</code>:</p>
            <pre>object ApiUser "icinga-alertforge" {
  password = "your-secure-password"
  permissions = [
    "actions/process-check-result",
    "objects/query/Host",
    "objects/query/Service",
    "objects/create/Host",
    "objects/create/Service",
    "objects/delete/Service",
    "status/query"
  ]
}</pre>
            <p><span class="about-step-num">2</span> Restart Icinga2: <code>systemctl restart icinga2</code></p>
            <p><span class="about-step-num">3</span> Configure the connection in IcingaAlertForge Settings panel or via environment variables:</p>
            <pre>ICINGA2_HOST=https://your-icinga:5665
ICINGA2_USER=icinga-alertforge
ICINGA2_PASS=your-secure-password</pre>

            <h3>Adding an API Key</h3>
            <p><span class="about-step-num">1</span> Open the <strong>Settings</strong> panel in the dashboard (requires admin access)</p>
            <p><span class="about-step-num">2</span> In the <strong>Targets</strong> section, add a new target or select an existing one</p>
            <p><span class="about-step-num">3</span> Click <strong>Generate Key</strong> — copy the key immediately (it will be masked after)</p>
            <p><span class="about-step-num">4</span> Use this key as the <code>X-API-Key</code> header in your Grafana webhook contact point</p>
            <p>Alternatively, set keys via environment: <code>IAF_TARGET_myid_API_KEYS=key1,key2</code></p>

            <h3>Dashboard Configuration Mode</h3>
            <p>IcingaAlertForge supports full configuration through the Beauty Panel — no need to edit config files or restart the service.</p>
            <p>Set the environment variable <code>CONFIG_IN_DASHBOARD=true</code> to enable it. Once active, the <strong>Settings</strong> panel appears in the admin sidebar where you can:</p>
            <ul>
              <li>Configure <strong>Icinga2 connection</strong> (host, user, password, TLS settings) — with a <strong>Test Connection</strong> button</li>
              <li>Manage <strong>webhook targets</strong> — add, remove, generate API keys</li>
              <li>Change <strong>admin credentials</strong> on the fly</li>
              <li>Set <strong>logging, history, and cache</strong> parameters</li>
              <li><strong>Export / Import</strong> full encrypted configuration backup</li>
            </ul>
            <p>All changes are applied immediately via hot-reload — no service restart required. Configuration is stored encrypted (AES-256-GCM) on disk.</p>

            <h3>Author &amp; Source</h3>
            <p>Created by <span class="about-author">dzaczek</span> — <a class="about-link" href="https://github.com/dzaczek/IcingaAlertingForge" target="_blank" rel="noopener">github.com/dzaczek/IcingaAlertingForge</a></p>
            <p>Licensed under the MIT License.</p>
          </div>
        </div>
      </div>
    </div><!-- /about -->

  </div><!-- /lcars-content -->

  <!-- ══════ FOOTER BAR ══════ -->
  <div class="lcars-footer">
    <div class="lcars-footer-cap"></div>
    <div class="lcars-footer-bar">
      <div class="foot-1">IcingaAlertForge // Grafana > Webhook Bridge > Icinga2</div>
      <div class="foot-2">dzaczek &copy; 2026 // <a href="https://github.com/dzaczek/IcingaAlertingForge" target="_blank" rel="noopener">GitHub</a></div>
      <div class="foot-3"></div>
      <div class="foot-4"></div>
    </div>
  </div>

</div><!-- /lcars-frame -->

<div class="toast" id="toast"></div>

<script>
// ── Navigation ──
function setActiveSidebar(name) {
  document.querySelectorAll('.lcars-sidebar .sidebar-btn[data-section]').forEach(b => b.classList.remove('active'));
  const btn = document.querySelector('.lcars-sidebar .sidebar-btn[data-section="' + name + '"]');
  if (btn) btn.classList.add('active');
}

function showSection(name, _btn, updateHash) {
  document.querySelectorAll('.nav-section').forEach(s => s.classList.remove('active'));
  let activeName = name;
  let sec = document.getElementById('sec-' + activeName);
  if (!sec) {
    activeName = 'overview';
    sec = document.getElementById('sec-overview');
  }
  if (sec) sec.classList.add('active');
  setActiveSidebar(activeName);
  if (updateHash && window.location.hash !== '#' + activeName) {
    window.location.hash = activeName;
  }
  if (activeName === 'settings' && !window._settingsLoaded) {
    window._settingsLoaded = true;
    loadSettings();
    rbacLoadUsers();
  }
  if (activeName === 'frozen') {
    loadFrozenList();
  }
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
var _canChangeStatus = {{.CanChangeStatus}};
var _canDeleteService = {{.CanDeleteService}};
var _canManageConfig = {{.CanManageConfig}};
var _canManageUsers = {{.CanManageUsers}};
var _primaryAdmin = '{{.PrimaryAdmin}}';

function showToast(msg, type) {
  const t = document.getElementById('toast');
  t.textContent = msg;
  t.className = 'toast show ' + type;
  setTimeout(() => t.className = 'toast', 3000);
}

function findServiceRow(host, name) {
  return Array.from(document.querySelectorAll('#servicesTable tbody tr')).find(row =>
    row.dataset.host === host && row.dataset.service === name
  );
}

function deleteService(btn) {
  const row = btn.closest('tr');
  const name = row?.dataset.service || '';
  const host = row?.dataset.host || '';
  if (!name || !host) return;
  if (!confirm('Confirm purge of service "' + name + '" on host "' + host + '" from Icinga2?')) return;
  btn.disabled = true;
  btn.textContent = '...';

  fetch('/admin/services/' + encodeURIComponent(name) + '?host=' + encodeURIComponent(host), {
    method: 'DELETE',
    credentials: 'include',
  }).then(r => r.json()).then(data => {
    if (data.status === 'deleted') {
      showToast('Purged: ' + host + ' / ' + name, 'success');
      const targetRow = findServiceRow(host, name);
      if (targetRow) targetRow.remove();
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
  const services = checked.map(cb => ({host: cb.dataset.host, service: cb.value}));
  if (services.length === 0) return;
  if (!confirm('Confirm purge of ' + services.length + ' service(s) from Icinga2?')) return;

  const btn = document.getElementById('btnDeleteSelected');
  btn.disabled = true;
  btn.textContent = 'Purging...';

  fetch('/admin/services/bulk-delete', {
    method: 'POST',
    credentials: 'include',
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify({services}),
  }).then(r => r.json()).then(data => {
    let deleted = 0, errors = 0;
    (data.results || []).forEach(r => {
      if (r.status === 'deleted') {
        deleted++;
        const row = findServiceRow(r.host, r.service);
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

// ── Dev Panel: SSE debug stream with ON/OFF + PAUSE ──
var devEnabled = false;
var devPaused = false;

function clearDevLog() {
  var container = document.getElementById('devLogContainer');
  if (container) container.innerHTML = '<div class="empty-state" id="devEmptyState">Debug collection is OFF. Click the toggle to start capturing API traffic.</div>';
}

function devToggle() {
  var newState = !devEnabled;
  fetch('/admin/debug/toggle', {
    method: 'POST',
    credentials: 'include',
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify({enabled: newState})
  }).then(function(r) { return r.json(); }).then(function(data) {
    devEnabled = data.enabled;
    devPaused = false;
    devUpdateButtons();
    if (!devEnabled) {
      var container = document.getElementById('devLogContainer');
      if (container) {
        var info = document.createElement('div');
        info.className = 'empty-state';
        info.textContent = 'Debug collection stopped.';
        container.insertBefore(info, container.firstChild);
      }
    } else {
      var empty = document.getElementById('devEmptyState');
      if (empty) empty.textContent = 'Collecting... waiting for API traffic.';
    }
  }).catch(function() {});
}

function devPause() {
  devPaused = !devPaused;
  devUpdateButtons();
}

function devUpdateButtons() {
  var toggleBtn = document.getElementById('devToggleBtn');
  var pauseBtn = document.getElementById('devPauseBtn');
  if (toggleBtn) {
    toggleBtn.textContent = devEnabled ? 'ON' : 'OFF';
    toggleBtn.style.background = devEnabled ? 'var(--lcars-ok)' : 'var(--lcars-red)';
  }
  if (pauseBtn) {
    pauseBtn.style.display = devEnabled ? '' : 'none';
    pauseBtn.textContent = devPaused ? 'RESUME' : 'PAUSE';
    pauseBtn.style.background = devPaused ? 'var(--lcars-warning)' : 'var(--lcars-lavender)';
  }
}

function colorizeJSON(str) {
  var obj;
  try { obj = JSON.parse(str); } catch(e) { return escHtml(str); }
  var pretty = JSON.stringify(obj, null, 2);
  return pretty.replace(/("(?:\\.|[^"\\])*")\s*:/g, function(m, key) {
    return '<span class="j-key">' + escHtml(key) + '</span>:';
  }).replace(/:\s*("(?:\\.|[^"\\])*")/g, function(m, val) {
    return ': <span class="j-str">' + escHtml(val) + '</span>';
  }).replace(/:\s*(-?\d+\.?\d*(?:[eE][+-]?\d+)?)/g, function(m, val) {
    return ': <span class="j-num">' + val + '</span>';
  }).replace(/:\s*(true|false)/g, function(m, val) {
    return ': <span class="j-bool">' + val + '</span>';
  }).replace(/:\s*(null)/g, function(m, val) {
    return ': <span class="j-null">' + val + '</span>';
  }).replace(/([{}\[\]])/g, '<span class="j-brace">$1</span>');
}

function devCopyJSON(btn) {
  var pre = btn.parentElement.querySelector('.dev-pre');
  if (!pre) return;
  var text = pre.textContent || pre.innerText;
  navigator.clipboard.writeText(text).then(function() {
    btn.textContent = 'Copied!';
    setTimeout(function() { btn.textContent = 'Copy'; }, 1500);
  }).catch(function() {
    btn.textContent = 'Failed';
    setTimeout(function() { btn.textContent = 'Copy'; }, 1500);
  });
}

function appendDevEntry(d) {
  if (devPaused) return;
  var container = document.getElementById('devLogContainer');
  if (!container) return;
  var empty = document.getElementById('devEmptyState');
  if (empty) empty.remove();

  var isInbound = d.direction === 'inbound';
  var statusOk = d.status_code >= 200 && d.status_code < 300;
  var method = d.method || '?';
  var ts = '';
  if (d.timestamp) {
    var dt = new Date(d.timestamp);
    ts = dt.toLocaleTimeString('en-GB', {hour12:false}) + '.' + String(dt.getMilliseconds()).padStart(3,'0');
  }

  var html = '<div class="dev-entry">';
  html += '<div class="dev-entry-header">';
  html += '<span class="dev-dir dev-dir-' + (d.direction||'outbound') + '">' + (isInbound ? 'IN' : 'OUT') + '</span>';
  html += '<span class="dev-method dev-method-' + method + '">' + method + '</span>';
  html += '<span class="dev-url">' + escHtml(d.url||'') + '</span>';
  if (d.source) html += '<span class="dev-source">' + escHtml(d.source) + '</span>';
  if (d.status_code) html += '<span class="dev-status dev-status-' + (statusOk?'ok':'err') + '">' + d.status_code + '</span>';
  if (d.duration_ms) html += '<span class="dev-duration">' + d.duration_ms + 'ms</span>';
  html += '<span class="dev-time">' + ts + '</span>';
  if (d.remote_addr) html += '<span class="dev-remote">' + escHtml(d.remote_addr) + '</span>';
  if (d.error) html += '<span class="dev-error-tag">ERR</span>';
  html += '</div>';
  if (d.request_body) {
    var bodyLabel = isInbound ? 'Webhook Payload' : 'Request Body';
    html += '<details class="dev-details"><summary>' + bodyLabel + '</summary><div class="dev-pre-wrap"><button class="dev-copy-btn" onclick="devCopyJSON(this)">Copy</button><pre class="dev-pre">' + colorizeJSON(d.request_body) + '</pre></div></details>';
  }
  if (d.response_body) {
    html += '<details class="dev-details"><summary>Response Body</summary><div class="dev-pre-wrap"><button class="dev-copy-btn" onclick="devCopyJSON(this)">Copy</button><pre class="dev-pre">' + colorizeJSON(d.response_body) + '</pre></div></details>';
  }
  if (d.error) {
    html += '<div class="dev-error-msg">' + escHtml(d.error) + '</div>';
  }
  html += '</div>';

  container.insertAdjacentHTML('afterbegin', html);

  while (container.children.length > 200) {
    container.removeChild(container.lastChild);
  }
}

function escHtml(s) {
  if (s == null) return '';
  return String(s)
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");
}

// Check initial debug state from server
fetch('/admin/debug/toggle', { credentials: 'include' }).then(function(r) { return r.json(); }).then(function(data) {
  devEnabled = data.enabled;
  devUpdateButtons();
}).catch(function() {});

if (typeof EventSource !== 'undefined') {
  var devEs = new EventSource('/status/beauty/events');
  devEs.addEventListener('debug', function(e) {
    try {
      var data = JSON.parse(e.data);
      appendDevEntry(data);
    } catch(err) {}
  });
}
{{end}}

function loadServiceDetail(panel, service, host) {
  var block = panel.querySelector('#svc-detail-block');
  if (!block) return;
  var url = '/status/' + encodeURIComponent(service);
  if (host) url += '?host=' + encodeURIComponent(host);
  fetch(url, { credentials: 'include' }).then(function(r) { return r.json(); }).then(function(d) {
    var exitLabels = ['OK','WARNING','CRITICAL','UNKNOWN'];
    var exitClasses = ['ok','warning','critical','unknown'];
    var exitCode = (d.last_check_result && d.last_check_result.exit_status != null) ? d.last_check_result.exit_status : -1;
    var exitLabel = exitCode >= 0 && exitCode <= 3 ? exitLabels[exitCode] : 'N/A';
    var exitCls = exitCode >= 0 && exitCode <= 3 ? exitClasses[exitCode] : '';
    var output = (d.last_check_result && d.last_check_result.output) ? d.last_check_result.output : '-';
    var lastCheck = '-';
    if (d.last_check_result && d.last_check_result.timestamp) {
      var dt = new Date(d.last_check_result.timestamp);
      lastCheck = dt.toLocaleDateString('en-GB') + ' ' + dt.toLocaleTimeString('en-GB', {hour12:false});
    }
    var cacheState = d.cache_state || '-';
    var inIcinga = d.exists_in_icinga ? 'Yes' : 'No';
    var html = '<div class="svc-detail-grid">';
    html += '<div class="svc-detail-item"><span class="svc-detail-label">Status</span><span class="svc-detail-value ' + exitCls + '">' + exitLabel + '</span></div>';
    html += '<div class="svc-detail-item"><span class="svc-detail-label">Cache</span><span class="svc-detail-value">' + escHtml(cacheState) + '</span></div>';
    html += '<div class="svc-detail-item"><span class="svc-detail-label">Last Check</span><span class="svc-detail-value">' + lastCheck + '</span></div>';
    html += '<div class="svc-detail-item"><span class="svc-detail-label">In Icinga</span><span class="svc-detail-value">' + inIcinga + '</span></div>';
    html += '<div class="svc-detail-item" style="grid-column:1/-1"><span class="svc-detail-label">Output</span><span class="svc-detail-value ' + exitCls + '">' + escHtml(output) + '</span></div>';
    if (d.is_frozen) {
      var frozenLabel = d.frozen_until
        ? 'FROZEN until ' + new Date(d.frozen_until).toLocaleString('en-GB', {hour12:false})
        : 'FROZEN — permanent (alerts suppressed)';
      html += '<div class="svc-detail-frozen-row">&#10052; ' + frozenLabel + '</div>';
    }
    html += '</div>';
    block.innerHTML = html;
    // Refresh freeze button state in the panel
    var panel = block.closest('.svc-history-panel');
    if (panel) { _updateFreezeBtn(panel, d.is_frozen, d.frozen_until || null); }
  }).catch(function() {
    block.innerHTML = '<div class="svc-detail-loading">Sensor data unavailable</div>';
  });
}

function loadServiceHistoryBody(panel, service, host) {
  var container = panel.querySelector('#svc-history-entries') || panel.querySelector('.svc-history-body');
  container.innerHTML = '<div class="svc-history-loading">Loading history...</div>';
  var since = new Date(Date.now() - 24*60*60*1000).toISOString();
  var url = '/history?service=' + encodeURIComponent(service) + '&limit=200&from=' + encodeURIComponent(since);
  if (host) url += '&host=' + encodeURIComponent(host);
  fetch(url, { credentials: 'include' }).then(function(r) { return r.json(); }).then(function(data) {
    var entries = data.entries || data;
    if (!entries || entries.length === 0) {
      container.innerHTML = '<div class="svc-history-empty">No history entries found</div>';
      return;
    }
    var html = '';
    for (var i = 0; i < entries.length; i++) {
      var e = entries[i];
      var ts = '';
      if (e.timestamp) {
        var dt = new Date(e.timestamp);
        ts = dt.toLocaleDateString('en-GB') + ' ' + dt.toLocaleTimeString('en-GB', {hour12:false});
      }
      var action = (e.action || '').toLowerCase();
      var actionClass = 'svc-history-action-' + action;
      var exitClass = 'svc-history-exit-' + (e.exit_status || 0);
      html += '<div class="svc-history-row">';
      html += '<span class="svc-history-time">' + ts + '</span>';
      html += '<span class="svc-history-action ' + actionClass + '">' + escHtml(e.action || '') + '</span>';
      if (e.mode === 'manual') {
        var who = (e.source_key || '').replace('admin:', '');
        html += '<span class="svc-history-manual">MANUAL by ' + escHtml(who) + '</span>';
      }
      html += '<span class="svc-history-exit ' + exitClass + '">EXIT ' + (e.exit_status != null ? e.exit_status : '?') + '</span>';
      html += '<span class="svc-history-msg" title="' + escHtml(e.message || '') + '">' + escHtml(e.message || '') + '</span>';
      html += '</div>';
    }
    container.innerHTML = html;
  }).catch(function() {
    container.innerHTML = '<div class="svc-history-empty">Failed to load history</div>';
  });
}

function showServiceHistory(service, host) {
  var overlay = document.createElement('div');
  overlay.className = 'svc-history-overlay';
  overlay.onclick = function(e) { if (e.target === overlay) overlay.remove(); };
  var panel = document.createElement('div');
  panel.className = 'svc-history-panel';
  panel.dataset.service = service;
  panel.dataset.host = host || '';
  var title = host ? host + ' / ' + service : service;
  var statusBtns = '';
  var freezeControls = '';
  if (_canChangeStatus) {
    statusBtns = '<div class="svc-status-buttons">' +
      '<button class="svc-status-btn svc-status-btn-ok" onclick="setServiceStatus(\'' + escHtml(host) + '\',\'' + escHtml(service) + '\',0,this)">OK</button>' +
      '<button class="svc-status-btn svc-status-btn-warning" onclick="setServiceStatus(\'' + escHtml(host) + '\',\'' + escHtml(service) + '\',1,this)">Warning</button>' +
      '<button class="svc-status-btn svc-status-btn-critical" onclick="setServiceStatus(\'' + escHtml(host) + '\',\'' + escHtml(service) + '\',2,this)">Critical</button>' +
      '</div><div class="svc-status-result" id="svc-status-result"></div>';
    freezeControls =
      '<div class="svc-freeze-controls" id="svc-freeze-controls">' +
        '<select class="svc-freeze-select" id="svc-freeze-duration">' +
          '<option value="0">Permanent</option>' +
          '<option value="600">10 min</option>' +
          '<option value="900">15 min</option>' +
          '<option value="1800">30 min</option>' +
          '<option value="3600">60 min</option>' +
          '<option value="7200">2 h</option>' +
          '<option value="86400">1 day</option>' +
          '<option value="604800">7 days</option>' +
        '</select>' +
        '<button class="svc-freeze-btn" id="svc-freeze-btn" onclick="freezeService(\'' + escHtml(host) + '\',\'' + escHtml(service) + '\',this)">Freeze</button>' +
        '<button class="svc-freeze-btn svc-freeze-btn-unfreeze" id="svc-unfreeze-btn" style="display:none" onclick="unfreezeService(\'' + escHtml(host) + '\',\'' + escHtml(service) + '\',this)">Unfreeze</button>' +
      '</div>' +
      '<div class="svc-status-result" id="svc-freeze-result"></div>';
  }
  panel.innerHTML = '<div class="svc-history-header"><span class="svc-history-title">' + escHtml(title) + '</span><button class="svc-history-close" onclick="this.closest(\'.svc-history-overlay\').remove()">Close</button></div>' +
    '<div class="svc-detail-block" id="svc-detail-block"><div class="svc-detail-loading">Querying sensor data...</div></div>' +
    statusBtns +
    freezeControls +
    '<div class="svc-history-body"><div class="svc-history-body-title">Transmission Log</div><div id="svc-history-entries"><div class="svc-history-loading">Loading history...</div></div></div>';
  overlay.appendChild(panel);
  document.body.appendChild(overlay);
  loadServiceDetail(panel, service, host);
  loadServiceHistoryBody(panel, service, host);
}

function setServiceStatus(host, service, exitStatus, btn) {
  var labels = ['OK', 'WARNING', 'CRITICAL', 'UNKNOWN'];
  var resultEl = document.getElementById('svc-status-result');
  var btns = btn.parentElement.querySelectorAll('.svc-status-btn');
  for (var i = 0; i < btns.length; i++) btns[i].disabled = true;
  if (resultEl) { resultEl.style.color = 'var(--lcars-blue)'; resultEl.textContent = 'Transmitting...'; }

  fetch('/admin/services/' + encodeURIComponent(service) + '/status', {
    method: 'POST',
    credentials: 'include',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ host: host, exit_status: exitStatus })
  }).then(function(r) { return r.json().then(function(d) { return { ok: r.ok, data: d }; }); })
  .then(function(res) {
    for (var i = 0; i < btns.length; i++) btns[i].disabled = false;
    if (res.ok) {
      if (resultEl) { resultEl.style.color = 'var(--lcars-ok)'; resultEl.textContent = 'Status set to ' + labels[exitStatus]; }
      var panel = btn.closest('.svc-history-panel');
      if (panel) {
        setTimeout(function() { loadServiceHistoryBody(panel, panel.dataset.service, panel.dataset.host); }, 500);
      }
    } else {
      if (resultEl) { resultEl.style.color = 'var(--lcars-critical)'; resultEl.textContent = res.data.error || 'Failed'; }
    }
  }).catch(function(err) {
    for (var i = 0; i < btns.length; i++) btns[i].disabled = false;
    if (resultEl) { resultEl.style.color = 'var(--lcars-critical)'; resultEl.textContent = 'Connection failed'; }
  });
}

function _updateFreezeBtn(panel, isFrozen, frozenUntil) {
  var freezeBtn = panel.querySelector('#svc-freeze-btn');
  var unfreezeBtn = panel.querySelector('#svc-unfreeze-btn');
  var sel = panel.querySelector('#svc-freeze-duration');
  if (!freezeBtn || !unfreezeBtn) return;
  if (isFrozen) {
    freezeBtn.style.display = 'none';
    if (sel) sel.style.display = 'none';
    unfreezeBtn.style.display = '';
  } else {
    freezeBtn.style.display = '';
    if (sel) sel.style.display = '';
    unfreezeBtn.style.display = 'none';
  }
}

function freezeService(host, service, btn) {
  var panel = btn.closest('.svc-history-panel');
  var sel = panel ? panel.querySelector('#svc-freeze-duration') : null;
  var duration = sel ? parseInt(sel.value, 10) : 0;
  var resultEl = panel ? panel.querySelector('#svc-freeze-result') : null;
  btn.disabled = true;
  if (resultEl) { resultEl.style.color = 'var(--lcars-blue)'; resultEl.textContent = 'Freezing...'; }

  fetch('/admin/services/' + encodeURIComponent(service) + '/freeze', {
    method: 'POST',
    credentials: 'include',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ host: host, duration_seconds: duration })
  }).then(function(r) { return r.json().then(function(d) { return { ok: r.ok, data: d }; }); })
  .then(function(res) {
    btn.disabled = false;
    if (res.ok) {
      var msg = res.data.frozen_until
        ? 'Frozen until ' + new Date(res.data.frozen_until).toLocaleString('en-GB', {hour12:false})
        : 'Frozen permanently';
      if (resultEl) { resultEl.style.color = 'var(--lcars-blue)'; resultEl.textContent = msg; }
      _updateFreezeBtn(panel, true, res.data.frozen_until);
      if (panel) { setTimeout(function() { loadServiceDetail(panel, service, host); }, 300); }
      setTimeout(applyFrozenHighlight, 150);
    } else {
      if (resultEl) { resultEl.style.color = 'var(--lcars-critical)'; resultEl.textContent = res.data.error || 'Failed'; }
    }
  }).catch(function() {
    btn.disabled = false;
    if (resultEl) { resultEl.style.color = 'var(--lcars-critical)'; resultEl.textContent = 'Connection failed'; }
  });
}

function unfreezeService(host, service, btn) {
  var panel = btn.closest('.svc-history-panel');
  var resultEl = panel ? panel.querySelector('#svc-freeze-result') : null;
  btn.disabled = true;
  if (resultEl) { resultEl.style.color = 'var(--lcars-blue)'; resultEl.textContent = 'Unfreezing...'; }

  fetch('/admin/services/' + encodeURIComponent(service) + '/freeze', {
    method: 'DELETE',
    credentials: 'include',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ host: host })
  }).then(function(r) { return r.json().then(function(d) { return { ok: r.ok, data: d }; }); })
  .then(function(res) {
    btn.disabled = false;
    if (res.ok) {
      if (resultEl) { resultEl.style.color = 'var(--lcars-ok)'; resultEl.textContent = 'Service unfrozen'; }
      _updateFreezeBtn(panel, false, null);
      if (panel) { setTimeout(function() { loadServiceDetail(panel, service, host); }, 300); }
      setTimeout(applyFrozenHighlight, 150);
    } else {
      if (resultEl) { resultEl.style.color = 'var(--lcars-critical)'; resultEl.textContent = res.data.error || 'Failed'; }
    }
  }).catch(function() {
    btn.disabled = false;
    if (resultEl) { resultEl.style.color = 'var(--lcars-critical)'; resultEl.textContent = 'Connection failed'; }
  });
}

function applyFrozenHighlight() {
  if (!_canChangeStatus) return;
  fetch('/admin/services/frozen', { credentials: 'include' })
    .then(function(r) { return r.json(); })
    .then(function(data) {
      var items = data.frozen || [];
      // Build lookup set keyed by host\x1fservice
      var frozenSet = {};
      for (var i = 0; i < items.length; i++) {
        frozenSet[(items[i].host || '') + '\x1f' + items[i].service] = items[i];
      }
      // Remove old highlights from all rows
      document.querySelectorAll('tr.frozen-row').forEach(function(tr) {
        tr.classList.remove('frozen-row');
        tr.querySelectorAll('.frozen-inline-badge').forEach(function(b) { b.remove(); });
        tr.querySelectorAll('.frozen-unfreeze-btn').forEach(function(b) { b.remove(); });
      });
      // Apply frozen highlight to all table rows — ❄ badge only, no inline button
      // (Unfreeze is available in the service detail panel)
      document.querySelectorAll('tr[data-service]').forEach(function(tr) {
        var svc  = tr.dataset.service || '';
        var host = tr.dataset.host   || '';
        if (!frozenSet[host + '\x1f' + svc]) return;
        tr.classList.add('frozen-row');
        var nameEl = tr.querySelector('strong');
        if (nameEl && !nameEl.querySelector('.frozen-inline-badge')) {
          var badge = document.createElement('span');
          badge.className = 'frozen-inline-badge';
          badge.title = 'Service is frozen — alerts suppressed';
          badge.textContent = ' ❄';
          nameEl.appendChild(badge);
        }
      });
      // Update sidebar badge
      var badge = document.getElementById('frozen-count-badge');
      if (badge) {
        if (items.length > 0) { badge.textContent = items.length; badge.style.display = ''; }
        else { badge.style.display = 'none'; }
      }
    })
    .catch(function() {});
}

function loadFrozenList() {
  var container = document.getElementById('frozen-list-container');
  if (!container) return;
  container.innerHTML = '<div class="empty-state">Loading...</div>';
  fetch('/admin/services/frozen', { credentials: 'include' })
    .then(function(r) { return r.json(); })
    .then(function(data) {
      var items = data.frozen || [];
      // Update badge
      var badge = document.getElementById('frozen-count-badge');
      if (badge) {
        if (items.length > 0) { badge.textContent = items.length; badge.style.display = ''; }
        else { badge.style.display = 'none'; }
      }
      if (items.length === 0) {
        container.innerHTML = '<div class="empty-state">No frozen services</div>';
        return;
      }
      var html = '<table style="width:100%;border-collapse:collapse;">';
      html += '<thead><tr style="color:var(--lcars-text-light);font-size:11px;letter-spacing:1px;text-transform:uppercase;">';
      html += '<th style="text-align:left;padding:6px 8px;">Host</th>';
      html += '<th style="text-align:left;padding:6px 8px;">Service</th>';
      html += '<th style="text-align:left;padding:6px 8px;">Frozen Until</th>';
      html += '<th style="padding:6px 8px;"></th>';
      html += '</tr></thead><tbody>';
      for (var i = 0; i < items.length; i++) {
        var e = items[i];
        var until = e.frozen_until
          ? new Date(e.frozen_until).toLocaleString('en-GB', {hour12:false})
          : '<span style="color:var(--lcars-blue)">Permanent</span>';
        html += '<tr style="border-top:1px solid rgba(255,255,255,0.05);">';
        html += '<td class="mono" style="padding:7px 8px;">' + escHtml(e.host) + '</td>';
        html += '<td style="padding:7px 8px;"><strong class="svc-link js-service-history-trigger" data-service="' + escHtml(e.service) + '" data-host="' + escHtml(e.host) + '">' + escHtml(e.service) + '</strong></td>';
        html += '<td style="padding:7px 8px;font-size:12px;">' + until + '</td>';
        html += '<td style="padding:7px 8px;text-align:right;">';
        html += '<button class="svc-freeze-btn svc-freeze-btn-unfreeze" style="padding:5px 14px;font-size:11px;" ';
        html += 'onclick="unfreezeFromList(\'' + escHtml(e.host) + '\',\'' + escHtml(e.service) + '\',this)">Unfreeze</button>';
        html += '</td></tr>';
      }
      html += '</tbody></table>';
      container.innerHTML = html;
    })
    .catch(function() {
      container.innerHTML = '<div class="empty-state">Failed to load frozen list</div>';
    });
}

function unfreezeFromList(host, service, btn) {
  btn.disabled = true;
  fetch('/admin/services/' + encodeURIComponent(service) + '/freeze', {
    method: 'DELETE',
    credentials: 'include',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ host: host })
  }).then(function(r) { return r.json(); })
  .then(function() { loadFrozenList(); applyFrozenHighlight(); })
  .catch(function() { btn.disabled = false; });
}

// ── Settings Panel ──
function settingsShowStatus(msg, isError) {
  var el = document.getElementById('settingsStatus');
  if (!el) return;
  el.className = 'settings-status ' + (isError ? 'err' : 'ok');
  el.textContent = msg;
  setTimeout(function() { el.className = 'settings-status'; el.textContent = ''; }, 6000);
}

function loadSettings() {
  fetch('/admin/settings', { method: 'GET', credentials: 'include' })
    .then(function(r) { if (!r.ok) throw new Error('HTTP ' + r.status); return r.json(); })
    .then(function(cfg) {
      document.getElementById('cfg-icinga2-host').value = cfg.icinga2_host || '';
      document.getElementById('cfg-icinga2-user').value = cfg.icinga2_user || '';
      document.getElementById('cfg-icinga2-pass').value = '';
      document.getElementById('cfg-icinga2-pass').placeholder = (cfg.icinga2_pass === '***') ? '(set)' : '(not set)';
      document.getElementById('cfg-icinga2-tls-skip').checked = !!cfg.icinga2_tls_skip_verify;
      document.getElementById('cfg-icinga2-auto-create').checked = !!cfg.icinga2_host_auto_create;
      document.getElementById('cfg-icinga2-conflict-policy').value = cfg.icinga2_conflict_policy || 'warn';
      document.getElementById('cfg-icinga2-force').checked = !!cfg.icinga2_force;

      document.getElementById('cfg-history-file').value = cfg.history_file || '';
      document.getElementById('cfg-history-max').value = cfg.history_max_entries || '';
      document.getElementById('cfg-cache-ttl').value = cfg.cache_ttl_minutes || '';

      document.getElementById('cfg-log-level').value = cfg.log_level || 'info';
      document.getElementById('cfg-log-format').value = cfg.log_format || 'json';

      document.getElementById('cfg-rl-mutate').value = cfg.ratelimit_mutate_max || '';
      document.getElementById('cfg-rl-status').value = cfg.ratelimit_status_max || '';
      document.getElementById('cfg-rl-queue').value = cfg.ratelimit_max_queue || '';

      if (cfg.targets) {
        renderTargetCards(cfg.targets);
      }
      settingsShowStatus('Configuration loaded', false);
    })
    .catch(function(err) {
      settingsShowStatus('Failed to load configuration: ' + err.message, true);
    });
}

function saveSettings() {
  var passVal = document.getElementById('cfg-icinga2-pass').value;
  var payload = {
    icinga2_host: document.getElementById('cfg-icinga2-host').value,
    icinga2_user: document.getElementById('cfg-icinga2-user').value,
    icinga2_tls_skip_verify: document.getElementById('cfg-icinga2-tls-skip').checked,
    icinga2_host_auto_create: document.getElementById('cfg-icinga2-auto-create').checked,
    icinga2_conflict_policy: document.getElementById('cfg-icinga2-conflict-policy').value,
    icinga2_force: document.getElementById('cfg-icinga2-force').checked,
    history_file: document.getElementById('cfg-history-file').value,
    history_max_entries: parseInt(document.getElementById('cfg-history-max').value) || 0,
    cache_ttl_minutes: parseInt(document.getElementById('cfg-cache-ttl').value) || 0,
    log_level: document.getElementById('cfg-log-level').value,
    log_format: document.getElementById('cfg-log-format').value,
    ratelimit_mutate_max: parseInt(document.getElementById('cfg-rl-mutate').value) || 0,
    ratelimit_status_max: parseInt(document.getElementById('cfg-rl-status').value) || 0,
    ratelimit_max_queue: parseInt(document.getElementById('cfg-rl-queue').value) || 0
  };
  if (passVal) { payload.icinga2_pass = passVal; }
  fetch('/admin/settings', {
    method: 'PATCH',
    credentials: 'include',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload)
  })
    .then(function(r) { if (!r.ok) throw new Error('HTTP ' + r.status); return r.json(); })
    .then(function(res) {
      settingsShowStatus('Configuration saved successfully', false);
    })
    .catch(function(err) {
      settingsShowStatus('Failed to save configuration: ' + err.message, true);
    });
}

function renderTargetCards(targets) {
  var container = document.getElementById('settingsTargets');
  if (!container) return;
  if (!targets || targets.length === 0) {
    container.innerHTML = '<div style="color:var(--lcars-tan);font-size:12px;opacity:0.6;">No targets configured</div>';
    return;
  }
  var html = '';
  targets.forEach(function(t) {
    var safeId = escHtml(t.id || 'unknown');
    var safeHost = escHtml(t.host_name || '-');
    html += '<div class="settings-target-card">';
    html += '<div class="settings-target-header">';
    html += '<span class="settings-target-title">' + safeId + '</span>';
    html += '<div><button class="settings-btn blue settings-btn-sm" onclick="generateKey(\'' + encodeURIComponent(t.id) + '\')">Generate Key</button> ';
    html += '<button class="settings-btn danger settings-btn-sm" onclick="deleteTarget(\'' + encodeURIComponent(t.id) + '\')">Delete</button></div>';
    html += '</div>';
    html += '<div class="settings-grid" style="margin-bottom:8px;">';
    html += '<span class="settings-label">Host</span><span style="color:var(--lcars-text-light);font-size:13px;">' + safeHost + '</span>';
    html += '</div>';
    html += '<div class="settings-key-list" id="key-list-' + encodeURIComponent(t.id) + '">';
    if (t.api_keys && t.api_keys.length > 0) {
      t.api_keys.forEach(function(k, idx) {
        html += '<div class="settings-key-item">';
        html += '<span class="settings-key-value" id="key-val-' + encodeURIComponent(t.id) + '-' + idx + '">&#x2022;&#x2022;&#x2022;&#x2022;&#x2022;&#x2022;&#x2022;&#x2022;</span>';
        html += '<button class="settings-key-copy" title="Copy key" onclick="copyRevealedKey(\'' + encodeURIComponent(t.id) + '\',' + idx + ')">&#x1F4CB;</button>';
        html += '</div>';
      });
      html += '<button class="settings-btn settings-btn-sm" id="reveal-btn-' + encodeURIComponent(t.id) + '" style="margin-top:4px;background:var(--lcars-blue);font-size:11px;" onclick="toggleKeys(\'' + encodeURIComponent(t.id) + '\')">Reveal Keys</button>';
    } else {
      html += '<div style="color:var(--lcars-tan);font-size:11px;opacity:0.5;">No API keys</div>';
    }
    html += '</div>';
    html += '</div>';
  });
  container.innerHTML = html;
}

function addNewTarget() {
  var overlay = document.createElement('div');
  overlay.className = 'target-popup-overlay';
  overlay.onclick = function(e) { if (e.target === overlay) overlay.remove(); };

  var popup = document.createElement('div');
  popup.className = 'target-popup';
  popup.innerHTML = '<div class="target-popup-header"><span>New Target Registration</span><button class="target-popup-close" onclick="this.closest(\'.target-popup-overlay\').remove()">&times;</button></div>'
    + '<div class="target-popup-body">'
    + '<div class="target-popup-row"><label class="target-popup-label">Target ID</label><input class="target-popup-input" id="new-target-id" placeholder="e.g. grafana-prod" autocomplete="off" /><div class="target-popup-hint">Unique identifier for this target</div></div>'
    + '<div class="target-popup-row"><label class="target-popup-label">Source</label><input class="target-popup-input" id="new-target-source" placeholder="Auto-filled from Target ID" autocomplete="off" /><div class="target-popup-hint">Webhook source name (optional, defaults to Target ID)</div></div>'
    + '<div class="target-popup-divider"></div>'
    + '<div class="target-popup-row"><label class="target-popup-label">Host Name</label><input class="target-popup-input" id="new-target-host" placeholder="e.g. my-server-01" autocomplete="off" /><div class="target-popup-hint">Host object name registered in Icinga2</div></div>'
    + '<div class="target-popup-row"><label class="target-popup-label">Display Name</label><input class="target-popup-input" id="new-target-display" placeholder="e.g. My Server 01" autocomplete="off" /><div class="target-popup-hint">Friendly display name (optional)</div></div>'
    + '</div>'
    + '<div class="target-popup-actions"><button class="target-popup-btn cancel" onclick="this.closest(\'.target-popup-overlay\').remove()">Cancel</button><button class="target-popup-btn confirm" onclick="submitNewTarget()">Engage</button></div>';

  overlay.appendChild(popup);
  document.body.appendChild(overlay);
  setTimeout(function() { document.getElementById('new-target-id').focus(); }, 100);
}

function submitNewTarget() {
  var id = document.getElementById('new-target-id').value.trim();
  var source = document.getElementById('new-target-source').value.trim();
  var host = document.getElementById('new-target-host').value.trim();
  var display = document.getElementById('new-target-display').value.trim();

  if (!id) { document.getElementById('new-target-id').style.borderColor = 'var(--lcars-critical)'; return; }
  if (!host) { document.getElementById('new-target-host').style.borderColor = 'var(--lcars-critical)'; return; }

  var overlay = document.querySelector('.target-popup-overlay');
  fetch('/admin/settings/targets', {
    method: 'POST',
    credentials: 'include',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ id: id, source: source || id, host_name: host, host_display: display || host, host_address: '127.0.0.1' })
  })
    .then(function(r) { if (!r.ok) throw new Error('HTTP ' + r.status); return r.json(); })
    .then(function(res) {
      if (overlay) overlay.remove();
      if (res.api_key) {
        showNewTargetKey(id, res.api_key);
      } else {
        settingsShowStatus('Target "' + id + '" registered', false);
      }
      loadSettings();
    })
    .catch(function(err) {
      settingsShowStatus('Failed to add target: ' + err.message, true);
    });
}

function showNewTargetKey(targetId, key) {
  var overlay = document.createElement('div');
  overlay.className = 'target-popup-overlay';

  var popup = document.createElement('div');
  popup.className = 'target-popup';
  popup.innerHTML = '<div class="target-popup-header" style="background:var(--lcars-ok);"><span>Target Registered</span></div>'
    + '<div class="target-popup-body">'
    + '<div style="color:var(--lcars-ok);font-family:Orbitron,sans-serif;font-size:10px;letter-spacing:1.5px;text-transform:uppercase;margin-bottom:12px;">Target "' + escHtml(targetId) + '" is online</div>'
    + '<div class="target-popup-row"><label class="target-popup-label" style="color:var(--lcars-critical);">API Key — Copy Now, Shown Only Once</label>'
    + '<input class="target-popup-input" id="new-target-key-display" value="' + escHtml(key) + '" readonly style="font-family:monospace;font-size:13px;border-color:var(--lcars-ok);" onclick="this.select();" />'
    + '</div>'
    + '</div>'
    + '<div class="target-popup-actions"><button class="target-popup-btn" style="background:var(--lcars-blue);color:#000;" onclick="copyNewTargetKey()">Copy Key</button><button class="target-popup-btn confirm" onclick="this.closest(\'.target-popup-overlay\').remove()">Dismiss</button></div>';

  overlay.appendChild(popup);
  document.body.appendChild(overlay);
  setTimeout(function() { document.getElementById('new-target-key-display').select(); }, 100);
}

function copyNewTargetKey() {
  var el = document.getElementById('new-target-key-display');
  if (!el) return;
  if (navigator.clipboard) {
    navigator.clipboard.writeText(el.value).then(function() {
      settingsShowStatus('API key copied to clipboard', false);
    });
  } else {
    el.select();
    document.execCommand('copy');
    settingsShowStatus('API key copied', false);
  }
}

function deleteTarget(id) {
  if (!confirm('Delete target "' + id + '"? This cannot be undone.')) return;
  fetch('/admin/settings/targets/' + encodeURIComponent(id), {
    method: 'DELETE',
    credentials: 'include'
  })
    .then(function(r) { if (!r.ok) throw new Error('HTTP ' + r.status); return r.json(); })
    .then(function(res) {
      settingsShowStatus('Target "' + id + '" deleted', false);
      loadSettings();
    })
    .catch(function(err) {
      settingsShowStatus('Failed to delete target: ' + err.message, true);
    });
}

function generateKey(targetId) {
  fetch('/admin/settings/targets/' + encodeURIComponent(targetId) + '/generate-key', {
    method: 'POST',
    credentials: 'include'
  })
    .then(function(r) { if (!r.ok) throw new Error('HTTP ' + r.status); return r.json(); })
    .then(function(res) {
      if (res.api_key) {
        prompt('New API key (copy it now, shown only once):', res.api_key);
      }
      settingsShowStatus('New key generated for "' + targetId + '"', false);
      loadSettings();
    })
    .catch(function(err) {
      settingsShowStatus('Failed to generate key: ' + err.message, true);
    });
}

var _revealedKeys = {};
var _keysVisible = {};

function toggleKeys(targetId) {
  var btn = document.getElementById('reveal-btn-' + targetId);
  if (_keysVisible[targetId]) {
    // Hide keys
    var keys = _revealedKeys[targetId] || [];
    keys.forEach(function(k, idx) {
      var el = document.getElementById('key-val-' + targetId + '-' + idx);
      if (el) { el.textContent = '\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022'; el.style.fontFamily = ''; el.style.fontSize = ''; el.style.wordBreak = ''; }
    });
    _keysVisible[targetId] = false;
    if (btn) btn.textContent = 'Reveal Keys';
    return;
  }
  // Reveal keys
  if (_revealedKeys[targetId]) {
    showKeys(targetId);
    return;
  }
  fetch('/admin/settings/targets/' + encodeURIComponent(targetId) + '/reveal-keys', {
    method: 'GET',
    credentials: 'include'
  })
    .then(function(r) { if (!r.ok) throw new Error('HTTP ' + r.status); return r.json(); })
    .then(function(res) {
      _revealedKeys[targetId] = res.api_keys || [];
      showKeys(targetId);
    })
    .catch(function(err) {
      settingsShowStatus('Failed to reveal keys: ' + err.message, true);
    });
}

function showKeys(targetId) {
  var btn = document.getElementById('reveal-btn-' + targetId);
  var keys = _revealedKeys[targetId] || [];
  keys.forEach(function(key, idx) {
    var el = document.getElementById('key-val-' + targetId + '-' + idx);
    if (el) {
      el.textContent = key;
      el.style.fontFamily = 'monospace';
      el.style.fontSize = '11px';
      el.style.wordBreak = 'break-all';
    }
  });
  _keysVisible[targetId] = true;
  if (btn) btn.textContent = 'Hide Keys';
}

function copyRevealedKey(targetId, idx) {
  var keys = _revealedKeys[targetId];
  if (!keys || !keys[idx]) {
    settingsShowStatus('Reveal keys first before copying', true);
    return;
  }
  if (navigator.clipboard) {
    navigator.clipboard.writeText(keys[idx]).then(function() {
      settingsShowStatus('Key copied to clipboard', false);
    });
  } else {
    prompt('Copy this key:', keys[idx]);
  }
}

function testIcingaConnection() {
  var resultEl = document.getElementById('icingaTestResult');
  if (resultEl) { resultEl.textContent = 'Testing...'; resultEl.style.color = 'var(--lcars-tan)'; }
  fetch('/admin/settings/test-icinga', {
    method: 'POST',
    credentials: 'include',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      host: document.getElementById('cfg-icinga2-host').value,
      user: document.getElementById('cfg-icinga2-user').value,
      password: document.getElementById('cfg-icinga2-pass').value
    })
  })
    .then(function(r) { if (!r.ok) throw new Error('HTTP ' + r.status); return r.json(); })
    .then(function(res) {
      if (resultEl) {
        var ok = res.status === 'ok';
        resultEl.textContent = ok ? 'Connection OK — Icinga2 ' + (res.icinga2_version || '') : ('Failed: ' + (res.error || 'unknown'));
        resultEl.style.color = ok ? 'var(--lcars-ok)' : 'var(--lcars-critical)';
      }
    })
    .catch(function(err) {
      if (resultEl) {
        resultEl.textContent = 'Error: ' + err.message;
        resultEl.style.color = 'var(--lcars-critical)';
      }
    });
}

function importSettings(input) {
  if (!input.files || !input.files[0]) return;
  var file = input.files[0];
  if (!confirm('Import configuration from "' + file.name + '"? This will overwrite the current configuration. Masked secrets (***) will be preserved from the current config.')) {
    input.value = '';
    return;
  }
  var reader = new FileReader();
  reader.onload = function(e) {
    var data;
    try { data = JSON.parse(e.target.result); } catch(err) {
      settingsShowStatus('Invalid JSON file: ' + err.message, true);
      input.value = '';
      return;
    }
    fetch('/admin/settings/import', {
      method: 'POST',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(data)
    }).then(function(r) { return r.json().then(function(d) { return { ok: r.ok, data: d }; }); })
      .then(function(res) {
        if (!res.ok) {
          settingsShowStatus('Import failed: ' + (res.data.error || 'unknown error'), true);
        } else {
          settingsShowStatus('Configuration imported (' + res.data.targets + ' targets)', false);
          loadSettings();
        }
      })
      .catch(function(err) { settingsShowStatus('Import error: ' + err.message, true); });
    input.value = '';
  };
  reader.readAsText(file);
}

function exportSettings() {
  fetch('/admin/settings/export', { method: 'GET', credentials: 'include' })
    .then(function(r) { if (!r.ok) throw new Error('HTTP ' + r.status); return r.blob(); })
    .then(function(blob) {
      var url = URL.createObjectURL(blob);
      var a = document.createElement('a');
      a.href = url;
      a.download = 'icinga-alertforge-config-' + new Date().toISOString().slice(0,10) + '.json';
      document.body.appendChild(a);
      a.click();
      document.body.removeChild(a);
      URL.revokeObjectURL(url);
      settingsShowStatus('Configuration exported', false);
    })
    .catch(function(err) {
      settingsShowStatus('Failed to export: ' + err.message, true);
    });
}

// ── RBAC User Management ──
function rbacLoadUsers() {
  fetch('/admin/users', { method: 'GET', credentials: 'include' })
    .then(function(r) { if (!r.ok) throw new Error('HTTP ' + r.status); return r.json(); })
    .then(function(users) {
      var tbody = document.getElementById('rbacUsersBody');
      if (!tbody) return;
      if (!Array.isArray(users) || users.length === 0) {
        tbody.innerHTML = '<tr><td colspan="3" style="padding:10px;color:var(--lcars-tan);font-size:12px;">No users configured</td></tr>';
        return;
      }
      var roleColors = { admin: 'var(--lcars-critical)', operator: 'var(--lcars-warning)', viewer: 'var(--lcars-blue)' };
      tbody.innerHTML = users.map(function(u) {
        var color = roleColors[u.role] || 'var(--lcars-tan)';
        var delBtn = (u.username === _primaryAdmin)
          ? '<span style="font-size:11px;color:var(--lcars-tan);opacity:0.5;">(env)</span>'
          : '<button class="settings-btn settings-btn-sm" style="background:var(--lcars-critical);color:#000;padding:2px 8px;font-size:11px;" onclick="rbacDeleteUser(\'' + u.username + '\')">Delete</button>';
        return '<tr style="border-bottom:1px solid rgba(204,153,204,0.15);">' +
          '<td style="padding:6px;color:var(--lcars-peach);font-size:13px;">' + u.username + (u.username === _primaryAdmin ? ' <span style="font-size:10px;color:var(--lcars-tan);">★ primary</span>' : '') + '</td>' +
          '<td style="padding:6px;"><span style="padding:2px 8px;border-radius:4px;background:' + color + ';color:#000;font-size:11px;font-weight:700;letter-spacing:1px;text-transform:uppercase;">' + u.role + '</span></td>' +
          '<td style="padding:6px;">' + delBtn + '</td></tr>';
      }).join('');
    })
    .catch(function(err) {
      var tbody = document.getElementById('rbacUsersBody');
      if (tbody) tbody.innerHTML = '<tr><td colspan="3" style="padding:10px;color:var(--lcars-critical);font-size:12px;">Failed to load users: ' + err.message + '</td></tr>';
    });
}

function rbacAddUser() {
  var username = document.getElementById('rbac-new-user').value.trim();
  var password = document.getElementById('rbac-new-pass').value;
  var role = document.getElementById('rbac-new-role').value;
  if (!username || !password) { alert('Username and password required'); return; }
  fetch('/admin/users', {
    method: 'POST', credentials: 'include',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username: username, password: password, role: role })
  })
    .then(function(r) { if (!r.ok) throw new Error('HTTP ' + r.status); return r.json(); })
    .then(function() {
      document.getElementById('rbac-new-user').value = '';
      document.getElementById('rbac-new-pass').value = '';
      settingsShowStatus('User "' + username + '" added with role: ' + role, false);
      rbacLoadUsers();
    })
    .catch(function(err) { settingsShowStatus('Failed to add user: ' + err.message, true); });
}

function rbacDeleteUser(username) {
  if (!confirm('Delete user "' + username + '"?')) return;
  fetch('/admin/users/' + encodeURIComponent(username), {
    method: 'DELETE', credentials: 'include'
  })
    .then(function(r) { if (!r.ok) throw new Error('HTTP ' + r.status); return r.json(); })
    .then(function() {
      settingsShowStatus('User "' + username + '" deleted', false);
      rbacLoadUsers();
    })
    .catch(function(err) { settingsShowStatus('Failed to delete user: ' + err.message, true); });
}

function doLogout() {
  // Set logout cookie so server forces fresh 401 on next admin login
  document.cookie = '_logged_out=1;path=/';
  window.location.href = '/status/beauty';
}

// ── Preserve section on auto-refresh ──
function applySectionFromHash() {
  const hash = window.location.hash.replace('#', '') || 'overview';
  showSection(hash, null, false);
}

window.addEventListener('hashchange', applySectionFromHash);
applySectionFromHash();

// ── Auto-refresh (preserves URL hash unlike meta refresh) ──
setTimeout(function() { window.location.reload(); }, 30000);

// ── Signal Sources toggle ──
function toggleSourceDetail(source) {
  var el = document.getElementById('source-detail-' + source);
  if (el) el.style.display = el.style.display === 'none' ? 'block' : 'none';
}

function clearHistory() {
  if (!confirm('Clear all history entries? This cannot be undone.')) return;
  var xhr = new XMLHttpRequest();
  xhr.open('POST', '/admin/history/clear', true);
  xhr.withCredentials = true;
  xhr.onload = function() {
    if (xhr.status === 200) { location.reload(); }
    else { alert('Error: HTTP ' + xhr.status); }
  };
  xhr.onerror = function() { alert('Request failed'); };
  xhr.send();
}

function switchIPTab(source, tab, btn) {
  var lastEl = document.getElementById('ip-last-' + source);
  var topEl = document.getElementById('ip-top-' + source);
  if (!lastEl || !topEl) return;
  lastEl.style.display = tab === 'last' ? 'block' : 'none';
  topEl.style.display = tab === 'top' ? 'block' : 'none';
  var tabs = btn.parentElement.querySelectorAll('.ip-tab');
  for (var i = 0; i < tabs.length; i++) tabs[i].classList.remove('active');
  btn.classList.add('active');
}

// ── Webhook Flow Animation (SSE real-time) ──
(function() {
  var flowLine = document.getElementById('webhookFlowLine');
  if (!flowLine) return;

  function spawnOrb(statusClass) {
    var orbClass = 'ok';
    if (statusClass === 'critical' || statusClass === 'error') orbClass = 'critical';
    else if (statusClass === 'warning') orbClass = 'warning';

    var orb = document.createElement('div');
    orb.className = 'webhook-orb ' + orbClass;
    flowLine.appendChild(orb);
    orb.addEventListener('animationend', function() { orb.remove(); });
  }

  // Spawn initial orbs from page data (last alerts)
  var recentAlerts = [
    {{range .RecentAlerts}}{status: "{{.StatusClass}}"},
    {{end}}
  ];
  var initCount = Math.min(recentAlerts.length, 5);
  for (var i = 0; i < initCount; i++) {
    (function(idx) {
      setTimeout(function() { spawnOrb(recentAlerts[idx].status); }, idx * 600);
    })(i);
  }

  // SSE real-time connection
  function incCounter(id) {
    var el = document.getElementById(id);
    if (el) el.textContent = parseInt(el.textContent || '0') + 1;
  }

  // ── Live refresh: Recent Transmissions table ──
  var _alertsRefreshTimer = null;
  function refreshAlertsTable() {
    fetch('/history?limit=100', { credentials: 'include' }).then(function(r) {
      if (!r.ok) return;
      return r.json();
    }).then(function(resp) {
      if (!resp) return;
      try {
        var entries = resp.entries || [];
        var table = document.getElementById('alertsTable');
        var container = table ? table.closest('.lcars-panel-body') : document.querySelector('#sec-alerts .lcars-panel-body');
        if (!container) return;

        if (entries.length === 0) {
          container.innerHTML = '<div class="empty-state">No transmissions recorded</div>';
          return;
        }

        var html = '<div class="table-filter"><input type="text" id="filterAlerts" placeholder="Filter alerts..." oninput="filterTable(\'alertsTable\', this.value, \'filterAlertsCount\')"><span class="table-filter-count" id="filterAlertsCount"></span></div>';
        html += '<table id="alertsTable"><thead><tr>';
        html += '<th onclick="sortTable(0,\'date\',this.closest(\'table\'))">Stardate <span class="sort-arrow"></span></th>';
        html += '<th onclick="sortTable(1,\'string\',this.closest(\'table\'))">Status <span class="sort-arrow"></span></th>';
        html += '<th onclick="sortTable(2,\'string\',this.closest(\'table\'))">Mode <span class="sort-arrow"></span></th>';
        html += '<th onclick="sortTable(3,\'string\',this.closest(\'table\'))">Action <span class="sort-arrow"></span></th>';
        html += '<th onclick="sortTable(4,\'string\',this.closest(\'table\'))">Host <span class="sort-arrow"></span></th>';
        html += '<th onclick="sortTable(5,\'string\',this.closest(\'table\'))">Service <span class="sort-arrow"></span></th>';
        html += '<th onclick="sortTable(6,\'string\',this.closest(\'table\'))">Source <span class="sort-arrow"></span></th>';
        html += '<th>Icinga</th>';
        html += '<th onclick="sortTable(8,\'number\',this.closest(\'table\'))">Duration <span class="sort-arrow"></span></th>';
        html += '</tr></thead><tbody>';

        for (var i = 0; i < entries.length; i++) {
          var e = entries[i];
          var statusLabel = 'OK', statusClass = 'ok';
          switch (e.exit_status) {
            case 1: statusLabel = 'WARNING'; statusClass = 'warning'; break;
            case 2: statusLabel = 'CRITICAL'; statusClass = 'critical'; break;
          }
          if (e.mode === 'test') { statusLabel = 'TEST'; statusClass = 'test'; }
          if (e.mode === 'manual') { statusLabel += ' [MANUAL]'; statusClass += ' manual'; }
          if (!e.icinga_ok || e.error) { statusClass = 'error'; }
          var ts = e.timestamp ? new Date(e.timestamp).toISOString().replace('T', ' ').replace(/\.\d+Z$/, ' UTC') : '';
          var mode = escHtml(e.mode || '');
          var manualTag = (e.mode === 'manual') ? ' <span class="alerts-manual-tag">ADMIN</span>' : '';
          html += '<tr data-service="' + escHtml(e.service_name || '') + '" data-host="' + escHtml(e.host_name || '') + '">';
          html += '<td class="mono">' + ts + '</td>';
          html += '<td><span class="badge ' + statusClass + '">' + escHtml(statusLabel) + '</span></td>';
          html += '<td>' + mode + manualTag + '</td>';
          html += '<td>' + escHtml(e.action || '') + '</td>';
          html += '<td class="mono">' + (e.host_name ? escHtml(e.host_name) : '-') + '</td>';
          html += '<td><strong class="svc-link js-service-history-trigger">' + escHtml(e.service_name || '') + '</strong></td>';
          html += '<td class="mono">' + escHtml(e.source_key || '') + '</td>';
          html += '<td>' + (e.icinga_ok ? '<span class="icinga-ok">OK</span>' : '<span class="icinga-fail">FAIL</span>') + '</td>';
          html += '<td class="duration">' + (e.duration_ms || 0) + 'ms</td>';
          html += '</tr>';
        }
        html += '</tbody></table>';
        container.innerHTML = html;
        applyFrozenHighlight();
      } catch(err) {}
    }).catch(function() {});
  }

  function scheduleAlertsRefresh() {
    if (_alertsRefreshTimer) clearTimeout(_alertsRefreshTimer);
    _alertsRefreshTimer = setTimeout(refreshAlertsTable, 2000);
  }

  // Apply frozen highlights to all tables on initial load
  setTimeout(applyFrozenHighlight, 600);

  if (typeof EventSource !== 'undefined') {
    var es = new EventSource('/status/beauty/events');
    es.onmessage = function(e) {
      try {
        var data = JSON.parse(e.data);
        spawnOrb(data.status || 'ok');
        incCounter('stat-total');
        if (data.mode === 'work') incCounter('stat-work');
        if (data.mode === 'test') incCounter('stat-test');
        if (data.status === 'critical') incCounter('stat-critical');
        if (data.status === 'warning') incCounter('stat-warning');
        scheduleAlertsRefresh();
      } catch(err) {}
    };
    es.onerror = function() {
      // Reconnect is automatic with EventSource
    };
  }
})();

// ── LCARS Info Popup System ──
var lcarsInfo = {
  total_since_start: "Cumulative count of all subspace relay transmissions received since last system initialization. This metric represents total signal traffic processed through the bridge relay array.",
  history_entries: "Number of sensor log entries currently stored in the ship's computer memory banks. Each entry documents a fully processed subspace relay event with complete telemetry data.",
  errors: "Anomalous signal events detected during relay processing. Elevated readings may indicate subspace interference, target system communication failures, or protocol misalignment requiring diagnostic review.",
  avg_duration: "Mean chronometric duration for full signal relay processing \u2014 from initial subspace reception through final acknowledgment from the Icinga tactical grid. Measured in standard milliseconds.",
  cached_services: "Number of Icinga tactical grid service endpoints currently held in the navigational deflector cache. Cached entries reduce subspace query overhead during alert correlation sequences.",
  firing: "Active threat signatures currently propagating through the alert relay network. These signals indicate unresolved conditions requiring tactical response from the Icinga defense grid.",
  resolved: "Threat signatures that have been neutralized and confirmed stable. These relay events indicate successful remediation \u2014 the alert condition has returned to nominal parameters.",
  test_mode: "Holodeck simulation signals \u2014 relay transmissions processed in diagnostic mode without propagation to the live Icinga tactical grid. Used for system calibration and crew training exercises.",
  critical_firing: "Red Alert conditions currently active across the sensor network. Critical-priority threat signatures demanding immediate bridge crew attention. These represent the highest severity level in the tactical classification matrix.",
  warning_firing: "Yellow Alert conditions currently active. Warning-priority anomalies detected by the sensor array that may escalate to Red Alert status if left unaddressed. Recommend monitoring at regular intervals.",
  signal_sources: "Subspace Relay Origin Registry \u2014 identifies all transmission sources currently feeding the bridge relay system. Displays source designation tags and point-of-origin coordinates for each incoming signal carrier.",
  recent_alerts: "Tactical Operations Log \u2014 chronological record of recent subspace relay events processed by the bridge system. Displays signal classification, source designation, target service endpoint, and processing timestamp.",
  recent_errors: "Anomaly Report \u2014 detailed log of processing failures and subspace relay disruptions. Each entry includes fault classification, diagnostic trace data, and chronometric stamp for engineering review.",
  system_diagnostics: "Engineering Systems Status Panel \u2014 real-time telemetry from the bridge relay computer core. Monitors processing thread allocation, memory bank utilization, waste reclamation cycles, computational core load, continuous uptime, and signal throughput rate.",
  cached_services_section: "Deflector Cache Manifest \u2014 complete inventory of Icinga tactical grid service endpoints currently stored in local memory banks. Enables rapid lookup and reduces subspace communication latency during high-volume alert processing.",
  service_management: "Starfleet Command Authorization Required \u2014 administrative interface for direct manipulation of Icinga tactical grid service registrations. Restricted to officers with command-level clearance.",
  webhook_flow_line: "Subspace Relay Flow Conduit \u2014 visual representation of signal propagation through the bridge relay system. Illuminated pathway orbs indicate active data transit from Grafana sensor arrays through processing cores to the Icinga tactical defense grid.",
  clear_history_button: "Initiate memory bank purge sequence \u2014 clears all stored sensor log entries from the ship's computer. This operation is irreversible. Recommend archival backup before executing purge authorization.",
  overview: "Bridge Operations Overview \u2014 primary command interface displaying consolidated telemetry from all relay subsystems. Provides commanding officer with immediate situational awareness of system-wide operational status.",
  alerts: "Tactical Alert Monitor \u2014 real-time feed of all subspace relay signals classified by threat level and resolution status. Standard bridge crew interface for monitoring active and recently resolved alert conditions.",
  services: "Service Registry Viewer \u2014 read-only manifest of all Icinga tactical grid endpoints tracked by the bridge relay system. Displays cached service configurations and operational status within the defense grid.",
  diagnostics: "Computer Core Diagnostics \u2014 Level 3 diagnostic readout of bridge relay system internals. Displays computational resource allocation, memory utilization curves, and processing efficiency metrics.",
  management: "Command Operations Console \u2014 Starfleet Command authorization required. Full administrative control over tactical grid service registrations. Access restricted to authorized personnel only.",
  settings_panel: "Starship Configuration Interface \u2014 modify all bridge subsystems including Icinga tactical grid credentials, subspace relay authentication tokens, and sensor array parameters. Changes are applied in real-time without requiring a full system restart. Use the Export function to create a backup of the current configuration matrix before making modifications.",
  dev_panel: "Subspace Relay Diagnostic Console \u2014 Level 1 engineering interface for monitoring all communications between the bridge relay system and the Icinga tactical defense grid. Displays raw transmission payloads, response telemetry, and chronometric data for each API interaction. Authorized engineering personnel only."
};

function showInfoPopup(key, text) {
  closeInfoPopup();
  var overlay = document.createElement('div');
  overlay.className = 'info-overlay';
  overlay.id = 'infoOverlay';
  overlay.addEventListener('click', closeInfoPopup);

  var popup = document.createElement('div');
  popup.className = 'info-popup';
  popup.id = 'infoPopup';

  var header = document.createElement('div');
  header.className = 'info-popup-header';
  var title = document.createElement('span');
  title.textContent = 'COMPUTER DATABASE \u2014 ' + key.toUpperCase().replace(/_/g, ' ');
  var closeBtn = document.createElement('button');
  closeBtn.className = 'info-popup-close';
  closeBtn.innerHTML = '\u00D7';
  closeBtn.addEventListener('click', closeInfoPopup);
  header.appendChild(title);
  header.appendChild(closeBtn);

  var body = document.createElement('div');
  body.className = 'info-popup-body';
  body.textContent = text;

  popup.appendChild(header);
  popup.appendChild(body);

  document.body.appendChild(overlay);
  document.body.appendChild(popup);
}

function closeInfoPopup() {
  var overlay = document.getElementById('infoOverlay');
  var popup = document.getElementById('infoPopup');
  if (overlay) overlay.remove();
  if (popup) popup.remove();
}

document.addEventListener('click', function(e) {
  if (e.target.classList.contains('info-trigger')) {
    e.stopPropagation();
    e.preventDefault();
    var key = e.target.getAttribute('data-info');
    var text = lcarsInfo[key] || 'No data available in computer database.';
    showInfoPopup(key, text);
  }
});

// ── Table Filter ──
function filterTable(tableId, query, countId) {
  var table = document.getElementById(tableId);
  if (!table) return;
  var rows = table.querySelectorAll('tbody tr');
  var q = query.toLowerCase();
  var visible = 0;
  var total = 0;
  for (var i = 0; i < rows.length; i++) {
    var row = rows[i];
    if (row.classList.contains('svc-host-divider')) {
      row.style.display = '';
      continue;
    }
    total++;
    var text = row.textContent.toLowerCase();
    if (!q || text.indexOf(q) !== -1) {
      row.style.display = '';
      visible++;
    } else {
      row.style.display = 'none';
    }
  }
  // hide host dividers with no visible rows after them
  if (tableId === 'svcRegistryTable') {
    var dividers = table.querySelectorAll('.svc-host-divider');
    for (var d = 0; d < dividers.length; d++) {
      var next = dividers[d].nextElementSibling;
      var hasVisible = false;
      while (next && !next.classList.contains('svc-host-divider')) {
        if (next.style.display !== 'none') hasVisible = true;
        next = next.nextElementSibling;
      }
      dividers[d].style.display = hasVisible ? '' : 'none';
    }
  }
  var countEl = document.getElementById(countId);
  if (countEl) {
    if (q) {
      countEl.textContent = visible + ' / ' + total + ' matching';
    } else {
      countEl.textContent = total + ' registered';
    }
  }
}

// ── Session Timeout (30 min, 3 min warning) ──
{{if .IsAdmin}}
(function() {
  var SESSION_MS = 30 * 60 * 1000;
  var WARN_MS = 3 * 60 * 1000;
  var sessionStart = Date.now();
  var warningShown = false;
  var countdownInterval = null;
  var popupEl = null;

  function resetSession() {
    sessionStart = Date.now();
    warningShown = false;
    if (popupEl) { popupEl.remove(); popupEl = null; }
    if (countdownInterval) { clearInterval(countdownInterval); countdownInterval = null; }
  }

  function doLogout() {
    if (countdownInterval) clearInterval(countdownInterval);
    if (popupEl) popupEl.remove();
    document.cookie = '_logged_out=1;path=/';
    window.location.href = '/status/beauty';
  }

  function showWarning() {
    if (warningShown) return;
    warningShown = true;
    var overlay = document.createElement('div');
    overlay.className = 'session-popup-overlay';
    overlay.id = 'sessionPopup';
    var remaining = SESSION_MS - (Date.now() - sessionStart);
    overlay.innerHTML = '<div class="session-popup">' +
      '<div class="session-popup-header"><span>Session Expiring</span><span style="font-size:11px;">SECURITY PROTOCOL</span></div>' +
      '<div class="session-popup-body">' +
      '<p>Your command access session will terminate in</p>' +
      '<div class="session-countdown" id="sessionCountdown"></div>' +
      '<p>All unsaved operations will be lost.</p>' +
      '<div class="session-popup-actions">' +
      '<button class="session-btn session-btn-extend" onclick="window._sessionExtend()">Extend Session</button>' +
      '<button class="session-btn session-btn-logout" onclick="window._sessionLogout()">Logout Now</button>' +
      '</div></div></div>';
    document.body.appendChild(overlay);
    popupEl = overlay;

    function updateCountdown() {
      var left = Math.max(0, SESSION_MS - (Date.now() - sessionStart));
      var min = Math.floor(left / 60000);
      var sec = Math.floor((left % 60000) / 1000);
      var el = document.getElementById('sessionCountdown');
      if (el) el.textContent = (min < 10 ? '0' : '') + min + ':' + (sec < 10 ? '0' : '') + sec;
      if (left <= 0) doLogout();
    }
    updateCountdown();
    countdownInterval = setInterval(updateCountdown, 1000);
  }

  window._sessionExtend = function() {
    resetSession();
  };
  window._sessionLogout = doLogout;

  setInterval(function() {
    var elapsed = Date.now() - sessionStart;
    if (elapsed >= SESSION_MS) {
      doLogout();
    } else if (elapsed >= SESSION_MS - WARN_MS && !warningShown) {
      showWarning();
    }
  }, 1000);

  // Reset on user activity
  ['click', 'keydown', 'scroll'].forEach(function(evt) {
    document.addEventListener(evt, function() {
      if (!warningShown) sessionStart = Date.now();
    });
  });
})();
{{end}}

document.addEventListener('click', function(e) {
  // Source detail toggle
  const sourceToggle = e.target.closest('.js-source-toggle');
  if (sourceToggle) {
    toggleSourceDetail(sourceToggle.dataset.source);
    return;
  }

  // IP tabs
  const ipTab = e.target.closest('.js-ip-tab');
  if (ipTab) {
    switchIPTab(ipTab.dataset.source, ipTab.dataset.tab, ipTab);
    return;
  }

  // Service history (Registry table or Icinga table strong tag)
  const historyTrigger = e.target.closest('.js-service-history-trigger');
  if (historyTrigger) {
    e.stopPropagation();
    const row = historyTrigger.closest('tr');
    if (row) {
      showServiceHistory(row.dataset.service, row.dataset.host);
    }
    return;
  }

  const historyRow = e.target.closest('.js-service-history');
  if (historyRow) {
    showServiceHistory(historyRow.dataset.service, historyRow.dataset.host);
    return;
  }
});
</script>

</body>
</html>`

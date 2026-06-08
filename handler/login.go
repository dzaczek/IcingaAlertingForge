package handler

import (
	"html/template"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"icinga-webhook-bridge/auth"
	"icinga-webhook-bridge/metrics"
	"icinga-webhook-bridge/rbac"
)

// LoginHandler serves the form-based login page and processes credential
// submissions, issuing an opaque session cookie on success. It replaces the
// native browser Basic Auth prompt so password managers can autofill.
type LoginHandler struct {
	Sessions   *auth.SessionStore
	AdminUser  string
	AdminPass  string
	RBAC       *rbac.Manager
	Metrics    *metrics.Collector
	Version    string
	SessionTTL time.Duration
}

// defaultLoginRedirect is where users land after a successful login.
const defaultLoginRedirect = "/status/beauty?admin=1"

func (h *LoginHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.render(w, http.StatusOK, safeNext(r.URL.Query().Get("next")), "")
	case http.MethodPost:
		h.handlePost(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *LoginHandler) handlePost(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10) // 4 KiB is plenty for a login form
	if err := r.ParseForm(); err != nil {
		h.render(w, http.StatusBadRequest, defaultLoginRedirect, "Invalid form submission.")
		return
	}

	username := r.PostFormValue("username")
	password := r.PostFormValue("password")
	next := safeNext(r.PostFormValue("next"))

	if !h.valid(username, password) {
		slog.Warn("Login failed", "user", username, "remote_addr", r.RemoteAddr)
		if h.Metrics != nil && username != "" {
			h.Metrics.RecordAuthFailure(r.RemoteAddr, username)
		}
		h.render(w, http.StatusUnauthorized, next, "Invalid credentials.")
		return
	}

	token, err := h.Sessions.Create(username, password)
	if err != nil {
		slog.Error("Login: failed to create session", "error", err)
		h.render(w, http.StatusInternalServerError, next, "Could not start session, please retry.")
		return
	}

	// Secure is conditional because the bridge legitimately runs over plain HTTP
	// behind a TLS-terminating proxy; HttpOnly + SameSite are always set.
	// #nosec G124
	http.SetCookie(w, &http.Cookie{
		Name:     auth.SessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(h.SessionTTL.Seconds()),
	})
	slog.Info("Login successful", "user", username, "remote_addr", r.RemoteAddr)
	http.Redirect(w, r, next, http.StatusSeeOther)
}

// HandleLogout destroys the current session, clears the cookie, and sends the
// user back to the login form.
func (h *LoginHandler) HandleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(auth.SessionCookieName); err == nil {
		h.Sessions.Delete(c.Value)
	}
	// #nosec G124 -- see note in handlePost; clearing cookie, Secure is conditional.
	http.SetCookie(w, &http.Cookie{
		Name:     auth.SessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// valid reports whether the credentials match the primary admin or an RBAC user.
// Mirrors the Basic Auth check used elsewhere so authorization stays identical.
func (h *LoginHandler) valid(username, password string) bool {
	if h.AdminPass == "" {
		return false
	}
	if auth.SecureCompare(username, h.AdminUser) && auth.SecureCompare(password, h.AdminPass) {
		return true
	}
	if h.RBAC != nil {
		if _, ok := h.RBAC.Authenticate(username, password); ok {
			return true
		}
	}
	return false
}

func (h *LoginHandler) render(w http.ResponseWriter, status int, next, errMsg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	data := loginPageData{Next: next, Error: errMsg, Version: h.Version}
	if err := loginTemplate.Execute(w, data); err != nil {
		slog.Error("Login: template render failed", "error", err)
	}
}

// safeNext returns next only if it is a safe relative path, preventing open
// redirects. Anything else falls back to the dashboard.
func safeNext(next string) string {
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		return defaultLoginRedirect
	}
	return next
}

// isHTTPS reports whether the request arrived over TLS, directly or via a proxy.
func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

type loginPageData struct {
	Next    string
	Error   string
	Version string
}

var loginTemplate = template.Must(template.New("login").Parse(loginHTML))

const loginHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>IcingaAlertForge — Login</title>
<style>
  :root { --amber:#ff9900; --amber-dim:#cc7a00; --bg:#000; --panel:#1a1a2e; --text:#ffcc99; --err:#cc6666; }
  * { box-sizing: border-box; }
  body { margin:0; min-height:100vh; display:flex; align-items:center; justify-content:center;
         background:var(--bg); color:var(--text); font-family:"Helvetica Neue",Arial,sans-serif; }
  .card { width:360px; max-width:92vw; background:var(--panel); border:2px solid var(--amber);
          border-radius:18px; padding:32px 28px; box-shadow:0 0 24px rgba(255,153,0,.25); }
  .title { color:var(--amber); font-size:1.4rem; font-weight:700; letter-spacing:2px;
           text-transform:uppercase; margin:0 0 4px; text-align:center; }
  .sub { text-align:center; font-size:.8rem; opacity:.7; margin:0 0 24px; }
  label { display:block; font-size:.75rem; text-transform:uppercase; letter-spacing:1px;
          margin:14px 0 6px; color:var(--amber); }
  input { width:100%; padding:11px 12px; background:#000; color:var(--text);
          border:1px solid var(--amber-dim); border-radius:8px; font-size:1rem; }
  input:focus { outline:none; border-color:var(--amber); box-shadow:0 0 8px rgba(255,153,0,.4); }
  button { width:100%; margin-top:22px; padding:12px; background:var(--amber); color:#000;
           border:none; border-radius:20px; font-size:1rem; font-weight:700; letter-spacing:1px;
           text-transform:uppercase; cursor:pointer; }
  button:hover { background:#ffb340; }
  .error { margin:14px 0 0; padding:10px; background:rgba(204,102,102,.15);
           border:1px solid var(--err); border-radius:8px; color:var(--err); font-size:.85rem; text-align:center; }
  .ver { text-align:center; margin-top:18px; font-size:.7rem; opacity:.5; }
</style>
</head>
<body>
  <form class="card" method="post" action="/login" autocomplete="on">
    <h1 class="title">IcingaAlertForge</h1>
    <p class="sub">Command Panel Authentication</p>
    <input type="hidden" name="next" value="{{.Next}}">
    <label for="username">Username</label>
    <input id="username" name="username" type="text" autocomplete="username" autofocus required>
    <label for="password">Password</label>
    <input id="password" name="password" type="password" autocomplete="current-password" required>
    {{if .Error}}<div class="error">{{.Error}}</div>{{end}}
    <button type="submit">Sign In</button>
    {{if .Version}}<div class="ver">{{.Version}}</div>{{end}}
  </form>
</body>
</html>`

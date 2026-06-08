package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"icinga-webhook-bridge/auth"
)

// captureAuth returns a handler that records the username/password it sees via
// r.BasicAuth(), plus the next handler.
func captureAuth(seen *struct {
	user, pass string
	ok         bool
}) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.user, seen.pass, seen.ok = r.BasicAuth()
	})
}

func TestSessionMiddleware_InjectsBasicAuthFromSession(t *testing.T) {
	store := auth.NewSessionStore(time.Minute)
	token, _ := store.Create("admin", "secret")

	var seen struct {
		user, pass string
		ok         bool
	}
	mw := SessionAuthMiddleware(store, captureAuth(&seen))

	req := httptest.NewRequest(http.MethodGet, "/status/beauty?admin=1", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: token})
	mw.ServeHTTP(httptest.NewRecorder(), req)

	if !seen.ok || seen.user != "admin" || seen.pass != "secret" {
		t.Fatalf("expected injected Basic Auth admin/secret, got user=%q pass=%q ok=%v", seen.user, seen.pass, seen.ok)
	}
}

func TestSessionMiddleware_NoCookieNoInjection(t *testing.T) {
	store := auth.NewSessionStore(time.Minute)
	var seen struct {
		user, pass string
		ok         bool
	}
	mw := SessionAuthMiddleware(store, captureAuth(&seen))

	mw.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/status/beauty?admin=1", nil))
	if seen.ok {
		t.Fatal("expected no Basic Auth without a session cookie")
	}
}

func TestSessionMiddleware_DoesNotOverrideExistingAuth(t *testing.T) {
	store := auth.NewSessionStore(time.Minute)
	token, _ := store.Create("admin", "secret")
	var seen struct {
		user, pass string
		ok         bool
	}
	mw := SessionAuthMiddleware(store, captureAuth(&seen))

	req := httptest.NewRequest(http.MethodGet, "/admin/services", nil)
	req.SetBasicAuth("apiuser", "apipass") // caller's own credentials
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: token})
	mw.ServeHTTP(httptest.NewRecorder(), req)

	if seen.user != "apiuser" || seen.pass != "apipass" {
		t.Fatalf("existing Authorization must be preserved, got %q/%q", seen.user, seen.pass)
	}
}

func TestSessionMiddleware_SkipsWebhookPath(t *testing.T) {
	store := auth.NewSessionStore(time.Minute)
	token, _ := store.Create("admin", "secret")
	var seen struct {
		user, pass string
		ok         bool
	}
	mw := SessionAuthMiddleware(store, captureAuth(&seen))

	req := httptest.NewRequest(http.MethodPost, "/webhook", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: token})
	mw.ServeHTTP(httptest.NewRecorder(), req)

	if seen.ok {
		t.Fatal("webhook path must not receive injected session credentials")
	}
}

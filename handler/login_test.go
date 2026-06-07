package handler

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"icinga-webhook-bridge/auth"
)

func testLoginHandler() *LoginHandler {
	return &LoginHandler{
		Sessions:   auth.NewSessionStore(30 * time.Minute),
		AdminUser:  "admin",
		AdminPass:  "secret",
		SessionTTL: 30 * time.Minute,
		Version:    "test",
	}
}

func TestLogin_GET_RendersFormWithAutocomplete(t *testing.T) {
	h := testLoginHandler()
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/login", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	body := rr.Body.String()
	// Password-manager friendliness depends on these autocomplete hints + a real form.
	for _, want := range []string{`autocomplete="username"`, `autocomplete="current-password"`, `method="post"`} {
		if !strings.Contains(body, want) {
			t.Errorf("login form missing %q", want)
		}
	}
}

func TestLogin_POST_ValidSetsSessionCookieAndRedirects(t *testing.T) {
	h := testLoginHandler()

	form := url.Values{"username": {"admin"}, "password": {"secret"}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect, got %d", rr.Code)
	}
	if loc := rr.Header().Get("Location"); loc != defaultLoginRedirect {
		t.Errorf("expected redirect to %q, got %q", defaultLoginRedirect, loc)
	}

	cookies := rr.Result().Cookies()
	var token string
	for _, c := range cookies {
		if c.Name == auth.SessionCookieName {
			token = c.Value
			if !c.HttpOnly {
				t.Error("session cookie should be HttpOnly")
			}
		}
	}
	if token == "" {
		t.Fatal("expected a session cookie to be set")
	}
	if _, ok := h.Sessions.Get(token); !ok {
		t.Error("expected session to exist in store")
	}
}

func TestLogin_POST_InvalidRejected(t *testing.T) {
	h := testLoginHandler()

	form := url.Values{"username": {"admin"}, "password": {"wrong"}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
	for _, c := range rr.Result().Cookies() {
		if c.Name == auth.SessionCookieName && c.Value != "" {
			t.Error("no session cookie should be set on failed login")
		}
	}
	if h.Sessions.Len() != 0 {
		t.Error("no session should be created on failed login")
	}
}

func TestSafeNext_BlocksOpenRedirect(t *testing.T) {
	cases := map[string]string{
		"":                 defaultLoginRedirect,
		"//evil.com":       defaultLoginRedirect,
		"https://evil.com": defaultLoginRedirect,
		"/history":         "/history",
		"/status/beauty":   "/status/beauty",
	}
	for in, want := range cases {
		if got := safeNext(in); got != want {
			t.Errorf("safeNext(%q) = %q, want %q", in, got, want)
		}
	}
}

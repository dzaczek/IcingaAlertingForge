package handler

import (
	"net/http"
	"strings"

	"icinga-webhook-bridge/auth"
)

// SessionAuthMiddleware bridges cookie sessions to the existing Basic-Auth-based
// handlers: when a request carries a valid session cookie and no Authorization
// header, it reconstructs an HTTP Basic Auth header from the session credentials.
// This lets every existing handler keep reading r.BasicAuth() unchanged while
// users authenticate via the login form. Webhook ingestion is skipped because it
// uses its own API-key scheme.
func SessionAuthMiddleware(store *auth.SessionStore, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if store != nil && r.Header.Get("Authorization") == "" && !strings.HasPrefix(r.URL.Path, "/webhook") {
			if c, err := r.Cookie(auth.SessionCookieName); err == nil {
				if sess, ok := store.Get(c.Value); ok {
					r.SetBasicAuth(sess.Username, sess.Password)
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

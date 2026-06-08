package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// SessionCookieName is the cookie that carries the opaque session token.
const SessionCookieName = "iaf_session"

// Session holds the credentials of a logged-in dashboard user. The plaintext
// password is retained so the session middleware can reconstruct an HTTP Basic
// Auth header, allowing all existing Basic-Auth-based handlers to keep working
// unchanged. This is consistent with the rest of the app, which already holds
// admin/RBAC passwords in plaintext in memory and the config store.
type Session struct {
	Username string
	Password string
	Expiry   time.Time
}

// SessionStore is an in-memory store of active sessions keyed by an opaque,
// cryptographically random token. Safe for concurrent use.
type SessionStore struct {
	mu       sync.Mutex
	sessions map[string]Session
	ttl      time.Duration
}

// NewSessionStore creates a store whose sessions expire after ttl of inactivity
// from creation.
func NewSessionStore(ttl time.Duration) *SessionStore {
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}
	return &SessionStore{
		sessions: make(map[string]Session),
		ttl:      ttl,
	}
}

// Create stores a new session for the given credentials and returns its token.
func (s *SessionStore) Create(username, password string) (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	token := hex.EncodeToString(buf)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[token] = Session{
		Username: username,
		Password: password,
		Expiry:   time.Now().Add(s.ttl),
	}
	return token, nil
}

// Get returns the session for a token if it exists and has not expired.
func (s *SessionStore) Get(token string) (Session, bool) {
	if token == "" {
		return Session{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, ok := s.sessions[token]
	if !ok {
		return Session{}, false
	}
	if time.Now().After(sess.Expiry) {
		delete(s.sessions, token)
		return Session{}, false
	}
	return sess, true
}

// Delete removes a session (logout).
func (s *SessionStore) Delete(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, token)
}

// StartEviction periodically removes expired sessions so memory does not grow
// without bound.
func (s *SessionStore) StartEviction(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Minute
	}
	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.evictExpired()
			}
		}
	}()
}

func (s *SessionStore) evictExpired() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for token, sess := range s.sessions {
		if now.After(sess.Expiry) {
			delete(s.sessions, token)
		}
	}
}

// Len returns the number of active sessions (primarily for tests).
func (s *SessionStore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sessions)
}

package auth

import (
	"testing"
	"time"
)

func TestSessionStore_CreateAndGet(t *testing.T) {
	s := NewSessionStore(time.Minute)
	token, err := s.Create("admin", "secret")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if len(token) != 64 {
		t.Fatalf("expected 64-char hex token, got %d chars", len(token))
	}

	sess, ok := s.Get(token)
	if !ok {
		t.Fatal("expected session to be retrievable")
	}
	if sess.Username != "admin" || sess.Password != "secret" {
		t.Fatalf("unexpected session contents: %+v", sess)
	}
}

func TestSessionStore_TokensAreUnique(t *testing.T) {
	s := NewSessionStore(time.Minute)
	t1, _ := s.Create("a", "p")
	t2, _ := s.Create("a", "p")
	if t1 == t2 {
		t.Fatal("expected distinct tokens for separate sessions")
	}
}

func TestSessionStore_GetUnknownOrEmpty(t *testing.T) {
	s := NewSessionStore(time.Minute)
	if _, ok := s.Get(""); ok {
		t.Error("empty token must not resolve")
	}
	if _, ok := s.Get("deadbeef"); ok {
		t.Error("unknown token must not resolve")
	}
}

func TestSessionStore_Expiry(t *testing.T) {
	s := NewSessionStore(5 * time.Millisecond)
	token, _ := s.Create("admin", "secret")
	if _, ok := s.Get(token); !ok {
		t.Fatal("session should be valid immediately after creation")
	}
	time.Sleep(10 * time.Millisecond)
	if _, ok := s.Get(token); ok {
		t.Fatal("expired session must not resolve")
	}
	if s.Len() != 0 {
		t.Fatal("expired session should be removed on Get")
	}
}

func TestSessionStore_Delete(t *testing.T) {
	s := NewSessionStore(time.Minute)
	token, _ := s.Create("admin", "secret")
	s.Delete(token)
	if _, ok := s.Get(token); ok {
		t.Fatal("deleted session must not resolve")
	}
}

func TestSessionStore_EvictExpired(t *testing.T) {
	s := NewSessionStore(5 * time.Millisecond)
	s.Create("a", "p")
	s.Create("b", "p")
	if s.Len() != 2 {
		t.Fatalf("expected 2 sessions, got %d", s.Len())
	}
	time.Sleep(10 * time.Millisecond)
	s.evictExpired()
	if s.Len() != 0 {
		t.Fatalf("expected all sessions evicted, got %d", s.Len())
	}
}

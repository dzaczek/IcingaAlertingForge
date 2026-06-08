package ratelimit

import (
	"context"
	"testing"
	"time"
)

func TestNew_DisabledWhenNonPositive(t *testing.T) {
	if New(0, 5) != nil {
		t.Error("expected nil limiter for rate <= 0")
	}
	if New(5, 0) != nil {
		t.Error("expected nil limiter for burst <= 0")
	}
}

func TestNilLimiter_AlwaysAllows(t *testing.T) {
	var l *Limiter
	for i := 0; i < 100; i++ {
		if ok, _ := l.Allow("any"); !ok {
			t.Fatal("nil limiter must always allow")
		}
	}
}

func TestAllow_BurstThenDeny(t *testing.T) {
	// 1 token/sec, burst of 3: first 3 requests pass instantly, 4th is denied.
	l := New(1, 3)
	for i := 0; i < 3; i++ {
		if ok, _ := l.Allow("ip-a"); !ok {
			t.Fatalf("request %d within burst should be allowed", i+1)
		}
	}
	ok, retryAfter := l.Allow("ip-a")
	if ok {
		t.Fatal("4th request should be denied after burst is exhausted")
	}
	if retryAfter <= 0 || retryAfter > time.Second {
		t.Fatalf("retryAfter should be ~1s, got %v", retryAfter)
	}
}

func TestAllow_KeysAreIndependent(t *testing.T) {
	l := New(1, 1)
	if ok, _ := l.Allow("ip-a"); !ok {
		t.Fatal("first request for ip-a should pass")
	}
	if ok, _ := l.Allow("ip-b"); !ok {
		t.Fatal("first request for ip-b should pass (independent bucket)")
	}
	if ok, _ := l.Allow("ip-a"); ok {
		t.Fatal("second request for ip-a should be denied")
	}
}

func TestAllow_RefillsOverTime(t *testing.T) {
	// 100 tokens/sec, burst 1: after exhausting, a token refills within ~10ms.
	l := New(100, 1)
	if ok, _ := l.Allow("ip-a"); !ok {
		t.Fatal("first request should pass")
	}
	if ok, _ := l.Allow("ip-a"); ok {
		t.Fatal("immediate second request should be denied")
	}
	time.Sleep(20 * time.Millisecond)
	if ok, _ := l.Allow("ip-a"); !ok {
		t.Fatal("request after refill window should be allowed")
	}
}

func TestEvictIdle_RemovesStaleBuckets(t *testing.T) {
	l := New(1, 1)
	l.ttl = 5 * time.Millisecond
	l.Allow("ip-a")
	if l.Len() != 1 {
		t.Fatalf("expected 1 bucket, got %d", l.Len())
	}
	time.Sleep(10 * time.Millisecond)
	l.evictIdle()
	if l.Len() != 0 {
		t.Fatalf("expected idle bucket to be evicted, got %d", l.Len())
	}
}

func TestStartEviction_StopsOnContextCancel(t *testing.T) {
	l := New(1, 1)
	l.ttl = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	l.StartEviction(ctx, 2*time.Millisecond)
	l.Allow("ip-a")
	// Give the eviction goroutine time to run and clear the idle bucket.
	deadline := time.After(time.Second)
	for l.Len() != 0 {
		select {
		case <-deadline:
			t.Fatal("eviction goroutine did not clear idle bucket")
		case <-time.After(2 * time.Millisecond):
		}
	}
	cancel()
}

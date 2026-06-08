// Package ratelimit provides a token-bucket rate limiter keyed by an arbitrary
// string (typically a client IP or API key). It is used to throttle inbound
// webhook traffic so a single noisy or malicious source cannot flood the bridge.
package ratelimit

import (
	"context"
	"math"
	"sync"
	"time"
)

// Limiter is a token-bucket rate limiter. Each key gets its own bucket that
// refills at `rate` tokens per second up to `burst` tokens. Safe for concurrent
// use.
type Limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	rate    float64       // tokens added per second
	burst   float64       // bucket capacity
	ttl     time.Duration // idle buckets older than ttl are evicted
}

type bucket struct {
	tokens   float64
	lastSeen time.Time
}

// New creates a Limiter allowing `ratePerSec` requests per second per key with a
// maximum burst of `burst` requests. Returns nil if ratePerSec <= 0 or burst <=
// 0, which callers treat as "rate limiting disabled".
func New(ratePerSec float64, burst int) *Limiter {
	if ratePerSec <= 0 || burst <= 0 {
		return nil
	}
	return &Limiter{
		buckets: make(map[string]*bucket),
		rate:    ratePerSec,
		burst:   float64(burst),
		ttl:     10 * time.Minute,
	}
}

// Allow reports whether a request for the given key may proceed. When denied it
// also returns how long the caller should wait before the next token is
// available (suitable for a Retry-After header). A nil Limiter always allows.
func (l *Limiter) Allow(key string) (allowed bool, retryAfter time.Duration) {
	if l == nil {
		return true, 0
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: l.burst, lastSeen: now}
		l.buckets[key] = b
	}

	// Refill proportionally to elapsed time since the bucket was last touched.
	elapsed := now.Sub(b.lastSeen).Seconds()
	b.tokens = math.Min(l.burst, b.tokens+elapsed*l.rate)
	b.lastSeen = now

	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}

	// Time until the bucket accrues one full token.
	wait := time.Duration((1 - b.tokens) / l.rate * float64(time.Second))
	return false, wait
}

// StartEviction periodically removes idle buckets so memory does not grow
// without bound under churning client IPs/keys.
func (l *Limiter) StartEviction(ctx context.Context, interval time.Duration) {
	if l == nil {
		return
	}
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
				l.evictIdle()
			}
		}
	}()
}

func (l *Limiter) evictIdle() {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	for key, b := range l.buckets {
		if now.Sub(b.lastSeen) > l.ttl {
			delete(l.buckets, key)
		}
	}
}

// Len returns the number of tracked buckets (primarily for tests/metrics).
func (l *Limiter) Len() int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}

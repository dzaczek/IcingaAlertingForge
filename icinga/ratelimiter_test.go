package icinga

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRateLimiter_MutateLimit(t *testing.T) {
	rl := NewRateLimiter(2, 10, 50)
	ctx := context.Background()

	// Acquire 2 slots (max)
	if err := rl.AcquireMutate(ctx); err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	if err := rl.AcquireMutate(ctx); err != nil {
		t.Fatalf("second acquire failed: %v", err)
	}

	// Third should block — use a short timeout
	ctx2, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	err := rl.AcquireMutate(ctx2)
	if err == nil {
		t.Error("expected timeout error, got nil")
	}

	// Release one, next should succeed
	rl.ReleaseMutate()
	if err := rl.AcquireMutate(ctx); err != nil {
		t.Fatalf("acquire after release failed: %v", err)
	}

	rl.ReleaseMutate()
	rl.ReleaseMutate()
}

func TestRateLimiter_StatusQueueFull(t *testing.T) {
	rl := NewRateLimiter(5, 2, 3)
	ctx := context.Background()

	// Fill status slots (2) and queue (1 more = 3 total)
	for i := 0; i < 2; i++ {
		ok, err := rl.AcquireStatus(ctx)
		if err != nil || !ok {
			t.Fatalf("acquire %d failed: ok=%v err=%v", i, ok, err)
		}
	}

	// Queue one more (blocked on semaphore but counted in queue)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ok, _ := rl.AcquireStatus(ctx)
		if ok {
			rl.ReleaseStatus()
		}
	}()

	// Give goroutine time to queue
	time.Sleep(20 * time.Millisecond)

	// Release slots
	rl.ReleaseStatus()
	rl.ReleaseStatus()
	wg.Wait()
}

func TestRateLimiter_ConcurrentMutate(t *testing.T) {
	rl := NewRateLimiter(3, 10, 50)
	var maxConcurrent atomic.Int32
	var current atomic.Int32

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx := context.Background()
			if err := rl.AcquireMutate(ctx); err != nil {
				return
			}
			c := current.Add(1)
			for {
				m := maxConcurrent.Load()
				if c <= m || maxConcurrent.CompareAndSwap(m, c) {
					break
				}
			}
			time.Sleep(5 * time.Millisecond)
			current.Add(-1)
			rl.ReleaseMutate()
		}()
	}
	wg.Wait()

	if max := maxConcurrent.Load(); max > 3 {
		t.Errorf("max concurrent was %d, expected <= 3", max)
	}
}

func TestRateLimiter_Stats(t *testing.T) {
	rl := NewRateLimiter(5, 20, 100)
	mInUse, mMax, sInUse, sMax, queued, maxQ := rl.Stats()

	if mMax != 5 || sMax != 20 || maxQ != 100 {
		t.Errorf("unexpected max values: mutate=%d status=%d queue=%d", mMax, sMax, maxQ)
	}
	if mInUse != 0 || sInUse != 0 || queued != 0 {
		t.Errorf("expected all zero in-use, got: mutate=%d status=%d queued=%d", mInUse, sInUse, queued)
	}
}

package icinga

import (
	"context"
	"sync"
)

// RateLimiter controls concurrent access to the Icinga2 API.
// It separates mutation operations (create/delete) from status updates
// to prevent overwhelming the Icinga2 API under load.
type RateLimiter struct {
	mutateSem chan struct{} // limits concurrent create/delete operations
	statusSem chan struct{} // limits concurrent status update operations
	mu        sync.Mutex
	queued    int // number of queued status operations
	maxQueue  int
}

// NewRateLimiter creates a rate limiter with the given concurrency limits.
// maxMutate: max concurrent create/delete operations (e.g. 3-5)
// maxStatus: max concurrent status update operations (e.g. 20)
// maxQueue: max queued status operations before rejecting (e.g. 100)
func NewRateLimiter(maxMutate, maxStatus, maxQueue int) *RateLimiter {
	return &RateLimiter{
		mutateSem: make(chan struct{}, maxMutate),
		statusSem: make(chan struct{}, maxStatus),
		maxQueue:  maxQueue,
	}
}

// AcquireMutate blocks until a mutation slot is available.
// Returns an error if the context is cancelled.
func (rl *RateLimiter) AcquireMutate(ctx context.Context) error {
	select {
	case rl.mutateSem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ReleaseMutate releases a mutation slot.
func (rl *RateLimiter) ReleaseMutate() {
	<-rl.mutateSem
}

// AcquireStatus tries to acquire a status update slot.
// Returns false if the queue is full (rejects the request).
func (rl *RateLimiter) AcquireStatus(ctx context.Context) (bool, error) {
	rl.mu.Lock()
	if rl.queued >= rl.maxQueue {
		rl.mu.Unlock()
		return false, nil
	}
	rl.queued++
	rl.mu.Unlock()

	select {
	case rl.statusSem <- struct{}{}:
		return true, nil
	case <-ctx.Done():
		rl.mu.Lock()
		rl.queued--
		rl.mu.Unlock()
		return false, ctx.Err()
	}
}

// ReleaseStatus releases a status update slot.
func (rl *RateLimiter) ReleaseStatus() {
	<-rl.statusSem
	rl.mu.Lock()
	rl.queued--
	rl.mu.Unlock()
}

// Stats returns current rate limiter statistics.
func (rl *RateLimiter) Stats() (mutateInUse, mutateMax, statusInUse, statusMax, queued, maxQueue int) {
	return len(rl.mutateSem), cap(rl.mutateSem),
		len(rl.statusSem), cap(rl.statusSem),
		rl.queued, rl.maxQueue
}

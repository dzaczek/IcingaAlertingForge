package cache

import (
	"sync"
	"time"
)

// ServiceState represents the current state of a cached service.
type ServiceState string

const (
	StateNotFound      ServiceState = "not_found"
	StatePending       ServiceState = "pending"
	StateReady         ServiceState = "ready"
	StatePendingDelete ServiceState = "pending_delete"
)

type serviceEntry struct {
	State     ServiceState
	CreatedAt time.Time
}

// ServiceCache provides a thread-safe, TTL-aware cache for tracking Icinga2 service states.
type ServiceCache struct {
	mu      sync.RWMutex
	entries map[string]serviceEntry
	ttl     time.Duration
}

// NewServiceCache creates a new ServiceCache with the given TTL in minutes.
func NewServiceCache(ttlMinutes int) *ServiceCache {
	return &ServiceCache{
		entries: make(map[string]serviceEntry),
		ttl:     time.Duration(ttlMinutes) * time.Minute,
	}
}

// GetState returns the current state of the named service.
// Expired entries are treated as not found.
func (c *ServiceCache) GetState(name string) ServiceState {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, ok := c.entries[name]
	if !ok {
		return StateNotFound
	}
	if time.Since(entry.CreatedAt) > c.ttl {
		return StateNotFound
	}
	return entry.State
}

// Exists returns true if the service is cached and not expired.
func (c *ServiceCache) Exists(name string) bool {
	return c.GetState(name) != StateNotFound
}

// Register marks the service as ready in the cache.
func (c *ServiceCache) Register(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[name] = serviceEntry{State: StateReady, CreatedAt: time.Now()}
}

// SetPending marks the service as pending (deploy in progress).
func (c *ServiceCache) SetPending(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[name] = serviceEntry{State: StatePending, CreatedAt: time.Now()}
}

// SetPendingDelete marks the service as pending deletion.
func (c *ServiceCache) SetPendingDelete(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[name] = serviceEntry{State: StatePendingDelete, CreatedAt: time.Now()}
}

// Remove deletes the service from the cache.
func (c *ServiceCache) Remove(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, name)
}

// All returns a snapshot of all non-expired cached service names and their states.
func (c *ServiceCache) All() map[string]ServiceState {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make(map[string]ServiceState)
	now := time.Now()
	for name, entry := range c.entries {
		if now.Sub(entry.CreatedAt) <= c.ttl {
			result[name] = entry.State
		}
	}
	return result
}

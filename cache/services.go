package cache

import (
	"context"
	"sort"
	"strings"
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

// CacheEntry is a structured view of one cached host/service pair.
type CacheEntry struct {
	Key     string
	Host    string
	Service string
	State   ServiceState
}

// ServiceCache provides a thread-safe, TTL-aware cache for tracking Icinga2 service states.
type ServiceCache struct {
	mu      sync.RWMutex
	entries map[string]serviceEntry
	ttl     time.Duration
}

const serviceKeySeparator = "\x1f"

// NewServiceCache creates a new ServiceCache with the given TTL in minutes.
func NewServiceCache(ttlMinutes int) *ServiceCache {
	return &ServiceCache{
		entries: make(map[string]serviceEntry),
		ttl:     time.Duration(ttlMinutes) * time.Minute,
	}
}

// StartMaintenance periodically evicts expired entries so the cache does not
// grow without bounds during long-running deployments.
func (c *ServiceCache) StartMaintenance(ctx context.Context, interval time.Duration) {
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
				c.EvictExpired()
			}
		}
	}()
}

// ServiceKey returns a stable cache key for a host/service pair.
func ServiceKey(host, service string) string {
	return host + serviceKeySeparator + service
}

// SplitServiceKey returns the host and service parts for a cache key.
func SplitServiceKey(key string) (host, service string) {
	host, service, ok := strings.Cut(key, serviceKeySeparator)
	if !ok {
		return "", key
	}
	return host, service
}

// GetState returns the current state of the named service.
// Expired entries are treated as not found.
func (c *ServiceCache) GetState(host, name string) ServiceState {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, ok := c.entries[ServiceKey(host, name)]
	if !ok {
		return StateNotFound
	}
	if time.Since(entry.CreatedAt) > c.ttl {
		return StateNotFound
	}
	return entry.State
}

// Exists returns true if the service is cached and not expired.
func (c *ServiceCache) Exists(host, name string) bool {
	return c.GetState(host, name) != StateNotFound
}

// Register marks the service as ready in the cache.
func (c *ServiceCache) Register(host, name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[ServiceKey(host, name)] = serviceEntry{State: StateReady, CreatedAt: time.Now()}
}

// SetPending marks the service as pending (deploy in progress).
func (c *ServiceCache) SetPending(host, name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[ServiceKey(host, name)] = serviceEntry{State: StatePending, CreatedAt: time.Now()}
}

// SetPendingDelete marks the service as pending deletion.
func (c *ServiceCache) SetPendingDelete(host, name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[ServiceKey(host, name)] = serviceEntry{State: StatePendingDelete, CreatedAt: time.Now()}
}

// Remove deletes the service from the cache.
func (c *ServiceCache) Remove(host, name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, ServiceKey(host, name))
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

// AllEntries returns a sorted snapshot of all non-expired cache entries.
func (c *ServiceCache) AllEntries() []CacheEntry {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entries := make([]CacheEntry, 0, len(c.entries))
	now := time.Now()
	for key, entry := range c.entries {
		if now.Sub(entry.CreatedAt) > c.ttl {
			continue
		}
		host, service := SplitServiceKey(key)
		entries = append(entries, CacheEntry{
			Key:     key,
			Host:    host,
			Service: service,
			State:   entry.State,
		})
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Host == entries[j].Host {
			return entries[i].Service < entries[j].Service
		}
		return entries[i].Host < entries[j].Host
	})

	return entries
}

// EvictExpired removes expired entries from the cache to prevent unbounded memory growth.
func (c *ServiceCache) EvictExpired() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	evicted := 0
	for name, entry := range c.entries {
		if now.Sub(entry.CreatedAt) > c.ttl {
			delete(c.entries, name)
			evicted++
		}
	}
	return evicted
}

// Len returns the number of entries currently in the cache (including expired).
func (c *ServiceCache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}

package cache

import (
	"context"
	"testing"
	"time"
)

func TestServiceCache_InitialState(t *testing.T) {
	c := NewServiceCache(60)

	if c.Exists("host-a", "unknown-service") {
		t.Error("expected Exists to return false for unknown service")
	}
	if state := c.GetState("host-a", "unknown-service"); state != StateNotFound {
		t.Errorf("expected StateNotFound, got %s", state)
	}
}

func TestServiceCache_RegisterAndExists(t *testing.T) {
	c := NewServiceCache(60)

	c.Register("host-a", "my-service")

	if !c.Exists("host-a", "my-service") {
		t.Error("expected Exists to return true after Register")
	}
	if state := c.GetState("host-a", "my-service"); state != StateReady {
		t.Errorf("expected StateReady, got %s", state)
	}
}

func TestServiceCache_SeparatesHosts(t *testing.T) {
	c := NewServiceCache(60)

	c.Register("host-a", "shared-service")

	if !c.Exists("host-a", "shared-service") {
		t.Fatal("expected host-a/shared-service to exist")
	}
	if c.Exists("host-b", "shared-service") {
		t.Fatal("expected host-b/shared-service to be isolated from host-a")
	}
}

func TestServiceCache_SetPending(t *testing.T) {
	c := NewServiceCache(60)

	c.SetPending("host-a", "my-service")

	if !c.Exists("host-a", "my-service") {
		t.Error("expected Exists to return true after SetPending")
	}
	if state := c.GetState("host-a", "my-service"); state != StatePending {
		t.Errorf("expected StatePending, got %s", state)
	}
}

func TestServiceCache_SetPendingDelete(t *testing.T) {
	c := NewServiceCache(60)

	c.Register("host-a", "my-service")
	c.SetPendingDelete("host-a", "my-service")

	if state := c.GetState("host-a", "my-service"); state != StatePendingDelete {
		t.Errorf("expected StatePendingDelete, got %s", state)
	}
}

func TestServiceCache_Remove(t *testing.T) {
	c := NewServiceCache(60)

	c.Register("host-a", "my-service")
	c.Remove("host-a", "my-service")

	if c.Exists("host-a", "my-service") {
		t.Error("expected Exists to return false after Remove")
	}
}

func TestServiceCache_TTLExpiry(t *testing.T) {
	c := NewServiceCache(0)

	c.Register("host-a", "my-service")

	if state := c.GetState("host-a", "my-service"); state != StateNotFound {
		t.Errorf("expected expired entry to return StateNotFound, got %s", state)
	}
}

func TestServiceCache_All(t *testing.T) {
	c := NewServiceCache(60)

	c.Register("host-a", "svc-a")
	c.SetPending("host-b", "svc-b")
	c.Register("host-a", "svc-c")

	all := c.All()
	if len(all) != 3 {
		t.Errorf("expected 3 entries, got %d", len(all))
	}
	if all[ServiceKey("host-a", "svc-a")] != StateReady {
		t.Errorf("expected host-a/svc-a to be ready, got %s", all[ServiceKey("host-a", "svc-a")])
	}
	if all[ServiceKey("host-b", "svc-b")] != StatePending {
		t.Errorf("expected host-b/svc-b to be pending, got %s", all[ServiceKey("host-b", "svc-b")])
	}
}

func TestServiceCache_AllEntries(t *testing.T) {
	c := NewServiceCache(60)
	c.Register("host-b", "svc-b")
	c.Register("host-a", "svc-a")

	entries := c.AllEntries()
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Host != "host-a" || entries[0].Service != "svc-a" {
		t.Fatalf("expected sorted entries, got %#v", entries)
	}
}

func TestServiceCache_EvictExpired(t *testing.T) {
	c := NewServiceCache(0)
	c.Register("host-a", "expired-service")

	evicted := c.EvictExpired()

	if evicted != 1 {
		t.Fatalf("expected 1 evicted entry, got %d", evicted)
	}
	if c.Len() != 0 {
		t.Fatalf("expected cache to be empty after eviction, got %d entries", c.Len())
	}
}

func TestServiceCache_StartMaintenance(t *testing.T) {
	c := NewServiceCache(0)
	c.Register("host-a", "expired-service")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c.StartMaintenance(ctx, 10*time.Millisecond)
	time.Sleep(40 * time.Millisecond)

	if c.Len() != 0 {
		t.Fatalf("expected maintenance to evict expired entries, got %d entries", c.Len())
	}
}

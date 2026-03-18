package cache

import (
	"testing"
)

func TestServiceCache_InitialState(t *testing.T) {
	c := NewServiceCache(60)

	if c.Exists("unknown-service") {
		t.Error("expected Exists to return false for unknown service")
	}
	if state := c.GetState("unknown-service"); state != StateNotFound {
		t.Errorf("expected StateNotFound, got %s", state)
	}
}

func TestServiceCache_RegisterAndExists(t *testing.T) {
	c := NewServiceCache(60)

	c.Register("my-service")

	if !c.Exists("my-service") {
		t.Error("expected Exists to return true after Register")
	}
	if state := c.GetState("my-service"); state != StateReady {
		t.Errorf("expected StateReady, got %s", state)
	}
}

func TestServiceCache_SetPending(t *testing.T) {
	c := NewServiceCache(60)

	c.SetPending("my-service")

	if !c.Exists("my-service") {
		t.Error("expected Exists to return true after SetPending")
	}
	if state := c.GetState("my-service"); state != StatePending {
		t.Errorf("expected StatePending, got %s", state)
	}
}

func TestServiceCache_SetPendingDelete(t *testing.T) {
	c := NewServiceCache(60)

	c.Register("my-service")
	c.SetPendingDelete("my-service")

	if state := c.GetState("my-service"); state != StatePendingDelete {
		t.Errorf("expected StatePendingDelete, got %s", state)
	}
}

func TestServiceCache_Remove(t *testing.T) {
	c := NewServiceCache(60)

	c.Register("my-service")
	c.Remove("my-service")

	if c.Exists("my-service") {
		t.Error("expected Exists to return false after Remove")
	}
}

func TestServiceCache_TTLExpiry(t *testing.T) {
	// Use TTL of 0 minutes — entries expire immediately
	c := NewServiceCache(0)

	c.Register("my-service")

	// With TTL=0, the entry is expired immediately
	if state := c.GetState("my-service"); state != StateNotFound {
		t.Errorf("expected expired entry to return StateNotFound, got %s", state)
	}
}

func TestServiceCache_All(t *testing.T) {
	c := NewServiceCache(60)

	c.Register("svc-a")
	c.SetPending("svc-b")
	c.Register("svc-c")

	all := c.All()
	if len(all) != 3 {
		t.Errorf("expected 3 entries, got %d", len(all))
	}
	if all["svc-a"] != StateReady {
		t.Errorf("expected svc-a to be ready, got %s", all["svc-a"])
	}
	if all["svc-b"] != StatePending {
		t.Errorf("expected svc-b to be pending, got %s", all["svc-b"])
	}
}

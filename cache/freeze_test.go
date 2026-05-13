package cache

import (
	"testing"
	"time"
)

func TestFreeze_PermanentAndUnfreeze(t *testing.T) {
	c := NewServiceCache(60)

	if c.IsFrozen("h", "svc") {
		t.Error("should not be frozen initially")
	}

	c.Freeze("h", "svc", nil) // permanent
	if !c.IsFrozen("h", "svc") {
		t.Error("should be frozen after Freeze(nil)")
	}

	frozen, until := c.GetFreezeInfo("h", "svc")
	if !frozen {
		t.Error("GetFreezeInfo: expected frozen=true")
	}
	if until != nil {
		t.Error("GetFreezeInfo: expected until=nil for permanent freeze")
	}

	c.Unfreeze("h", "svc")
	if c.IsFrozen("h", "svc") {
		t.Error("should not be frozen after Unfreeze")
	}
}

func TestFreeze_WithExpiry(t *testing.T) {
	c := NewServiceCache(60)

	future := time.Now().Add(time.Hour)
	c.Freeze("h", "svc", &future)

	if !c.IsFrozen("h", "svc") {
		t.Error("should be frozen within expiry window")
	}
	frozen, until := c.GetFreezeInfo("h", "svc")
	if !frozen || until == nil {
		t.Errorf("expected frozen=true and non-nil until, got frozen=%v until=%v", frozen, until)
	}
}

func TestFreeze_ExpiredTreatedAsUnfrozen(t *testing.T) {
	c := NewServiceCache(60)

	past := time.Now().Add(-time.Second) // already expired
	c.Freeze("h", "svc", &past)

	if c.IsFrozen("h", "svc") {
		t.Error("expired freeze should be treated as unfrozen")
	}
}

func TestAllFrozen(t *testing.T) {
	c := NewServiceCache(60)

	future := time.Now().Add(time.Hour)
	past := time.Now().Add(-time.Second)

	c.Freeze("h", "svc-a", nil)        // permanent
	c.Freeze("h", "svc-b", &future)    // future expiry
	c.Freeze("h", "svc-c", &past)      // expired — should not appear

	entries := c.AllFrozen()
	if len(entries) != 2 {
		t.Errorf("expected 2 frozen entries, got %d", len(entries))
	}
	names := map[string]bool{}
	for _, e := range entries {
		names[e.Service] = true
	}
	if !names["svc-a"] || !names["svc-b"] {
		t.Errorf("unexpected frozen entries: %v", entries)
	}
	if names["svc-c"] {
		t.Error("expired entry svc-c should not appear in AllFrozen")
	}
}

func TestUnfreeze_NonExistent(t *testing.T) {
	c := NewServiceCache(60)
	// Should not panic for non-existent key
	c.Unfreeze("h", "does-not-exist")
}

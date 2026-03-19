package metrics

import (
	"testing"
)

func TestCollector_RequestMetrics(t *testing.T) {
	c := NewCollector()

	c.RecordRequest(50)
	c.RecordRequest(100)
	c.RecordRequest(150)
	c.RecordError()

	snap := c.Snapshot()

	if snap.TotalRequests != 3 {
		t.Errorf("expected 3 requests, got %d", snap.TotalRequests)
	}
	if snap.TotalErrors != 1 {
		t.Errorf("expected 1 error, got %d", snap.TotalErrors)
	}
	if snap.AvgLatencyMs != 100 {
		t.Errorf("expected avg latency 100ms, got %.1f", snap.AvgLatencyMs)
	}
	if snap.ErrorRate < 33 || snap.ErrorRate > 34 {
		t.Errorf("expected ~33%% error rate, got %.1f%%", snap.ErrorRate)
	}
}

func TestCollector_AuthFailures(t *testing.T) {
	c := NewCollector()

	// Simulate brute force from same IP
	for i := 0; i < 5; i++ {
		c.RecordAuthFailure("192.168.1.100", "bad-key-attempt")
	}
	// Single attempt from different IP
	c.RecordAuthFailure("10.0.0.1", "wrong")

	snap := c.Snapshot()

	if snap.FailedAuthTotal != 6 {
		t.Errorf("expected 6 failed auths, got %d", snap.FailedAuthTotal)
	}

	if len(snap.BruteForceIPs) != 1 {
		t.Fatalf("expected 1 brute force IP, got %d", len(snap.BruteForceIPs))
	}
	if snap.BruteForceIPs[0].IP != "192.168.1.100" {
		t.Errorf("expected IP 192.168.1.100, got %s", snap.BruteForceIPs[0].IP)
	}
	if snap.BruteForceIPs[0].Attempts != 5 {
		t.Errorf("expected 5 attempts, got %d", snap.BruteForceIPs[0].Attempts)
	}
}

func TestCollector_KeyPrefixTruncation(t *testing.T) {
	c := NewCollector()

	c.RecordAuthFailure("1.2.3.4", "super-long-secret-key-12345")
	c.RecordAuthFailure("1.2.3.4", "")

	snap := c.Snapshot()
	if len(snap.FailedAuthRecent) != 2 {
		t.Fatalf("expected 2 recent failures, got %d", len(snap.FailedAuthRecent))
	}
	if snap.FailedAuthRecent[0].KeyPrefix != "super-lo..." {
		t.Errorf("expected truncated key, got %q", snap.FailedAuthRecent[0].KeyPrefix)
	}
	if snap.FailedAuthRecent[1].KeyPrefix != "(empty)..." {
		t.Errorf("expected (empty)..., got %q", snap.FailedAuthRecent[1].KeyPrefix)
	}
}

func TestCollector_SystemStats(t *testing.T) {
	c := NewCollector()
	snap := c.Snapshot()

	if snap.NumCPU < 1 {
		t.Errorf("expected at least 1 CPU, got %d", snap.NumCPU)
	}
	if snap.GoRoutines < 1 {
		t.Errorf("expected at least 1 goroutine, got %d", snap.GoRoutines)
	}
	if snap.MemAllocMB <= 0 {
		t.Errorf("expected positive memory allocation, got %.2f", snap.MemAllocMB)
	}
	if snap.Uptime == "" {
		t.Error("expected non-empty uptime")
	}
}

package health

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type mockProber struct {
	failCount atomic.Int32
	calls     atomic.Int32
}

func (m *mockProber) GetHostInfo(host string) (HostResult, error) {
	n := m.calls.Add(1)
	if int(n) <= int(m.failCount.Load()) {
		return HostResult{}, errors.New("connection refused")
	}
	return HostResult{Exists: true}, nil
}

func (m *mockProber) SendCheckResult(host, service string, exitStatus int, message string) error {
	return nil
}

func (m *mockProber) CreateService(host, name string, labels, annotations map[string]string) error {
	return nil
}

func TestHealthChecker_Healthy(t *testing.T) {
	prober := &mockProber{}
	c := New(Config{
		Enabled:     true,
		IntervalSec: 1,
		TargetHost:  "test-host",
		ServiceName: "bridge-health",
	}, prober)

	ctx, cancel := context.WithCancel(context.Background())
	go c.Start(ctx)

	time.Sleep(150 * time.Millisecond)
	cancel()

	status := c.GetStatus()
	if !status.Healthy {
		t.Error("expected healthy after successful check")
	}
	if !status.IcingaUp {
		t.Error("expected IcingaUp=true")
	}
	if status.TotalChecks < 1 {
		t.Error("expected at least 1 check")
	}
}

func TestHealthChecker_Unhealthy(t *testing.T) {
	prober := &mockProber{}
	prober.failCount.Store(100) // always fail

	c := New(Config{
		Enabled:     true,
		IntervalSec: 1,
		TargetHost:  "test-host",
		ServiceName: "bridge-health",
	}, prober)

	// Run checks manually to trigger unhealthy state
	for i := 0; i < 4; i++ {
		c.runCheck()
	}

	status := c.GetStatus()
	if status.Healthy {
		t.Error("expected unhealthy after 4 consecutive failures")
	}
	if status.IcingaUp {
		t.Error("expected IcingaUp=false")
	}
	if status.ConsecutiveFails != 4 {
		t.Errorf("expected 4 consecutive fails, got %d", status.ConsecutiveFails)
	}
	if status.LastError == "" {
		t.Error("expected error message")
	}
}

func TestHealthChecker_Recovery(t *testing.T) {
	prober := &mockProber{}
	prober.failCount.Store(3) // first 3 fail, then succeed

	c := New(Config{
		Enabled:     true,
		IntervalSec: 1,
		TargetHost:  "test-host",
		ServiceName: "bridge-health",
	}, prober)

	// Fail 3 times
	for i := 0; i < 3; i++ {
		c.runCheck()
	}
	status := c.GetStatus()
	if status.Healthy {
		t.Error("expected unhealthy after 3 failures")
	}

	// 4th check succeeds
	c.runCheck()
	status = c.GetStatus()
	if !status.Healthy {
		t.Error("expected recovery after successful check")
	}
	if status.ConsecutiveFails != 0 {
		t.Errorf("expected consecutive fails reset, got %d", status.ConsecutiveFails)
	}
	if status.TotalFailures != 3 {
		t.Errorf("expected 3 total failures, got %d", status.TotalFailures)
	}
}

func TestHealthChecker_Register(t *testing.T) {
	prober := &mockProber{}
	c := New(Config{
		Enabled:     true,
		IntervalSec: 1,
		TargetHost:  "test-host",
		ServiceName: "bridge-health",
		Register:    true,
	}, prober)

	ctx, cancel := context.WithCancel(context.Background())
	go c.Start(ctx)
	time.Sleep(150 * time.Millisecond)
	cancel()

	// Verify checks ran (CreateService + GetHostInfo + SendCheckResult)
	if prober.calls.Load() < 1 {
		t.Error("expected API calls for registration and health check")
	}
}

func TestHealthChecker_Disabled(t *testing.T) {
	prober := &mockProber{}
	c := New(Config{
		Enabled: false,
	}, prober)

	ctx, cancel := context.WithCancel(context.Background())
	go c.Start(ctx)
	time.Sleep(100 * time.Millisecond)
	cancel()

	if prober.calls.Load() != 0 {
		t.Error("expected no API calls when disabled")
	}
}

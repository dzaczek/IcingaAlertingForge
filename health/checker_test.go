package health

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type mockProber struct {
	failCount         atomic.Int32
	calls             atomic.Int32
	sendCheckErr      error
	createServiceErr  error
}

func (m *mockProber) GetHostInfo(host string) (HostResult, error) {
	n := m.calls.Add(1)
	if int(n) <= int(m.failCount.Load()) {
		return HostResult{}, errors.New("connection refused")
	}
	return HostResult{Exists: true}, nil
}

func (m *mockProber) SendCheckResult(host, service string, exitStatus int, message string) error {
	return m.sendCheckErr
}

func (m *mockProber) CreateService(host, name string, labels, annotations map[string]string) error {
	return m.createServiceErr
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

func TestHealthChecker_ErrorPaths(t *testing.T) {
	prober := &mockProber{
		sendCheckErr:     errors.New("mock send error"),
		createServiceErr: errors.New("mock create service error"),
	}

	c := New(Config{
		Enabled:     true,
		IntervalSec: 1,
		TargetHost:  "test-host",
		ServiceName: "bridge-health",
		Register:    true,
	}, prober)

	// This triggers registerService() which will hit the createServiceErr path
	c.registerService()

	// This triggers runCheck() which will hit the sendCheckErr path
	c.runCheck()

	status := c.GetStatus()
	if !status.Healthy {
		t.Errorf("expected healthy=true when getHostInfo succeeds, got %v", status.Healthy)
	}

	// Now trigger runCheck but make the API fail to hit the other sendCheckErr path
	// This also makes healthy=false which covers the "else" branch inside runCheck
	prober.failCount.Store(100)
	c.runCheck()

	status = c.GetStatus()
	if status.ConsecutiveFails != 1 {
		t.Errorf("expected consecutive fails = 1, got %d", status.ConsecutiveFails)
	}
	if !status.Healthy {
		t.Errorf("expected still healthy after 1 fail, got %v", status.Healthy)
	}

	c.runCheck()
	c.runCheck()

	status = c.GetStatus()
	if status.ConsecutiveFails != 3 {
		t.Errorf("expected consecutive fails = 3, got %d", status.ConsecutiveFails)
	}
	if status.Healthy {
		t.Errorf("expected healthy=false after 3 fails, got %v", status.Healthy)
	}
}

func TestHealthChecker_Ticker(t *testing.T) {
	prober := &mockProber{}
	c := New(Config{
		Enabled:     true,
		IntervalSec: 1,
		TargetHost:  "test-host",
		ServiceName: "bridge-health",
		Register:    false,
	}, prober)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Run Start in a goroutine
	go c.Start(ctx)

	// We can use a polling loop to wait for the ticker to fire, which is more robust than a sleep.
	// Since interval is 1s, we'll poll for up to 3s.
	success := false
	for i := 0; i < 30; i++ {
		time.Sleep(100 * time.Millisecond)
		if prober.calls.Load() >= 2 {
			success = true
			break
		}
	}

	if !success {
		t.Errorf("expected at least 2 checks to have run due to ticker, got %d", prober.calls.Load())
	}
}

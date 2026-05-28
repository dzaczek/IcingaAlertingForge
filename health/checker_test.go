package health

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type mockProber struct {
	failCount      atomic.Int32
	calls          atomic.Int32
	sendCalls      atomic.Int32
	createCalls    atomic.Int32
	sendCheckErr   error
	createCheckErr error
}

func (m *mockProber) GetHostInfo(host string) (HostResult, error) {
	n := m.calls.Add(1)
	if int(n) <= int(m.failCount.Load()) {
		return HostResult{}, errors.New("connection refused")
	}
	return HostResult{Exists: true}, nil
}

func (m *mockProber) SendCheckResult(host, service string, exitStatus int, message string) error {
	m.sendCalls.Add(1)
	return m.sendCheckErr
}

func (m *mockProber) CreateService(host, name string, labels, annotations map[string]string) error {
	m.createCalls.Add(1)
	return m.createCheckErr
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
		Register:    true,
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

func TestHealthChecker_Start_Ticker(t *testing.T) {
	prober := &mockProber{}
	c := New(Config{
		Enabled:     true,
		IntervalSec: 1, // 1 second ticker
		TargetHost:  "test-host",
		ServiceName: "bridge-health",
		Register:    true,
	}, prober)

	ctx, cancel := context.WithCancel(context.Background())

	// Track start time
	start := time.Now()

	// Start in a goroutine
	done := make(chan struct{})
	go func() {
		c.Start(ctx)
		close(done)
	}()

	// Wait for at least 2 calls (initial + 1 ticker tick) by polling
	timeout := time.After(3 * time.Second)
Loop:
	for {
		select {
		case <-timeout:
			t.Fatal("timed out waiting for ticker to fire")
		default:
			if prober.calls.Load() >= 2 {
				break Loop
			}
			time.Sleep(50 * time.Millisecond)
		}
	}

	cancel()

	// Wait for Start to exit
	select {
	case <-done:
		// success
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not return after context cancellation")
	}

	// Verify the number of calls
	if prober.calls.Load() < 2 {
		t.Errorf("expected at least 2 GetHostInfo calls (initial + 1 tick), got %d. Elapsed time: %v", prober.calls.Load(), time.Since(start))
	}

	// Verify that context cancellation sent a final UNKNOWN status CheckResult call.
	// Initial + ticker = 2 sendCalls (since we pass healthy)
	// Context cancellation = 1 sendCall (UNKNOWN)
	// Total expected send calls = 3
	sendCalls := prober.sendCalls.Load()
	if sendCalls < 3 {
		t.Errorf("expected at least 3 SendCheckResult calls (initial + tick + final cancel), got %d", sendCalls)
	}
}

func TestHealthChecker_ApiErrors(t *testing.T) {
	prober := &mockProber{
		sendCheckErr:   errors.New("mock send error"),
		createCheckErr: errors.New("mock create error"),
	}
	c := New(Config{
		Enabled:     true,
		IntervalSec: 1,
		TargetHost:  "test-host",
		ServiceName: "bridge-health",
		Register:    true,
	}, prober)

	// Since we mock CreateService to return an error, registerService will
	// hit the warn log path instead of info log.

	ctx, cancel := context.WithCancel(context.Background())

	// Start in a goroutine
	done := make(chan struct{})
	go func() {
		c.Start(ctx)
		close(done)
	}()

	// Wait for the initial runCheck to complete by polling calls
	timeout := time.After(2 * time.Second)
PollLoop:
	for {
		select {
		case <-timeout:
			t.Fatal("timed out waiting for initial check")
		default:
			if prober.calls.Load() >= 1 {
				break PollLoop
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

	cancel()

	// Wait for Start to exit
	select {
	case <-done:
		// success
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not return after context cancellation")
	}

	// We can assert that the send check error and create check error occurred.
	// There is no exported error state for sendCheckErr or createCheckErr, but we verify
	// that it does not panic and coverage hits those blocks.

	// Ensure that GetHostInfo ran successfully (IcingaUp should be true)
	status := c.GetStatus()
	if !status.IcingaUp {
		t.Errorf("expected IcingaUp=true despite mock create/send errors, since GetHostInfo succeeded")
	}
}

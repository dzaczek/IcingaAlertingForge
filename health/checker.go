// Package health provides a reverse health checker that periodically probes
// the Icinga2 API and optionally self-registers the bridge as a monitored
// service so Icinga2 alerts when the bridge itself is unhealthy.
package health

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// IcingaProber abstracts the Icinga2 API calls needed by the health checker.
type IcingaProber interface {
	GetHostInfo(host string) (HostResult, error)
	SendCheckResult(host, service string, exitStatus int, message string) error
	CreateService(host, name string, labels, annotations map[string]string) error
}

// HostResult is a minimal host info for health check purposes.
type HostResult struct {
	Exists bool
}

// Config holds health checker configuration.
type Config struct {
	Enabled     bool
	IntervalSec int
	ServiceName string // name of the self-monitoring service in Icinga2
	TargetHost  string // Icinga2 host to register under
	Register    bool   // auto-create service in Icinga2
}

// Status represents the current health state.
type Status struct {
	Healthy          bool      `json:"healthy"`
	IcingaUp         bool      `json:"icinga_up"`
	LastCheck        time.Time `json:"last_check"`
	LastSuccess      time.Time `json:"last_success"`
	LastError        string    `json:"last_error,omitempty"`
	ConsecutiveFails int       `json:"consecutive_fails"`
	TotalChecks      int64     `json:"total_checks"`
	TotalFailures    int64     `json:"total_failures"`
	Uptime           string    `json:"uptime,omitempty"`
}

// Checker runs periodic health probes against Icinga2.
type Checker struct {
	cfg       Config
	api       IcingaProber
	startedAt time.Time

	mu     sync.RWMutex
	status Status
}

// New creates a new health checker.
func New(cfg Config, api IcingaProber) *Checker {
	return &Checker{
		cfg:       cfg,
		api:       api,
		startedAt: time.Now(),
		status: Status{
			Healthy: true,
		},
	}
}

// Start begins the periodic health check loop. It blocks until ctx is cancelled.
func (c *Checker) Start(ctx context.Context) {
	if !c.cfg.Enabled {
		return
	}

	interval := time.Duration(c.cfg.IntervalSec) * time.Second
	slog.Info("Health checker started",
		"interval", interval,
		"service", c.cfg.ServiceName,
		"target_host", c.cfg.TargetHost,
		"register", c.cfg.Register,
	)

	// Register self-monitoring service if configured
	if c.cfg.Register && c.cfg.TargetHost != "" && c.cfg.ServiceName != "" {
		c.registerService()
	}

	// Run first check immediately
	c.runCheck()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("Health checker stopped")
			// Send final UNKNOWN status
			if c.cfg.Register && c.cfg.TargetHost != "" {
				_ = c.api.SendCheckResult(c.cfg.TargetHost, c.cfg.ServiceName, 3,
					"UNKNOWN: Bridge shutting down")
			}
			return
		case <-ticker.C:
			c.runCheck()
		}
	}
}

// GetStatus returns a copy of the current health status.
func (c *Checker) GetStatus() Status {
	c.mu.RLock()
	defer c.mu.RUnlock()
	s := c.status
	s.Uptime = time.Since(c.startedAt).Round(time.Second).String()
	return s
}

func (c *Checker) runCheck() {
	c.mu.Lock()
	c.status.TotalChecks++
	c.mu.Unlock()

	// Probe: try to reach Icinga2 API
	_, err := c.api.GetHostInfo(c.cfg.TargetHost)

	c.mu.Lock()
	c.status.LastCheck = time.Now()

	if err != nil {
		c.status.IcingaUp = false
		c.status.ConsecutiveFails++
		c.status.TotalFailures++
		c.status.LastError = err.Error()

		// Consider unhealthy after 3 consecutive failures
		if c.status.ConsecutiveFails >= 3 {
			c.status.Healthy = false
		}

		slog.Warn("Health check failed",
			"consecutive_fails", c.status.ConsecutiveFails,
			"error", err,
		)
	} else {
		c.status.IcingaUp = true
		c.status.LastSuccess = time.Now()
		c.status.LastError = ""
		c.status.ConsecutiveFails = 0
		c.status.Healthy = true
	}

	healthy := c.status.Healthy
	consecutiveFails := c.status.ConsecutiveFails
	c.mu.Unlock()

	// Report to Icinga2 if self-registration is enabled
	if c.cfg.Register && c.cfg.TargetHost != "" && c.cfg.ServiceName != "" {
		var exitStatus int
		var message string
		if healthy {
			exitStatus = 0
			message = fmt.Sprintf("OK: Bridge healthy, Icinga2 API reachable | checks=%d failures=%d",
				c.status.TotalChecks, c.status.TotalFailures)
		} else {
			exitStatus = 2
			message = fmt.Sprintf("CRITICAL: Icinga2 API unreachable for %d consecutive checks | checks=%d failures=%d",
				consecutiveFails, c.status.TotalChecks, c.status.TotalFailures)
		}

		if sendErr := c.api.SendCheckResult(c.cfg.TargetHost, c.cfg.ServiceName, exitStatus, message); sendErr != nil {
			slog.Debug("Health check: could not report status to Icinga2", "error", sendErr)
		}
	}
}

func (c *Checker) registerService() {
	labels := map[string]string{
		"managed_by": "IcingaAlertingForge",
		"component":  "health-checker",
	}
	annotations := map[string]string{
		"summary": "IcingaAlertForge bridge health monitor",
	}
	err := c.api.CreateService(c.cfg.TargetHost, c.cfg.ServiceName, labels, annotations)
	if err != nil {
		slog.Warn("Health checker: could not register self-monitoring service (may already exist)",
			"host", c.cfg.TargetHost, "service", c.cfg.ServiceName, "error", err)
	} else {
		slog.Info("Health checker: self-monitoring service registered",
			"host", c.cfg.TargetHost, "service", c.cfg.ServiceName)
	}
}

package reports

import (
	"context"
	"fmt"
	"time"
)

// HealthStatus is the outcome of a HealthChecker.Check call.
type HealthStatus struct {
	Healthy bool
	Message string
}

// protocolProvider abstracts a mechanism for retrieving the latest SyncProtocol.
// Defined here, at the consumer, so tests can supply a fake without a real DB.
type protocolProvider interface {
	Latest(ctx context.Context) (*SyncProtocol, error)
}

// HealthChecker decides whether the application is healthy by inspecting the
// most recently persisted SyncProtocol.
//
// The check is uniform: the application is healthy when the latest
// protocol's last_activity_at is within Tolerance of "now". Because every
// per-repo result write bumps last_activity_at, a stuck cycle is detected
// without waiting for it to complete. A completed cycle's last_activity_at
// equals its ended_at, so the same rule covers the post-completion window
// (during which the daemon is sleeping until the next tick).
//
// A missing latest protocol (just after first boot) is reported as
// unhealthy; the Docker HEALTHCHECK's start_period grace window is the right
// tool to suppress that during initial boot.
type HealthChecker struct {
	source    protocolProvider
	tolerance time.Duration
	now       func() time.Time // injectable time provider for testability
}

// NewHealthChecker constructs a checker that tolerates up to the given gap
// between activity events.
func NewHealthChecker(source protocolProvider, tolerance time.Duration) *HealthChecker {
	return NewHealthCheckerWithClock(source, tolerance, func() time.Time { return time.Now().UTC() })
}

// NewHealthCheckerWithClock is like NewHealthChecker but accepts an
// injectable clock, which is useful in tests.
func NewHealthCheckerWithClock(source protocolProvider, tolerance time.Duration, now func() time.Time) *HealthChecker {
	return &HealthChecker{
		source:    source,
		tolerance: tolerance,
		now:       now,
	}
}

// Check evaluates application health.
func (h *HealthChecker) Check(ctx context.Context) HealthStatus {
	p, err := h.source.Latest(ctx)
	if err != nil {
		return HealthStatus{
			Healthy: false,
			Message: fmt.Sprintf("UNHEALTHY: failed to read sync history: %v", err),
		}
	}
	if p == nil {
		return HealthStatus{
			Healthy: false,
			Message: "UNHEALTHY: no sync cycle has started yet",
		}
	}

	idle := h.now().Sub(p.LastActivityAt)
	if idle > h.tolerance {
		return HealthStatus{
			Healthy: false,
			Message: fmt.Sprintf(
				"UNHEALTHY: no sync activity for %s (tolerance %s) — cycle stuck or daemon not running",
				idle.Truncate(time.Second), h.tolerance,
			),
		}
	}

	if p.EndedAt == nil {
		return HealthStatus{
			Healthy: true,
			Message: fmt.Sprintf(
				"OK: sync cycle in progress, last activity %s ago (succeeded=%d, failed=%d so far)",
				idle.Truncate(time.Second), p.SucceededRepos, p.FailedRepos,
			),
		}
	}
	return HealthStatus{
		Healthy: true,
		Message: fmt.Sprintf(
			"OK: last sync ended %s ago, %d/%d repos succeeded",
			idle.Truncate(time.Second), p.SucceededRepos, p.TotalRepos,
		),
	}
}

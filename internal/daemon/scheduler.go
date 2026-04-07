package daemon

import (
	"context"
	"time"
)

// Scheduler is the boundary between the daemon runner and whatever decides
// when to run scans. SPEC-X1 ships only NoopScheduler; SPEC-X2 will swap in
// a real cron-style implementation without touching the runner or service
// plumbing.
//
// Implementations must be safe for a single Run call per instance. Run
// blocks until ctx is cancelled.
type Scheduler interface {
	// Next returns the time of the next scheduled scan, or the zero value
	// if none is scheduled. The runner reports this in the state file so
	// `surfbot daemon status` can show "next scan: ..." to users.
	Next() time.Time

	// Run blocks until ctx is cancelled. Real schedulers trigger scans on
	// their internal cadence; NoopScheduler simply waits.
	Run(ctx context.Context) error
}

// NoopScheduler is the default Scheduler used in X1. It reports a fixed
// "next scan in 24h" so daemon status displays a non-zero value, then
// blocks until cancelled without doing any work.
type NoopScheduler struct {
	startedAt time.Time
}

func NewNoopScheduler() *NoopScheduler {
	return &NoopScheduler{startedAt: time.Now()}
}

func (n *NoopScheduler) Next() time.Time {
	return n.startedAt.Add(24 * time.Hour)
}

func (n *NoopScheduler) Run(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

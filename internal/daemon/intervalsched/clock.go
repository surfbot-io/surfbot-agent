// Package intervalsched implements a config-driven Scheduler for the
// surfbot daemon: full scans on one interval, quick checks on another,
// honoring a maintenance window. It satisfies daemon.Scheduler from X1.
//
// Every timing decision goes through the Clock interface so unit tests can
// drive the scheduler with a fake clock and assert exact tick ordering
// without sleeping.
package intervalsched

import "time"

// Clock abstracts wall-clock time so tests can use a fake.
type Clock interface {
	Now() time.Time
	NewTimer(d time.Duration) Timer
}

// Timer mirrors the bits of *time.Timer the scheduler uses.
type Timer interface {
	C() <-chan time.Time
	Stop() bool
}

// realClock is the production Clock backed by the time package.
type realClock struct{}

// NewRealClock returns a Clock backed by the standard time package.
func NewRealClock() Clock { return realClock{} }

func (realClock) Now() time.Time { return time.Now() }

func (realClock) NewTimer(d time.Duration) Timer {
	return &realTimer{t: time.NewTimer(d)}
}

type realTimer struct{ t *time.Timer }

func (r *realTimer) C() <-chan time.Time { return r.t.C }
func (r *realTimer) Stop() bool          { return r.t.Stop() }

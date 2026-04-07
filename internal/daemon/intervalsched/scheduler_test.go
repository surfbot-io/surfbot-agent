package intervalsched

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fakeClock is a deterministic Clock for scheduler tests. NewTimer hands
// the test a channel it controls via Fire(); Now() returns the current
// virtual time which Advance() moves forward.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
}

func newFakeClock(start time.Time) *fakeClock { return &fakeClock{now: start} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) NewTimer(d time.Duration) Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTimer{ch: make(chan time.Time, 1), fireAt: c.now.Add(d)}
	c.timers = append(c.timers, t)
	return t
}

// advanceAndFire jumps virtual time forward by d and fires the most
// recently created pending timer. If no timer exists yet (the scheduler
// goroutine hasn't reached its select), it polls briefly.
func (c *fakeClock) advanceAndFire(d time.Duration) {
	deadline := time.Now().Add(2 * time.Second)
	for {
		c.mu.Lock()
		var t *fakeTimer
		for i := len(c.timers) - 1; i >= 0; i-- {
			if !c.timers[i].stopped && !c.timers[i].fired {
				t = c.timers[i]
				break
			}
		}
		if t != nil {
			c.now = c.now.Add(d)
			now := c.now
			c.mu.Unlock()
			t.fire(now)
			return
		}
		c.mu.Unlock()
		if time.Now().After(deadline) {
			return
		}
		time.Sleep(time.Millisecond)
	}
}

type fakeTimer struct {
	mu      sync.Mutex
	ch      chan time.Time
	fireAt  time.Time
	fired   bool
	stopped bool
}

func (t *fakeTimer) C() <-chan time.Time { return t.ch }
func (t *fakeTimer) Stop() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	was := !t.fired && !t.stopped
	t.stopped = true
	return was
}
func (t *fakeTimer) fire(now time.Time) {
	t.mu.Lock()
	if t.fired || t.stopped {
		t.mu.Unlock()
		return
	}
	t.fired = true
	t.mu.Unlock()
	t.ch <- now
}

// recordingScanner is a ScanRunner that records every Run call. Optional
// runErr lets a test simulate failures.
type recordingScanner struct {
	mu      sync.Mutex
	calls   []Profile
	runErr  error
	delay   time.Duration
	blockCh chan struct{}
}

func (r *recordingScanner) Run(ctx context.Context, p Profile) error {
	r.mu.Lock()
	r.calls = append(r.calls, p)
	delay := r.delay
	block := r.blockCh
	err := r.runErr
	r.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return err
}

func (r *recordingScanner) snapshot() []Profile {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Profile, len(r.calls))
	copy(out, r.calls)
	return out
}

// waitFor polls fn until it returns true or the deadline elapses.
func waitFor(t *testing.T, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("waitFor: condition never met")
}

func TestScheduler_FullOnly_Ticks(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
	scanner := &recordingScanner{}
	s := New(Config{FullInterval: time.Hour}, Options{Clock: clk, Scanner: scanner, RandSeed: 1})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = s.Run(ctx); close(done) }()

	for i := 0; i < 3; i++ {
		clk.advanceAndFire(time.Hour)
		expect := i + 1
		waitFor(t, func() bool { return len(scanner.snapshot()) >= expect })
	}
	cancel()
	<-done

	calls := scanner.snapshot()
	require.GreaterOrEqual(t, len(calls), 3)
	for _, p := range calls {
		require.Equal(t, ProfileFull, p)
	}
}

func TestScheduler_QuickOnly_Ticks(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
	scanner := &recordingScanner{}
	s := New(Config{FullInterval: 24 * time.Hour, QuickInterval: time.Hour}, Options{Clock: clk, Scanner: scanner, RandSeed: 1})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = s.Run(ctx); close(done) }()

	clk.advanceAndFire(time.Hour)
	waitFor(t, func() bool { return len(scanner.snapshot()) >= 1 })
	cancel()
	<-done

	require.Equal(t, ProfileQuick, scanner.snapshot()[0])
}

func TestScheduler_Interleaved(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
	scanner := &recordingScanner{}
	s := New(Config{FullInterval: 4 * time.Hour, QuickInterval: time.Hour, RunOnStart: true}, Options{Clock: clk, Scanner: scanner, RandSeed: 1})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = s.Run(ctx); close(done) }()

	// Drive the scheduler for ~24h: 24 quick ticks expected (and 6 full,
	// but coalesced when both fall on the same step).
	for i := 0; i < 24; i++ {
		clk.advanceAndFire(time.Hour)
		expect := i + 1
		waitFor(t, func() bool { return len(scanner.snapshot()) >= expect })
	}
	cancel()
	<-done

	calls := scanner.snapshot()
	var fulls, quicks int
	for _, p := range calls {
		switch p {
		case ProfileFull:
			fulls++
		case ProfileQuick:
			quicks++
		}
	}
	require.Greater(t, fulls, 0, "expected at least one full scan")
	require.Greater(t, quicks, fulls, "expected more quick than full scans")
}

func TestScheduler_RunOnStart_OutsideWindow(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
	scanner := &recordingScanner{}
	s := New(Config{FullInterval: time.Hour, RunOnStart: true}, Options{Clock: clk, Scanner: scanner, RandSeed: 1})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = s.Run(ctx); close(done) }()

	waitFor(t, func() bool { return len(scanner.snapshot()) >= 1 })
	cancel()
	<-done
	require.Equal(t, ProfileFull, scanner.snapshot()[0])
}

func TestScheduler_RunOnStart_InsideWindowSkipped(t *testing.T) {
	loc := time.UTC
	clk := newFakeClock(time.Date(2026, 1, 1, 2, 30, 0, 0, loc))
	scanner := &recordingScanner{}
	s := New(Config{
		FullInterval: time.Hour,
		RunOnStart:   true,
		Window:       MaintenanceWindow{Enabled: true, Start: TimeOfDay{2, 0}, End: TimeOfDay{4, 0}, Loc: loc},
	}, Options{Clock: clk, Scanner: scanner, RandSeed: 1})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = s.Run(ctx); close(done) }()

	// Give the goroutine a moment; run-on-start must NOT have fired.
	time.Sleep(20 * time.Millisecond)
	cancel()
	<-done
	require.Empty(t, scanner.snapshot())
}

func TestScheduler_FailedScanAdvancesCursor(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
	scanner := &recordingScanner{runErr: errors.New("boom")}
	s := New(Config{FullInterval: time.Hour}, Options{Clock: clk, Scanner: scanner, RandSeed: 1})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = s.Run(ctx); close(done) }()

	clk.advanceAndFire(time.Hour)
	waitFor(t, func() bool { return len(scanner.snapshot()) >= 1 })
	// Cursor must have advanced even though the scan failed; status reflects failure.
	waitFor(t, func() bool {
		st := s.State()
		return !st.LastFullAt.IsZero() && st.LastFullStatus == "failed"
	})

	clk.advanceAndFire(time.Hour)
	waitFor(t, func() bool { return len(scanner.snapshot()) >= 2 })
	cancel()
	<-done
}

func TestScheduler_PersistsCursorsAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	store := NewScheduleStateStore(filepath.Join(dir, "schedule.state.json"))

	clk := newFakeClock(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
	scanner := &recordingScanner{}
	s := New(Config{FullInterval: time.Hour},
		Options{Clock: clk, Scanner: scanner, StateStore: store, RandSeed: 1})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = s.Run(ctx); close(done) }()
	clk.advanceAndFire(time.Hour)
	waitFor(t, func() bool { return len(scanner.snapshot()) >= 1 })
	waitFor(t, func() bool { return !s.State().LastFullAt.IsZero() })
	cancel()
	<-done

	persisted, err := store.Load()
	require.NoError(t, err)
	require.False(t, persisted.LastFullAt.IsZero())

	// Second instance must hydrate from disk.
	scanner2 := &recordingScanner{}
	s2 := New(Config{FullInterval: time.Hour},
		Options{Clock: newFakeClock(time.Date(2026, 1, 1, 14, 0, 0, 0, time.UTC)),
			Scanner: scanner2, StateStore: store, RandSeed: 1})
	ctx2, cancel2 := context.WithCancel(context.Background())
	done2 := make(chan struct{})
	go func() { _ = s2.Run(ctx2); close(done2) }()
	waitFor(t, func() bool {
		return s2.State().LastFullAt.Equal(persisted.LastFullAt)
	})
	cancel2()
	<-done2
}

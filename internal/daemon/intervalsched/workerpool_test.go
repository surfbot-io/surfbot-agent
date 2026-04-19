package intervalsched

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// runnerFunc adapts a plain function to JobRunner.
type runnerFunc func(ctx context.Context, job Job) error

func (f runnerFunc) Run(ctx context.Context, job Job) error { return f(ctx, job) }

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// trackPeak wraps a runner body while tracking the peak concurrent
// active count observed.
type peakTracker struct {
	mu   sync.Mutex
	cur  int
	peak int
}

func (pt *peakTracker) enter() {
	pt.mu.Lock()
	pt.cur++
	if pt.cur > pt.peak {
		pt.peak = pt.cur
	}
	pt.mu.Unlock()
}

func (pt *peakTracker) leave() {
	pt.mu.Lock()
	pt.cur--
	pt.mu.Unlock()
}

func (pt *peakTracker) Peak() int {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	return pt.peak
}

// dispatchUntil retries Dispatch at a short cadence until the queue
// accepts it or the deadline passes.
func dispatchUntil(t *testing.T, p *WorkerPool, job Job, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if p.Dispatch(job) {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("dispatch timed out after %s", timeout)
}

func TestWorkerPool_BoundedConcurrency(t *testing.T) {
	t.Parallel()
	pt := &peakTracker{}
	runner := runnerFunc(func(ctx context.Context, job Job) error {
		pt.enter()
		time.Sleep(100 * time.Millisecond)
		pt.leave()
		return nil
	})
	pool := NewWorkerPool(2, runner, quietLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)

	for i := 0; i < 5; i++ {
		dispatchUntil(t, pool, Job{ScheduleID: "s", TargetID: "t"}, time.Second)
	}

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopCancel()
	if err := pool.Stop(stopCtx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if pt.Peak() > 2 {
		t.Fatalf("peak active %d exceeds size 2", pt.Peak())
	}
	if pt.Peak() < 1 {
		t.Fatalf("expected jobs to run, peak=%d", pt.Peak())
	}
}

func TestWorkerPool_FullBufferNonBlocking(t *testing.T) {
	t.Parallel()
	// One worker, buffer sized to 1. Submit two blocking jobs: first
	// takes the worker, second sits in the buffer. Third Dispatch must
	// return false immediately.
	hold := make(chan struct{})
	runner := runnerFunc(func(ctx context.Context, job Job) error {
		<-hold
		return nil
	})
	pool := NewWorkerPool(1, runner, quietLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)

	// Fill the running worker.
	if !pool.Dispatch(Job{ScheduleID: "a"}) {
		t.Fatalf("first dispatch failed")
	}
	// Wait until the worker has pulled it off, freeing the buffer.
	deadline := time.Now().Add(time.Second)
	for pool.Active() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	// Now queue up the buffer.
	if !pool.Dispatch(Job{ScheduleID: "b"}) {
		t.Fatalf("buffering dispatch failed")
	}
	start := time.Now()
	accepted := pool.Dispatch(Job{ScheduleID: "c"})
	elapsed := time.Since(start)
	if accepted {
		t.Fatalf("expected third dispatch to return false")
	}
	if elapsed > 10*time.Millisecond {
		t.Fatalf("dispatch took too long: %s", elapsed)
	}
	close(hold)
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopCancel()
	if err := pool.Stop(stopCtx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestWorkerPool_Resize_Grow(t *testing.T) {
	t.Parallel()
	pt := &peakTracker{}
	runner := runnerFunc(func(ctx context.Context, job Job) error {
		pt.enter()
		time.Sleep(80 * time.Millisecond)
		pt.leave()
		return nil
	})
	pool := NewWorkerPool(2, runner, quietLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)

	pool.Resize(4)
	// Dispatch a burst. Buffer was sized to 2 so retry until accepted.
	for i := 0; i < 8; i++ {
		dispatchUntil(t, pool, Job{ScheduleID: "s"}, time.Second)
	}

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopCancel()
	if err := pool.Stop(stopCtx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if pt.Peak() > 4 {
		t.Fatalf("peak active %d exceeds post-grow size 4", pt.Peak())
	}
	if pt.Peak() <= 2 {
		t.Fatalf("expected grow to raise peak above 2, got %d", pt.Peak())
	}
}

func TestWorkerPool_Resize_Shrink(t *testing.T) {
	t.Parallel()
	var active int64
	var peak int64
	// Jobs whose TargetID is "phase1" block on hold1; phase2 blocks on hold2.
	hold1 := make(chan struct{})
	hold2 := make(chan struct{})
	runner := runnerFunc(func(ctx context.Context, job Job) error {
		n := atomic.AddInt64(&active, 1)
		for {
			p := atomic.LoadInt64(&peak)
			if n <= p || atomic.CompareAndSwapInt64(&peak, p, n) {
				break
			}
		}
		switch job.TargetID {
		case "phase1":
			<-hold1
		case "phase2":
			<-hold2
		}
		atomic.AddInt64(&active, -1)
		return nil
	})
	pool := NewWorkerPool(4, runner, quietLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)

	for i := 0; i < 4; i++ {
		dispatchUntil(t, pool, Job{TargetID: "phase1"}, time.Second)
	}
	deadline := time.Now().Add(time.Second)
	for atomic.LoadInt64(&active) < 4 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := atomic.LoadInt64(&active); got != 4 {
		t.Fatalf("expected 4 active, got %d", got)
	}

	pool.Resize(2)
	close(hold1) // let the 4 in-flight finish; 2 workers will exit

	// Wait for 2 workers to exit.
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		pool.mu.Lock()
		workers := pool.workers
		pool.mu.Unlock()
		if workers == 2 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	pool.mu.Lock()
	workers := pool.workers
	pool.mu.Unlock()
	if workers != 2 {
		t.Fatalf("expected 2 remaining workers after shrink, got %d", workers)
	}

	// Reset peak for phase 2.
	atomic.StoreInt64(&peak, 0)

	for i := 0; i < 4; i++ {
		dispatchUntil(t, pool, Job{TargetID: "phase2"}, time.Second)
	}
	// Give workers time to hit peak.
	deadline = time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if atomic.LoadInt64(&active) >= 2 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	close(hold2)
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopCancel()
	if err := pool.Stop(stopCtx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if p := atomic.LoadInt64(&peak); p > 2 {
		t.Fatalf("post-shrink peak %d exceeds 2", p)
	}
}

func TestWorkerPool_Stop_Graceful(t *testing.T) {
	t.Parallel()
	var ran int64
	runner := runnerFunc(func(ctx context.Context, job Job) error {
		time.Sleep(30 * time.Millisecond)
		atomic.AddInt64(&ran, 1)
		return nil
	})
	pool := NewWorkerPool(2, runner, quietLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)

	for i := 0; i < 4; i++ {
		dispatchUntil(t, pool, Job{}, time.Second)
	}
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopCancel()
	if err := pool.Stop(stopCtx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if atomic.LoadInt64(&ran) != 4 {
		t.Fatalf("expected 4 jobs to run, got %d", ran)
	}
}

func TestWorkerPool_Stop_Deadline(t *testing.T) {
	t.Parallel()
	runner := runnerFunc(func(ctx context.Context, job Job) error {
		time.Sleep(time.Second)
		return nil
	})
	pool := NewWorkerPool(1, runner, quietLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)

	if !pool.Dispatch(Job{}) {
		t.Fatalf("dispatch failed")
	}
	// Wait until the job is active.
	deadline := time.Now().Add(time.Second)
	for pool.Active() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer stopCancel()
	err := pool.Stop(stopCtx)
	if err == nil {
		t.Fatalf("expected deadline error")
	}
	if err != context.DeadlineExceeded {
		t.Fatalf("expected DeadlineExceeded, got %v", err)
	}
}

func TestWorkerPool_DispatchAfterStop(t *testing.T) {
	t.Parallel()
	runner := runnerFunc(func(ctx context.Context, job Job) error { return nil })
	pool := NewWorkerPool(1, runner, quietLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)

	stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Second)
	defer stopCancel()
	if err := pool.Stop(stopCtx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if pool.Dispatch(Job{}) {
		t.Fatalf("dispatch after stop should fail")
	}
}

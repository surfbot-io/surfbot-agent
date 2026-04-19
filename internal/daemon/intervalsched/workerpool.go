package intervalsched

import (
	"context"
	"log/slog"
	"sync"
)

// Job is an opaque unit of work dispatched to the pool. The master
// ticker fills in Payload; the pool is scheduler-agnostic.
type Job struct {
	ScheduleID string
	TargetID   string
	Payload    any
}

// JobRunner is the behavior a worker invokes for every job it pulls off
// the queue. The returned error is logged by the pool and does not stop
// the worker.
type JobRunner interface {
	Run(ctx context.Context, job Job) error
}

// WorkerPool is a bounded goroutine pool with a non-blocking dispatch
// queue. Resize grows immediately and shrinks by letting excess workers
// exit after their current job completes — in-flight jobs are never
// canceled by a resize.
//
// The job queue buffer is set at construction to the initial size and
// never shrinks. Resize changes the number of worker goroutines only;
// bounded concurrency is enforced by worker count, not buffer capacity.
type WorkerPool struct {
	runner JobRunner
	log    *slog.Logger

	mu      sync.Mutex
	size    int
	active  int
	jobs    chan Job
	started bool
	stopped bool
	ctx     context.Context
	wg      sync.WaitGroup
	workers int
	// excessQuit carries shrink tokens. A worker that observes a token
	// here exits after its current job completes.
	excessQuit chan struct{}
}

// NewWorkerPool constructs a pool with `size` worker slots. Non-positive
// size is normalized to 1. Start must be called before Dispatch.
func NewWorkerPool(size int, runner JobRunner, log *slog.Logger) *WorkerPool {
	if size <= 0 {
		size = 1
	}
	if log == nil {
		log = slog.Default()
	}
	return &WorkerPool{
		runner:     runner,
		log:        log,
		size:       size,
		jobs:       make(chan Job, size),
		excessQuit: make(chan struct{}, 1024),
	}
}

// Start spawns `size` worker goroutines. Re-entrant: a second call is a
// no-op.
func (p *WorkerPool) Start(ctx context.Context) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.started {
		return
	}
	p.started = true
	p.ctx = ctx
	for i := 0; i < p.size; i++ {
		p.spawnWorkerLocked()
	}
}

func (p *WorkerPool) spawnWorkerLocked() {
	p.workers++
	p.wg.Add(1)
	go p.workerLoop()
}

func (p *WorkerPool) workerLoop() {
	defer p.wg.Done()
	defer func() {
		p.mu.Lock()
		p.workers--
		p.mu.Unlock()
	}()
	for {
		select {
		case <-p.ctx.Done():
			return
		case job, ok := <-p.jobs:
			if !ok {
				return
			}
			p.mu.Lock()
			p.active++
			p.mu.Unlock()

			if err := p.runner.Run(p.ctx, job); err != nil {
				p.log.Error("worker job failed",
					"schedule_id", job.ScheduleID,
					"target_id", job.TargetID,
					"error", err)
			}

			p.mu.Lock()
			p.active--
			p.mu.Unlock()

			// Cooperative shrink: after each job, see whether we've been
			// asked to step down.
			select {
			case <-p.excessQuit:
				return
			default:
			}
		}
	}
}

// Free returns the number of idle worker slots (size - active).
func (p *WorkerPool) Free() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	free := p.size - p.active
	if free < 0 {
		return 0
	}
	return free
}

// Active returns the number of workers currently executing a job.
func (p *WorkerPool) Active() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.active
}

// Dispatch enqueues a job. Non-blocking: returns false when the queue
// is full.
func (p *WorkerPool) Dispatch(job Job) bool {
	p.mu.Lock()
	if p.stopped {
		p.mu.Unlock()
		return false
	}
	ch := p.jobs
	p.mu.Unlock()
	select {
	case ch <- job:
		return true
	default:
		return false
	}
}

// Resize changes the number of workers. Grow spawns new goroutines
// immediately. Shrink queues excess-exit tokens; excess workers exit
// after their current job completes. In-flight jobs are never canceled.
func (p *WorkerPool) Resize(newSize int) {
	if newSize <= 0 {
		newSize = 1
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if newSize == p.size {
		return
	}
	if newSize > p.size {
		toAdd := newSize - p.size
		p.size = newSize
		if p.started && !p.stopped {
			for i := 0; i < toAdd; i++ {
				p.spawnWorkerLocked()
			}
		}
		return
	}
	toRemove := p.size - newSize
	p.size = newSize
	for i := 0; i < toRemove; i++ {
		select {
		case p.excessQuit <- struct{}{}:
		default:
		}
	}
}

// Stop closes the job queue and waits for workers to drain remaining
// jobs. Returns ctx.Err() if the deadline fires before all workers exit.
func (p *WorkerPool) Stop(ctx context.Context) error {
	p.mu.Lock()
	if p.stopped {
		p.mu.Unlock()
		return nil
	}
	p.stopped = true
	close(p.jobs)
	p.mu.Unlock()

	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

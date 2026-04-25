package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/surfbot-io/surfbot-agent/internal/storage"
)

// schedulerLockStaleAfter is how long the heartbeat may be silent
// before a second process treats the lock as reclaimable. Default is
// 2× the state-file heartbeat (30s) — fast enough that a crashed
// daemon does not block the next `surfbot ui` for long, slow enough
// to absorb transient pauses (e.g. laptop sleep).
const schedulerLockStaleAfter = 60 * time.Second

// schedulerLockRefreshInterval is the cadence at which the active
// holder bumps heartbeat_at. Stays well under staleAfter so a healthy
// holder is never reclaimed by mistake.
const schedulerLockRefreshInterval = 15 * time.Second

// sameHostPIDAlive probes whether a PID is still live on this host.
// Cross-host reclaim is out of scope per the product model; we rely on
// the stale-heartbeat timer for remote-host edge cases.
//
// Implementation lives in scheduler_lock_{unix,windows}.go so each OS
// can use the right liveness primitive (Signal(0) on POSIX, FindProcess
// truth on Windows). Overridable for tests.
var sameHostPIDAlive = sameHostPIDAliveOS

// schedulerLockHandle wraps an active scheduler_lock ownership. Its
// Close method releases the lock and stops the heartbeat goroutine.
type schedulerLockHandle struct {
	store    *storage.SQLiteStore
	pid      int
	hostname string
	stop     context.CancelFunc
	wg       sync.WaitGroup
}

// acquireSchedulerLock takes the scheduler_lock row for this process,
// reclaiming a stale or same-host-dead holder if necessary. On
// success, a heartbeat goroutine is launched; stop it via Close.
//
// ErrLockHeld (with the holder's identity) is returned when the lock
// is owned by a live process on this host or by a remote host whose
// heartbeat is still fresh.
func acquireSchedulerLock(ctx context.Context, store *storage.SQLiteStore) (*schedulerLockHandle, error) {
	host, _ := os.Hostname()
	pid := os.Getpid()
	now := time.Now()

	lock, err := store.AcquireSchedulerLock(ctx, pid, host, schedulerLockStaleAfter)
	if err == nil {
		return startLockHeartbeat(store, pid, host), nil
	}
	if !errors.Is(err, storage.ErrLockHeld) {
		return nil, err
	}
	// Lock held: maybe it's us from a previous crash (same host, dead PID)?
	sameHost := lock.Hostname == host
	if sameHost && !sameHostPIDAlive(lock.PID) {
		slog.Warn("reclaiming stale scheduler_lock from dead PID",
			"prev_pid", lock.PID, "prev_host", lock.Hostname,
			"age", lock.Age(now).Round(time.Second))
		// Force-take by overwriting with a staleAfter=0 retry.
		lock2, rerr := store.AcquireSchedulerLock(ctx, pid, host, 0)
		if rerr == nil {
			return startLockHeartbeat(store, pid, host), nil
		}
		if !errors.Is(rerr, storage.ErrLockHeld) {
			return nil, rerr
		}
		// Someone else raced us to the reclaim — fall through and
		// surface the current holder (lock2) back to the caller.
		lock = lock2
	}
	return nil, fmt.Errorf("%w: pid=%d host=%s age=%s",
		storage.ErrLockHeld, lock.PID, lock.Hostname, lock.Age(now).Round(time.Second))
}

// startLockHeartbeat launches the goroutine that keeps the lock's
// heartbeat_at fresh. The returned handle releases the lock and stops
// the goroutine on Close.
func startLockHeartbeat(store *storage.SQLiteStore, pid int, host string) *schedulerLockHandle {
	runCtx, cancel := context.WithCancel(context.Background())
	h := &schedulerLockHandle{
		store:    store,
		pid:      pid,
		hostname: host,
		stop:     cancel,
	}
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		t := time.NewTicker(schedulerLockRefreshInterval)
		defer t.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-t.C:
				if err := store.RefreshSchedulerLock(runCtx, pid, host); err != nil {
					slog.Warn("scheduler_lock heartbeat failed", "err", err)
				}
			}
		}
	}()
	return h
}

// Close stops the heartbeat goroutine and releases the lock row. Safe
// to call multiple times.
func (h *schedulerLockHandle) Close(ctx context.Context) error {
	if h == nil || h.stop == nil {
		return nil
	}
	h.stop()
	h.stop = nil
	h.wg.Wait()
	return h.store.ReleaseSchedulerLock(ctx, h.pid, h.hostname)
}

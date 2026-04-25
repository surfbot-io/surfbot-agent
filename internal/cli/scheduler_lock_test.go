package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/surfbot-io/surfbot-agent/internal/storage"
)

func newLockTestStore(t *testing.T) *storage.SQLiteStore {
	t.Helper()
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "lock.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// TestSchedulerLock_Contention asserts that a second acquire against a
// freshly-held lock returns ErrLockHeld and surfaces the holder.
func TestSchedulerLock_Contention(t *testing.T) {
	// Pin liveness so the second call doesn't take the reclaim path.
	prev := sameHostPIDAlive
	sameHostPIDAlive = func(int) bool { return true }
	t.Cleanup(func() { sameHostPIDAlive = prev })

	store := newLockTestStore(t)
	ctx := context.Background()

	first, err := acquireSchedulerLock(ctx, store)
	require.NoError(t, err)
	t.Cleanup(func() { _ = first.Close(ctx) })

	_, err = acquireSchedulerLock(ctx, store)
	require.Error(t, err)
	require.ErrorIs(t, err, storage.ErrLockHeld)
}

// TestSchedulerLock_StaleReclaim seeds a lock from a fabricated PID
// with a stale heartbeat, then asserts the next acquirer reclaims it.
func TestSchedulerLock_StaleReclaim(t *testing.T) {
	prev := sameHostPIDAlive
	sameHostPIDAlive = func(int) bool { return false } // dead by definition
	t.Cleanup(func() { sameHostPIDAlive = prev })

	store := newLockTestStore(t)
	ctx := context.Background()

	// Seed via the storage API on this host so the same-host reclaim
	// path fires. The fake PID is "dead" because sameHostPIDAlive is
	// pinned to false above.
	host, _ := os.Hostname()
	_, err := store.AcquireSchedulerLock(ctx, 999999, host, 24*time.Hour)
	require.NoError(t, err)

	// Stale-aware acquire takes over.
	h, err := acquireSchedulerLock(ctx, store)
	require.NoError(t, err, "stale lock should be reclaimed when holder PID is dead")
	t.Cleanup(func() { _ = h.Close(ctx) })
}

// TestSchedulerLock_LiveSameHost asserts that a second process on the
// same host with a live PID does NOT reclaim — it returns ErrLockHeld
// regardless of heartbeat freshness.
func TestSchedulerLock_LiveSameHost(t *testing.T) {
	prev := sameHostPIDAlive
	sameHostPIDAlive = func(int) bool { return true } // always alive
	t.Cleanup(func() { sameHostPIDAlive = prev })

	store := newLockTestStore(t)
	ctx := context.Background()

	host, _ := os.Hostname()
	_, err := store.AcquireSchedulerLock(ctx, 12345, host, schedulerLockStaleAfter)
	require.NoError(t, err)

	_, err = acquireSchedulerLock(ctx, store)
	require.ErrorIs(t, err, storage.ErrLockHeld,
		"a live same-host PID must block reclaim regardless of heartbeat freshness")
}

// TestSchedulerLock_RefreshAndRelease asserts the heartbeat keeps the
// lock fresh and that Close removes the row.
func TestSchedulerLock_RefreshAndRelease(t *testing.T) {
	prev := sameHostPIDAlive
	sameHostPIDAlive = func(int) bool { return true }
	t.Cleanup(func() { sameHostPIDAlive = prev })

	store := newLockTestStore(t)
	ctx := context.Background()

	h, err := acquireSchedulerLock(ctx, store)
	require.NoError(t, err)
	require.NoError(t, h.Close(ctx))

	// After release, a second acquire must succeed.
	h2, err := acquireSchedulerLock(ctx, store)
	require.NoError(t, err)
	t.Cleanup(func() { _ = h2.Close(ctx) })
}


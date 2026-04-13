package daemon

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// blockingScheduler ignores ctx cancellation entirely. Used to verify
// Stop()'s grace timeout fires correctly.
type blockingScheduler struct{}

func (blockingScheduler) Next() time.Time { return time.Now().Add(time.Hour) }
func (blockingScheduler) Run(_ context.Context) error {
	time.Sleep(10 * time.Second)
	return nil
}

func newTestRunner(t *testing.T, sched Scheduler) *Runner {
	t.Helper()
	dir := t.TempDir()
	store := NewStateStore(filepath.Join(dir, "state.json"))
	logger := NewLogger(filepath.Join(dir, "test.log"), LoggerOptions{MaxSizeMB: 1})
	t.Cleanup(func() { _ = logger.Close() })
	return NewRunner(RunnerConfig{
		Scheduler: sched,
		State:     store,
		Logger:    logger,
		Heartbeat: 50 * time.Millisecond,
		Version:   "test",
	})
}

func TestRunner_StartStop(t *testing.T) {
	r := newTestRunner(t, NewNoopScheduler())
	require.NoError(t, r.Start())

	// Give the heartbeat a couple of ticks.
	time.Sleep(150 * time.Millisecond)

	require.NoError(t, r.Stop(2*time.Second))

	st, err := r.state.Load()
	require.NoError(t, err)
	require.Equal(t, "test", st.Version)
	require.NotZero(t, st.PID)
	require.False(t, st.StartedAt.IsZero())
	require.False(t, st.NextScanAt.IsZero())
}

func TestRunner_ShutdownTimeout(t *testing.T) {
	r := newTestRunner(t, blockingScheduler{})
	require.NoError(t, r.Start())

	err := r.Stop(100 * time.Millisecond)
	require.ErrorIs(t, err, ErrShutdownTimeout)
}

func TestRunner_StartedAtFreshOnRestart(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")
	stateStore := NewStateStore(statePath)

	// Seed an old state file with a stale StartedAt.
	oldStart := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, stateStore.Save(State{
		Version:   "old",
		PID:       99999,
		StartedAt: oldStart,
		WrittenAt: oldStart,
	}))

	logger := NewLogger(filepath.Join(dir, "test.log"), LoggerOptions{MaxSizeMB: 1})
	t.Cleanup(func() { _ = logger.Close() })

	r := NewRunner(RunnerConfig{
		Scheduler: NewNoopScheduler(),
		State:     stateStore,
		Logger:    logger,
		Heartbeat: 50 * time.Millisecond,
		Version:   "new",
	})

	before := time.Now().UTC()
	require.NoError(t, r.Start())
	time.Sleep(100 * time.Millisecond)
	require.NoError(t, r.Stop(2*time.Second))

	st, err := stateStore.Load()
	require.NoError(t, err)
	require.Equal(t, "new", st.Version)
	require.True(t, st.StartedAt.After(oldStart), "StartedAt should be newer than old state")
	require.True(t, !st.StartedAt.Before(before), "StartedAt should be at or after runner start")
}

func TestRunner_DoubleStart(t *testing.T) {
	r := newTestRunner(t, NewNoopScheduler())
	require.NoError(t, r.Start())
	defer r.Stop(time.Second) //nolint:errcheck

	err := r.Start()
	require.Error(t, err)
}

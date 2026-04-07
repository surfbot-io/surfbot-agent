package intervalsched

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestScheduleStateStore_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	store := NewScheduleStateStore(filepath.Join(dir, "schedule.state.json"))

	got, err := store.Load()
	require.NoError(t, err)
	require.True(t, got.LastFullAt.IsZero())

	now := time.Now().UTC().Truncate(time.Second)
	want := ScheduleState{
		LastFullAt:      now,
		LastFullStatus:  "ok",
		LastQuickAt:     now.Add(-time.Hour),
		LastQuickStatus: "failed",
		LastQuickError:  "boom",
		NextFullAt:      now.Add(24 * time.Hour),
		NextQuickAt:     now.Add(time.Hour),
	}
	require.NoError(t, store.Save(want))

	got, err = store.Load()
	require.NoError(t, err)
	require.True(t, want.LastFullAt.Equal(got.LastFullAt))
	require.Equal(t, want.LastFullStatus, got.LastFullStatus)
	require.Equal(t, want.LastQuickStatus, got.LastQuickStatus)
	require.Equal(t, want.LastQuickError, got.LastQuickError)
}

func TestScheduleStateStore_StaleTmpDoesNotCorrupt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "schedule.state.json")
	// Pre-existing tmp from a previous crashed write must not break Load.
	require.NoError(t, os.WriteFile(path+".tmp", []byte("garbage"), 0o600))

	store := NewScheduleStateStore(path)
	st, err := store.Load()
	require.NoError(t, err)
	require.True(t, st.LastFullAt.IsZero())

	require.NoError(t, store.Save(ScheduleState{LastFullStatus: "ok"}))
	st, err = store.Load()
	require.NoError(t, err)
	require.Equal(t, "ok", st.LastFullStatus)
}

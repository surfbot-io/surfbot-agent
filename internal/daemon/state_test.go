package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStateStore_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	store := NewStateStore(filepath.Join(dir, "daemon.state.json"))

	// Loading a missing file returns zero state, no error.
	st, err := store.Load()
	require.NoError(t, err)
	require.Zero(t, st.PID)

	now := time.Now().UTC().Truncate(time.Second)
	want := State{
		Version:    "1.2.3",
		PID:        4242,
		StartedAt:  now,
		NextScanAt: now.Add(24 * time.Hour),
	}
	require.NoError(t, store.Save(want))

	got, err := store.Load()
	require.NoError(t, err)
	require.Equal(t, want.Version, got.Version)
	require.Equal(t, want.PID, got.PID)
	require.True(t, want.StartedAt.Equal(got.StartedAt))
	require.True(t, want.NextScanAt.Equal(got.NextScanAt))
}

func TestStateStore_AtomicWrite_NoCorruption(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.state.json")
	store := NewStateStore(path)

	// Pre-create a stale .tmp file as if a previous write crashed.
	require.NoError(t, os.WriteFile(path+".tmp", []byte("garbage"), 0o600))

	require.NoError(t, store.Save(State{Version: "v", PID: 1}))

	got, err := store.Load()
	require.NoError(t, err)
	require.Equal(t, "v", got.Version)
	require.Equal(t, 1, got.PID)
}

func TestStateStore_Update(t *testing.T) {
	dir := t.TempDir()
	store := NewStateStore(filepath.Join(dir, "s.json"))

	require.NoError(t, store.Update(func(s *State) { s.PID = 7 }))
	require.NoError(t, store.Update(func(s *State) { s.Version = "abc" }))

	got, err := store.Load()
	require.NoError(t, err)
	require.Equal(t, 7, got.PID)
	require.Equal(t, "abc", got.Version)
}

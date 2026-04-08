package intervalsched

// SPEC-X3.1 §8 — trigger flag-file consumer tests. Drives processTrigger
// directly with a real filesystem (TempDir) to avoid the goroutine and
// fake-clock plumbing — the loop is a thin wrapper around it.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func writeTriggerFile(t *testing.T, dir string, tf triggerFile) {
	t.Helper()
	data, err := json.Marshal(tf)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "trigger.json"), data, 0o600))
}

func TestProcessTrigger_RunsScanAndUpdatesState(t *testing.T) {
	dir := t.TempDir()
	scanner := &recordingScanner{}
	store := NewScheduleStateStore(filepath.Join(dir, "schedule.state.json"))
	s := New(Config{FullInterval: time.Hour, TriggerDir: dir}, Options{
		Scanner: scanner, StateStore: store, RandSeed: 1,
	})

	writeTriggerFile(t, dir, triggerFile{
		ID: "tr_1", Profile: "quick", RequestedAt: time.Now().UTC(),
	})
	s.processTrigger(context.Background())

	if got := scanner.snapshot(); len(got) != 1 || got[0] != ProfileQuick {
		t.Fatalf("scanner calls: %v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "trigger.json")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("trigger.json should be gone, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "trigger.json.processing")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf(".processing should be gone, err=%v", err)
	}
	st, err := store.Load()
	require.NoError(t, err)
	require.NotNil(t, st.LastTrigger)
	require.Equal(t, "tr_1", st.LastTrigger.ID)
	require.Equal(t, "quick", st.LastTrigger.Profile)
	require.Equal(t, "ok", st.LastTrigger.Status)
	if st.LastQuickAt.IsZero() {
		t.Errorf("LastQuickAt not updated")
	}
}

func TestProcessTrigger_NoFileNoOp(t *testing.T) {
	dir := t.TempDir()
	scanner := &recordingScanner{}
	s := New(Config{FullInterval: time.Hour, TriggerDir: dir}, Options{Scanner: scanner, RandSeed: 1})
	s.processTrigger(context.Background())
	if len(scanner.snapshot()) != 0 {
		t.Errorf("scanner should not have run")
	}
}

func TestProcessTrigger_BypassesWindow(t *testing.T) {
	dir := t.TempDir()
	scanner := &recordingScanner{}
	loc := time.UTC
	s := New(Config{
		FullInterval: time.Hour,
		TriggerDir:   dir,
		Window: MaintenanceWindow{
			Enabled: true,
			Start:   TimeOfDay{Hour: 0},
			End:     TimeOfDay{Hour: 23, Minute: 59},
			Loc:     loc,
		},
	}, Options{Scanner: scanner, RandSeed: 1})

	writeTriggerFile(t, dir, triggerFile{ID: "tr_1", Profile: "full", RequestedAt: time.Now().UTC()})
	s.processTrigger(context.Background())
	if got := scanner.snapshot(); len(got) != 1 {
		t.Fatalf("trigger should bypass window, got %v", got)
	}
}

package storage

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/surfbot-io/surfbot-agent/internal/model"
)

func TestAdHocScanRunStore_CRUDRoundTrip(t *testing.T) {
	s := newTestStore(t)
	store := s.AdHocScanRuns()
	ctx := context.Background()
	tgt := seedTarget(t, s, "example.com")

	r := &model.AdHocScanRun{
		TargetID:    tgt.ID,
		InitiatedBy: "cli",
		Reason:      "zero-day verification",
		ToolConfig: model.ToolConfig{
			"nuclei": json.RawMessage(`{"tags":["cve-2025"]}`),
		},
		Status: model.AdHocPending,
	}
	require.NoError(t, store.Create(ctx, r))
	assert.NotEmpty(t, r.ID)

	got, err := store.Get(ctx, r.ID)
	require.NoError(t, err)
	assert.Equal(t, "cli", got.InitiatedBy)
	assert.Equal(t, model.AdHocPending, got.Status)
	assert.Equal(t, "zero-day verification", got.Reason)

	list, err := store.ListByTarget(ctx, tgt.ID, 10)
	require.NoError(t, err)
	assert.Len(t, list, 1)

	require.NoError(t, store.Delete(ctx, r.ID))
	_, err = store.Get(ctx, r.ID)
	assert.True(t, errors.Is(err, ErrNotFound))
}

func TestAdHocScanRunStore_StatusLifecycle(t *testing.T) {
	s := newTestStore(t)
	store := s.AdHocScanRuns()
	ctx := context.Background()
	tgt := seedTarget(t, s, "example.com")

	r := &model.AdHocScanRun{TargetID: tgt.ID, InitiatedBy: "api:token-1"}
	require.NoError(t, store.Create(ctx, r))

	started := time.Now().UTC().Truncate(time.Second)
	require.NoError(t, store.UpdateStatus(ctx, r.ID, model.AdHocRunning, started))
	got, err := store.Get(ctx, r.ID)
	require.NoError(t, err)
	assert.Equal(t, model.AdHocRunning, got.Status)
	require.NotNil(t, got.StartedAt)
	assert.WithinDuration(t, started, *got.StartedAt, time.Second)

	completed := started.Add(5 * time.Minute)
	require.NoError(t, store.UpdateStatus(ctx, r.ID, model.AdHocCompleted, completed))
	got, err = store.Get(ctx, r.ID)
	require.NoError(t, err)
	assert.Equal(t, model.AdHocCompleted, got.Status)
	require.NotNil(t, got.CompletedAt)
	assert.WithinDuration(t, completed, *got.CompletedAt, time.Second)
}

func TestAdHocScanRunStore_AttachScan(t *testing.T) {
	s := newTestStore(t)
	store := s.AdHocScanRuns()
	ctx := context.Background()
	tgt := seedTarget(t, s, "example.com")

	r := &model.AdHocScanRun{TargetID: tgt.ID, InitiatedBy: "cli"}
	require.NoError(t, store.Create(ctx, r))

	scan := &model.Scan{TargetID: tgt.ID, Type: model.ScanTypeFull, Status: model.ScanStatusQueued}
	require.NoError(t, s.CreateScan(ctx, scan))

	require.NoError(t, store.AttachScan(ctx, r.ID, scan.ID))

	got, err := store.Get(ctx, r.ID)
	require.NoError(t, err)
	require.NotNil(t, got.ScanID)
	assert.Equal(t, scan.ID, *got.ScanID)
}

func TestAdHocScanRunStore_ListByStatus(t *testing.T) {
	s := newTestStore(t)
	store := s.AdHocScanRuns()
	ctx := context.Background()
	tgt := seedTarget(t, s, "example.com")

	make := func(status model.AdHocRunStatus, initiator string) {
		r := &model.AdHocScanRun{TargetID: tgt.ID, InitiatedBy: initiator, Status: status}
		require.NoError(t, store.Create(ctx, r))
	}
	make(model.AdHocPending, "cli")
	make(model.AdHocRunning, "api:1")
	make(model.AdHocPending, "webui:alice")

	pending, err := store.ListByStatus(ctx, model.AdHocPending)
	require.NoError(t, err)
	assert.Len(t, pending, 2)
}

func TestAdHocScanRunStore_CascadeOnTargetDelete(t *testing.T) {
	s := newTestStore(t)
	store := s.AdHocScanRuns()
	ctx := context.Background()
	tgt := seedTarget(t, s, "example.com")

	r := &model.AdHocScanRun{TargetID: tgt.ID, InitiatedBy: "cli"}
	require.NoError(t, store.Create(ctx, r))
	require.NoError(t, s.DeleteTarget(ctx, tgt.ID))

	_, err := store.Get(ctx, r.ID)
	assert.True(t, errors.Is(err, ErrNotFound))
}

func TestAdHocScanRunStore_ValidatesUnknownTool(t *testing.T) {
	s := newTestStore(t)
	store := s.AdHocScanRuns()
	ctx := context.Background()
	tgt := seedTarget(t, s, "example.com")

	r := &model.AdHocScanRun{
		TargetID: tgt.ID, InitiatedBy: "cli",
		ToolConfig: model.ToolConfig{"amass": json.RawMessage(`{}`)},
	}
	err := store.Create(ctx, r)
	require.Error(t, err)
	assert.True(t, errors.Is(err, model.ErrUnknownTool))
}

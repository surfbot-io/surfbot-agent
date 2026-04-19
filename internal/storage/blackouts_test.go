package storage

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/surfbot-io/surfbot-agent/internal/model"
)

func TestBlackoutStore_CRUDRoundTrip(t *testing.T) {
	s := newTestStore(t)
	store := s.Blackouts()
	ctx := context.Background()

	b := &model.BlackoutWindow{
		Scope:       model.BlackoutScopeGlobal,
		Name:        "nightly-freeze",
		RRule:       "FREQ=DAILY;BYHOUR=2",
		DurationSec: 7200,
		Timezone:    "UTC",
		Enabled:     true,
	}
	require.NoError(t, store.Create(ctx, b))
	assert.NotEmpty(t, b.ID)

	got, err := store.Get(ctx, b.ID)
	require.NoError(t, err)
	assert.Equal(t, model.BlackoutScopeGlobal, got.Scope)
	assert.Equal(t, 7200, got.DurationSec)

	got.Enabled = false
	got.DurationSec = 3600
	require.NoError(t, store.Update(ctx, got))

	reread, err := store.Get(ctx, b.ID)
	require.NoError(t, err)
	assert.False(t, reread.Enabled)
	assert.Equal(t, 3600, reread.DurationSec)

	require.NoError(t, store.Delete(ctx, b.ID))
	_, err = store.Get(ctx, b.ID)
	assert.True(t, errors.Is(err, ErrNotFound))
}

func TestBlackoutStore_ScopeConstraints(t *testing.T) {
	s := newTestStore(t)
	store := s.Blackouts()
	ctx := context.Background()
	tgt := seedTarget(t, s, "example.com")

	// Global with target_id must fail validation.
	badGlobal := &model.BlackoutWindow{
		Scope:       model.BlackoutScopeGlobal,
		TargetID:    &tgt.ID,
		Name:        "bad",
		RRule:       "FREQ=DAILY",
		DurationSec: 60,
		Timezone:    "UTC",
		Enabled:     true,
	}
	require.Error(t, store.Create(ctx, badGlobal))

	// Target without target_id must fail validation.
	badTarget := &model.BlackoutWindow{
		Scope:       model.BlackoutScopeTarget,
		Name:        "bad",
		RRule:       "FREQ=DAILY",
		DurationSec: 60,
		Timezone:    "UTC",
		Enabled:     true,
	}
	require.Error(t, store.Create(ctx, badTarget))
}

func TestBlackoutStore_ListActive(t *testing.T) {
	s := newTestStore(t)
	store := s.Blackouts()
	ctx := context.Background()
	tgtA := seedTarget(t, s, "a.example")
	tgtB := seedTarget(t, s, "b.example")

	mk := func(scope model.BlackoutScope, targetID *string, enabled bool, name string) *model.BlackoutWindow {
		return &model.BlackoutWindow{
			Scope: scope, TargetID: targetID, Name: name,
			RRule: "FREQ=DAILY;BYHOUR=2", DurationSec: 3600, Timezone: "UTC",
			Enabled: enabled,
		}
	}
	require.NoError(t, store.Create(ctx, mk(model.BlackoutScopeGlobal, nil, true, "global-on")))
	require.NoError(t, store.Create(ctx, mk(model.BlackoutScopeGlobal, nil, false, "global-off")))
	require.NoError(t, store.Create(ctx, mk(model.BlackoutScopeTarget, &tgtA.ID, true, "a-on")))
	require.NoError(t, store.Create(ctx, mk(model.BlackoutScopeTarget, &tgtB.ID, true, "b-on")))

	active, err := store.ListActive(ctx, tgtA.ID)
	require.NoError(t, err)
	names := make([]string, 0, len(active))
	for _, bl := range active {
		names = append(names, bl.Name)
	}
	assert.ElementsMatch(t, []string{"global-on", "a-on"}, names)
}

func TestBlackoutStore_CascadeOnTargetDelete(t *testing.T) {
	s := newTestStore(t)
	store := s.Blackouts()
	ctx := context.Background()
	tgt := seedTarget(t, s, "example.com")

	b := &model.BlackoutWindow{
		Scope: model.BlackoutScopeTarget, TargetID: &tgt.ID, Name: "x",
		RRule: "FREQ=DAILY", DurationSec: 60, Timezone: "UTC", Enabled: true,
	}
	require.NoError(t, store.Create(ctx, b))
	require.NoError(t, s.DeleteTarget(ctx, tgt.ID))
	_, err := store.Get(ctx, b.ID)
	assert.True(t, errors.Is(err, ErrNotFound))
}

func TestBlackoutStore_ValidatesRRule(t *testing.T) {
	s := newTestStore(t)
	store := s.Blackouts()
	ctx := context.Background()

	bad := &model.BlackoutWindow{
		Scope: model.BlackoutScopeGlobal, Name: "x",
		RRule: "FREQ=SECONDLY", DurationSec: 60, Timezone: "UTC", Enabled: true,
	}
	err := store.Create(ctx, bad)
	require.Error(t, err)
}

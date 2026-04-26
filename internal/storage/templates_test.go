package storage

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/surfbot-io/surfbot-agent/internal/model"
)

func newTemplate(name string) *model.Template {
	return &model.Template{
		Name:        name,
		Description: "desc",
		RRule:       "FREQ=DAILY;BYHOUR=2",
		Timezone:    "UTC",
		ToolConfig: model.ToolConfig{
			"nuclei": json.RawMessage(`{"severity":["critical","high"]}`),
		},
		MaintenanceWindow: &model.MaintenanceWindow{
			RRule:       "FREQ=DAILY;BYHOUR=2",
			DurationSec: 3600,
			Timezone:    "UTC",
		},
	}
}

func TestTemplateStore_CRUDRoundTrip(t *testing.T) {
	s := newTestStore(t)
	store := s.Templates()
	ctx := context.Background()

	tmpl := newTemplate("prod-critical")
	require.NoError(t, store.Create(ctx, tmpl))
	assert.NotEmpty(t, tmpl.ID)

	got, err := store.Get(ctx, tmpl.ID)
	require.NoError(t, err)
	assert.Equal(t, "prod-critical", got.Name)
	assert.Equal(t, "FREQ=DAILY;BYHOUR=2", got.RRule)
	require.NotNil(t, got.MaintenanceWindow)
	assert.Equal(t, 3600, got.MaintenanceWindow.DurationSec)
	assert.Contains(t, got.ToolConfig, "nuclei")

	byName, err := store.GetByName(ctx, "prod-critical")
	require.NoError(t, err)
	assert.Equal(t, tmpl.ID, byName.ID)

	got.Description = "updated"
	got.RRule = "FREQ=HOURLY"
	require.NoError(t, store.Update(ctx, got))

	reread, err := store.Get(ctx, tmpl.ID)
	require.NoError(t, err)
	assert.Equal(t, "updated", reread.Description)
	assert.Equal(t, "FREQ=HOURLY", reread.RRule)

	list, err := store.List(ctx)
	require.NoError(t, err)
	assert.Len(t, list, 1)

	require.NoError(t, store.Delete(ctx, tmpl.ID))
	_, err = store.Get(ctx, tmpl.ID)
	assert.True(t, errors.Is(err, ErrNotFound))
}

func TestTemplateStore_UniqueName(t *testing.T) {
	s := newTestStore(t)
	store := s.Templates()
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, newTemplate("prod-critical")))
	err := store.Create(ctx, newTemplate("prod-critical"))
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrAlreadyExists))
}

func TestTemplateStore_DeleteRefusesSystem(t *testing.T) {
	s := newTestStore(t)
	store := s.Templates()
	ctx := context.Background()

	tmpl := newTemplate("builtin")
	tmpl.IsSystem = true
	require.NoError(t, store.Create(ctx, tmpl))

	err := store.Delete(ctx, tmpl.ID)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrSystemTemplateImmutable),
		"want ErrSystemTemplateImmutable, got %v", err)

	// Row must still be present after the refused delete.
	got, err := store.Get(ctx, tmpl.ID)
	require.NoError(t, err)
	assert.True(t, got.IsSystem)
}

func TestTemplateStore_DeleteUnknownReturnsNotFound(t *testing.T) {
	s := newTestStore(t)
	store := s.Templates()
	ctx := context.Background()

	err := store.Delete(ctx, "no-such-id")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNotFound))
}

func TestTemplateStore_ValidatesOnCreate(t *testing.T) {
	s := newTestStore(t)
	store := s.Templates()
	ctx := context.Background()

	bad := newTemplate("bad")
	bad.RRule = "FREQ=SECONDLY"
	err := store.Create(ctx, bad)
	require.Error(t, err)

	unknown := newTemplate("unknown-tool")
	unknown.ToolConfig = model.ToolConfig{"amass": json.RawMessage(`{}`)}
	err = store.Create(ctx, unknown)
	require.Error(t, err)
	assert.True(t, errors.Is(err, model.ErrUnknownTool))
}

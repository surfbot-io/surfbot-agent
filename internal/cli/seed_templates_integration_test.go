package cli

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/surfbot-io/surfbot-agent/internal/daemon"
	"github.com/surfbot-io/surfbot-agent/internal/storage"
)

// TestSeedBuiltinTemplates_FullPathThroughRealStore drives the same seed
// call BuildSchedulerBootstrap makes, against a real *storage.SQLiteStore.
// Verifies the three builtin rows materialize with is_system=1 and
// names matching the catalog.
func TestSeedBuiltinTemplates_FullPathThroughRealStore(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "seed.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	ctx := context.Background()

	report, err := daemon.SeedBuiltinTemplates(ctx, store, slog.Default())
	require.NoError(t, err)
	assert.Equal(t, len(daemon.BuiltinTemplates), report.Created)
	assert.Equal(t, 0, report.AlreadyPresent)
	assert.True(t, report.Duration > 0)

	list, err := store.Templates().List(ctx)
	require.NoError(t, err)
	require.Len(t, list, len(daemon.BuiltinTemplates))

	byName := map[string]bool{}
	for _, t := range list {
		byName[t.Name] = t.IsSystem
	}
	for _, want := range []string{"Default", "Fast", "Deep"} {
		isSystem, ok := byName[want]
		require.True(t, ok, "builtin %q missing", want)
		assert.True(t, isSystem, "builtin %q must have is_system=1", want)
	}
}

// TestSeedBuiltinTemplates_BootIdempotent verifies a second seed call
// against the same DB inserts nothing — the production hook can fire
// safely on every boot.
func TestSeedBuiltinTemplates_BootIdempotent(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "seed.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	ctx := context.Background()

	r1, err := daemon.SeedBuiltinTemplates(ctx, store, slog.Default())
	require.NoError(t, err)
	require.Equal(t, len(daemon.BuiltinTemplates), r1.Created)

	r2, err := daemon.SeedBuiltinTemplates(ctx, store, slog.Default())
	require.NoError(t, err)
	assert.Zero(t, r2.Created, "second pass must not insert anything")
	assert.Equal(t, len(daemon.BuiltinTemplates), r2.AlreadyPresent)

	list, err := store.Templates().List(ctx)
	require.NoError(t, err)
	assert.Len(t, list, len(daemon.BuiltinTemplates), "no duplicates allowed")
}

// TestSeedBuiltinTemplates_BlocksDeleteOnSeededRows is the storage-gate
// integration check from the SCHED2.3 R3 acceptance: after seeding via
// the production path, the resulting builtin rows must refuse Delete.
func TestSeedBuiltinTemplates_BlocksDeleteOnSeededRows(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "seed.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	ctx := context.Background()
	_, err = daemon.SeedBuiltinTemplates(ctx, store, slog.Default())
	require.NoError(t, err)

	tmpl, err := store.Templates().GetByName(ctx, "Default")
	require.NoError(t, err)

	err = store.Templates().Delete(ctx, tmpl.ID)
	require.Error(t, err)
	assert.ErrorIs(t, err, storage.ErrSystemTemplateImmutable)
}

package daemon

import (
	"context"
	"encoding/json"
	"log/slog"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/surfbot-io/surfbot-agent/internal/model"
	"github.com/surfbot-io/surfbot-agent/internal/rrule"
	"github.com/surfbot-io/surfbot-agent/internal/storage"
)

func newSeedTestStore(t *testing.T) *storage.SQLiteStore {
	t.Helper()
	s, err := storage.NewSQLiteStore(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// TestBuiltinTemplates_RRulesPassValidator catches a mistyped builtin
// RRULE before it ever reaches the seed transaction. SPEC-SCHED2.3 OQ2
// flagged that BYHOUR=*/6 isn't accepted by the underlying rrule
// library; this test enforces the resolution.
func TestBuiltinTemplates_RRulesPassValidator(t *testing.T) {
	for _, b := range BuiltinTemplates {
		t.Run(b.Name, func(t *testing.T) {
			_, err := rrule.ValidateRRule(b.RRule)
			require.NoError(t, err, "builtin %q has invalid RRULE %q", b.Name, b.RRule)
		})
	}
}

// TestBuiltinTemplates_ToolConfigsValid catches an unknown tool key or
// malformed params struct in the catalog. ValidateToolConfig is the same
// gate the storage Create path runs.
func TestBuiltinTemplates_ToolConfigsValid(t *testing.T) {
	for _, b := range BuiltinTemplates {
		t.Run(b.Name, func(t *testing.T) {
			require.NoError(t, model.ValidateToolConfig(b.ToolConfig),
				"builtin %q tool_config rejected", b.Name)
		})
	}
}

func TestSeedBuiltinTemplates_FreshStore(t *testing.T) {
	s := newSeedTestStore(t)
	ctx := context.Background()

	report, err := SeedBuiltinTemplates(ctx, s, slog.Default())
	require.NoError(t, err)
	assert.Equal(t, len(BuiltinTemplates), report.Created)
	assert.Equal(t, 0, report.AlreadyPresent)
	assert.ElementsMatch(t, []string{"Default", "Fast", "Deep"}, report.Names)

	list, err := s.Templates().List(ctx)
	require.NoError(t, err)
	require.Len(t, list, len(BuiltinTemplates))

	gotNames := make([]string, 0, len(list))
	for _, tmpl := range list {
		gotNames = append(gotNames, tmpl.Name)
		assert.True(t, tmpl.IsSystem, "builtin %q must have is_system=1", tmpl.Name)
		assert.NotEmpty(t, tmpl.ID)
		assert.NotEmpty(t, tmpl.Description)
	}
	sort.Strings(gotNames)
	assert.Equal(t, []string{"Deep", "Default", "Fast"}, gotNames)
}

func TestSeedBuiltinTemplates_Idempotent(t *testing.T) {
	s := newSeedTestStore(t)
	ctx := context.Background()

	first, err := SeedBuiltinTemplates(ctx, s, slog.Default())
	require.NoError(t, err)
	require.Equal(t, len(BuiltinTemplates), first.Created)

	second, err := SeedBuiltinTemplates(ctx, s, slog.Default())
	require.NoError(t, err)
	assert.Equal(t, 0, second.Created)
	assert.Equal(t, len(BuiltinTemplates), second.AlreadyPresent)

	list, err := s.Templates().List(ctx)
	require.NoError(t, err)
	assert.Len(t, list, len(BuiltinTemplates), "second seed must not duplicate rows")
}

// TestSeedBuiltinTemplates_NameCollisionPreservesOperatorRow covers
// SPEC-SCHED2.3 OQ4: a non-system template with the same name as a
// builtin must survive the seed untouched.
func TestSeedBuiltinTemplates_NameCollisionPreservesOperatorRow(t *testing.T) {
	s := newSeedTestStore(t)
	ctx := context.Background()

	operatorTmpl := &model.Template{
		Name:        "Default",
		Description: "operator-managed default",
		RRule:       "FREQ=HOURLY",
		Timezone:    "UTC",
		ToolConfig: model.ToolConfig{
			"nuclei": json.RawMessage(`{"severity":["info"]}`),
		},
		IsSystem: false,
	}
	require.NoError(t, s.Templates().Create(ctx, operatorTmpl))
	operatorID := operatorTmpl.ID

	report, err := SeedBuiltinTemplates(ctx, s, slog.Default())
	require.NoError(t, err)
	assert.GreaterOrEqual(t, report.AlreadyPresent, 1)

	got, err := s.Templates().GetByName(ctx, "Default")
	require.NoError(t, err)
	assert.Equal(t, operatorID, got.ID, "operator row must not be replaced")
	assert.False(t, got.IsSystem, "operator row must not be flipped to system")
	assert.Equal(t, "operator-managed default", got.Description)

	// The other two builtins must still be seeded normally.
	for _, name := range []string{"Fast", "Deep"} {
		t.Run(name, func(t *testing.T) {
			tmpl, err := s.Templates().GetByName(ctx, name)
			require.NoError(t, err)
			assert.True(t, tmpl.IsSystem)
		})
	}
}

// TestSeedBuiltinTemplates_RoundTripsToolConfig checks that the deep
// copy in cloneToolConfig and the storage Marshal/Unmarshal preserve
// the catalog-level ToolConfig byte-for-byte.
func TestSeedBuiltinTemplates_RoundTripsToolConfig(t *testing.T) {
	s := newSeedTestStore(t)
	ctx := context.Background()

	_, err := SeedBuiltinTemplates(ctx, s, slog.Default())
	require.NoError(t, err)

	for _, b := range BuiltinTemplates {
		t.Run(b.Name, func(t *testing.T) {
			got, err := s.Templates().GetByName(ctx, b.Name)
			require.NoError(t, err)
			require.Equal(t, len(b.ToolConfig), len(got.ToolConfig))
			for tool, want := range b.ToolConfig {
				gotRaw, ok := got.ToolConfig[tool]
				require.True(t, ok, "tool %q missing from persisted row", tool)
				assert.JSONEq(t, string(want), string(gotRaw))
			}
		})
	}
}

package cli

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/surfbot-io/surfbot-agent/internal/model"
	"github.com/surfbot-io/surfbot-agent/internal/storage"
)

func newTestStore(t *testing.T) *storage.SQLiteStore {
	t.Helper()
	s, err := storage.NewSQLiteStore(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	return s
}

func TestParseScanType(t *testing.T) {
	tests := []struct {
		input string
		want  model.ScanType
	}{
		{"full", model.ScanTypeFull},
		{"quick", model.ScanTypeQuick},
		{"discovery", model.ScanTypeDiscovery},
		{"", model.ScanTypeFull},
		{"unknown", model.ScanTypeFull},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, parseScanType(tc.input), "input: %q", tc.input)
	}
}

func TestAutoCreateTarget(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Auto-create when target doesn't exist
	target, err := autoCreateTarget(ctx, s, "example.com")
	require.NoError(t, err)
	assert.NotEmpty(t, target.ID)
	assert.Equal(t, "example.com", target.Value)
	assert.Equal(t, model.TargetTypeDomain, target.Type)

	// Reuse existing target
	target2, err := autoCreateTarget(ctx, s, "example.com")
	require.NoError(t, err)
	assert.Equal(t, target.ID, target2.ID)

	// Create a different target
	target3, err := autoCreateTarget(ctx, s, "other.com")
	require.NoError(t, err)
	assert.NotEqual(t, target.ID, target3.ID)
	assert.Equal(t, "other.com", target3.Value)
}

func TestAutoCreateTargetIP(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	target, err := autoCreateTarget(ctx, s, "192.168.1.1")
	require.NoError(t, err)
	assert.Equal(t, model.TargetTypeIP, target.Type)
}

func TestScanCommandFlags(t *testing.T) {
	// Verify the scan command has the expected flags
	cmd := scanCmd

	assert.NotNil(t, cmd.Flags().Lookup("type"))
	assert.NotNil(t, cmd.Flags().Lookup("tools"))
	assert.NotNil(t, cmd.Flags().Lookup("rate-limit"))
	assert.NotNil(t, cmd.Flags().Lookup("timeout"))
	assert.NotNil(t, cmd.Flags().Lookup("output"))

	// Verify defaults
	typeFlag := cmd.Flags().Lookup("type")
	assert.Equal(t, "full", typeFlag.DefValue)

	timeoutFlag := cmd.Flags().Lookup("timeout")
	assert.Equal(t, "300", timeoutFlag.DefValue)
}

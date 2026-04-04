package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/surfbot-io/surfbot-agent/internal/model"
	sstorage "github.com/surfbot-io/surfbot-agent/internal/storage"
)

func setupAssetsTest(t *testing.T) {
	t.Helper()
	s, err := sstorage.NewSQLiteStore(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	store = s

	target := &model.Target{Value: "example.com"}
	require.NoError(t, s.CreateTarget(context.Background(), target))

	assets := []model.Asset{
		{TargetID: target.ID, Type: model.AssetTypeSubdomain, Value: "api.example.com", Status: model.AssetStatusActive},
		{TargetID: target.ID, Type: model.AssetTypeSubdomain, Value: "staging.example.com", Status: model.AssetStatusNew},
		{TargetID: target.ID, Type: model.AssetTypeSubdomain, Value: "old.example.com", Status: model.AssetStatusDisappeared},
		{TargetID: target.ID, Type: model.AssetTypeIPv4, Value: "93.184.216.34", Status: model.AssetStatusActive},
	}
	for i := range assets {
		require.NoError(t, s.UpsertAsset(context.Background(), &assets[i]))
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	fn()

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)
	return buf.String()
}

func TestAssetsListDefault(t *testing.T) {
	setupAssetsTest(t)

	output := captureStdout(t, func() {
		cmd := assetsCmd
		cmd.Flags().Set("new", "false")
		cmd.Flags().Set("disappeared", "false")
		cmd.Flags().Set("diff", "false")
		cmd.Flags().Set("json", "false")
		cmd.Flags().Set("type", "")
		cmd.Flags().Set("limit", "100")
		err := runAssetsList(cmd)
		require.NoError(t, err)
	})

	assert.Contains(t, output, "api.example.com")
	assert.Contains(t, output, "staging.example.com")
	assert.Contains(t, output, "old.example.com")
	assert.Contains(t, output, "93.184.216.34")
	assert.Contains(t, output, "TYPE")
	assert.Contains(t, output, "VALUE")
}

func TestAssetsListNewOnly(t *testing.T) {
	setupAssetsTest(t)

	output := captureStdout(t, func() {
		cmd := assetsCmd
		cmd.Flags().Set("new", "true")
		cmd.Flags().Set("disappeared", "false")
		cmd.Flags().Set("diff", "false")
		cmd.Flags().Set("json", "false")
		cmd.Flags().Set("type", "")
		cmd.Flags().Set("limit", "100")
		err := runAssetsList(cmd)
		require.NoError(t, err)
	})

	assert.Contains(t, output, "staging.example.com")
	assert.NotContains(t, output, "api.example.com")
	assert.NotContains(t, output, "old.example.com")
}

func TestAssetsListDisappeared(t *testing.T) {
	setupAssetsTest(t)

	output := captureStdout(t, func() {
		cmd := assetsCmd
		cmd.Flags().Set("new", "false")
		cmd.Flags().Set("disappeared", "true")
		cmd.Flags().Set("diff", "false")
		cmd.Flags().Set("json", "false")
		cmd.Flags().Set("type", "")
		cmd.Flags().Set("limit", "100")
		err := runAssetsList(cmd)
		require.NoError(t, err)
	})

	assert.Contains(t, output, "old.example.com")
	assert.NotContains(t, output, "api.example.com")
	assert.NotContains(t, output, "staging.example.com")
}

func TestAssetsDiff(t *testing.T) {
	s, err := sstorage.NewSQLiteStore(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	store = s

	ctx := context.Background()
	target := &model.Target{Value: "example.com"}
	require.NoError(t, s.CreateTarget(ctx, target))

	now := time.Now().UTC()
	scan := &model.Scan{TargetID: target.ID, Type: model.ScanTypeFull, Status: model.ScanStatusCompleted, StartedAt: &now}
	require.NoError(t, s.CreateScan(ctx, scan))

	ac := &model.AssetChange{
		TargetID:     target.ID,
		ScanID:       scan.ID,
		ChangeType:   model.ChangeTypeAppeared,
		Significance: model.SignificanceCritical,
		AssetType:    "subdomain",
		AssetValue:   "new.example.com",
		Summary:      "New subdomain discovered: new.example.com",
	}
	require.NoError(t, s.CreateAssetChange(ctx, ac))

	output := captureStdout(t, func() {
		cmd := assetsCmd
		cmd.Flags().Set("diff", "true")
		cmd.Flags().Set("json", "false")
		cmd.Flags().Set("limit", "100")
		err := runAssetsDiff(cmd)
		require.NoError(t, err)
	})

	assert.Contains(t, output, "new.example.com")
	assert.Contains(t, output, "APPEARED")
	assert.Contains(t, output, "critical")
}

func TestAssetsJSON(t *testing.T) {
	setupAssetsTest(t)

	output := captureStdout(t, func() {
		cmd := assetsCmd
		cmd.Flags().Set("new", "false")
		cmd.Flags().Set("disappeared", "false")
		cmd.Flags().Set("diff", "false")
		cmd.Flags().Set("json", "true")
		cmd.Flags().Set("type", "")
		cmd.Flags().Set("limit", "100")
		err := runAssetsList(cmd)
		require.NoError(t, err)
	})

	var assets []model.Asset
	require.NoError(t, json.Unmarshal([]byte(output), &assets))
	assert.NotEmpty(t, assets)
}

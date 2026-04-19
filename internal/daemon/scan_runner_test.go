package daemon

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/surfbot-io/surfbot-agent/internal/model"
	"github.com/surfbot-io/surfbot-agent/internal/pipeline"
)

type fakeOrchestrator struct {
	mu       sync.Mutex
	calls    int
	gotID    string
	gotOpts  pipeline.PipelineOptions
	gotCtx   context.Context
	scanID   string
	runErr   error
	blockCh  chan struct{}
	observed bool
}

func (f *fakeOrchestrator) Run(ctx context.Context, targetID string, opts pipeline.PipelineOptions) (*pipeline.PipelineResult, error) {
	f.mu.Lock()
	f.calls++
	f.gotID = targetID
	f.gotOpts = opts
	f.gotCtx = ctx
	block := f.blockCh
	scanID := f.scanID
	err := f.runErr
	f.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			f.mu.Lock()
			f.observed = ctx.Err() != nil
			f.mu.Unlock()
			return nil, ctx.Err()
		}
	}
	if scanID == "" {
		scanID = "s_default"
	}
	return &pipeline.PipelineResult{ScanID: scanID}, err
}

func silentLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestScanRunner_HonorsToolConfig(t *testing.T) {
	orch := &fakeOrchestrator{scanID: "s_42"}
	r := &ScanRunner{orchestrator: orch, log: silentLog()}

	tc := model.ToolConfig{}
	require.NoError(t, model.SetTool(tc, "nuclei", model.NucleiParams{Severity: []string{"critical"}}))
	eff := model.EffectiveConfig{
		RRule:      "FREQ=DAILY",
		Timezone:   "UTC",
		ToolConfig: tc,
	}

	scanID, err := r.Run(context.Background(), "sched_1", "tgt_1", eff)
	require.NoError(t, err)
	assert.Equal(t, "s_42", scanID)
	assert.Equal(t, "tgt_1", orch.gotID)
	// ToolConfig must reach the pipeline so it can per-unmarshal into typed
	// RunOptions for each detection tool.
	assert.Equal(t, tc, orch.gotOpts.ToolConfig,
		"scanRunner must forward EffectiveConfig.ToolConfig into PipelineOptions")
}

func TestScanRunner_UsesDefaultsWhenEmpty(t *testing.T) {
	orch := &fakeOrchestrator{scanID: "s_default"}
	r := &ScanRunner{orchestrator: orch, log: silentLog()}

	_, err := r.Run(context.Background(), "sched_1", "tgt_1", model.EffectiveConfig{})
	require.NoError(t, err)
	assert.Empty(t, orch.gotOpts.ToolConfig,
		"empty EffectiveConfig.ToolConfig must surface as nil/empty in PipelineOptions; tools then derive defaults")
}

func TestScanRunner_PropagatesCtx(t *testing.T) {
	orch := &fakeOrchestrator{blockCh: make(chan struct{})}
	r := &ScanRunner{orchestrator: orch, log: silentLog()}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := r.Run(ctx, "sched_1", "tgt_1", model.EffectiveConfig{})
		done <- err
	}()
	cancel()
	err := <-done
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.Canceled))
	orch.mu.Lock()
	defer orch.mu.Unlock()
	assert.True(t, orch.observed, "orchestrator must observe ctx cancellation")
}

func TestScanRunner_ReturnsScanID(t *testing.T) {
	orch := &fakeOrchestrator{scanID: "s_123"}
	r := &ScanRunner{orchestrator: orch, log: silentLog()}

	scanID, err := r.Run(context.Background(), "sched_1", "tgt_1", model.EffectiveConfig{})
	require.NoError(t, err)
	assert.Equal(t, "s_123", scanID)
	assert.Equal(t, 1, orch.calls)
}

func TestScanRunner_PropagatesError(t *testing.T) {
	orch := &fakeOrchestrator{scanID: "s_partial", runErr: errors.New("boom")}
	r := &ScanRunner{orchestrator: orch, log: silentLog()}

	scanID, err := r.Run(context.Background(), "sched_1", "tgt_1", model.EffectiveConfig{})
	require.Error(t, err)
	assert.Equal(t, "s_partial", scanID)
}

func TestScanRunner_InvalidToolConfigForwardsAnyway(t *testing.T) {
	// Malformed JSON for one tool must not fail the runner — the pipeline's
	// applyToolConfig falls through silently and the tool defaults kick in.
	// Here we only assert scanRunner forwards whatever ToolConfig came in;
	// pipeline.applyToolConfig is responsible for the per-tool fallback.
	orch := &fakeOrchestrator{scanID: "s_ok"}
	r := &ScanRunner{orchestrator: orch, log: silentLog()}

	tc := model.ToolConfig{
		"nuclei": []byte(`{"severity": "not-an-array"}`),
		"naabu":  []byte(`{"ports": "22,80"}`),
	}
	_, err := r.Run(context.Background(), "sched_1", "tgt_1", model.EffectiveConfig{ToolConfig: tc})
	require.NoError(t, err)
	assert.Equal(t, tc, orch.gotOpts.ToolConfig)
}

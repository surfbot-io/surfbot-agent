package pipeline

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/surfbot-io/surfbot-agent/internal/model"
	"github.com/surfbot-io/surfbot-agent/internal/storage"
)

// TestPipeline_FullScan_PersistsLogs is the issue #52 happy path:
// run a full mocked pipeline with a SQLiteLogSink wired in, then
// query the resulting scan_logs rows and assert lifecycle coverage.
//
// CLI parity (G4) is implicit — the test runs the same Pipeline.Run
// the CLI invokes, and asserts that the sink emits the events the
// UI needs WITHOUT modifying any pp.muted/success calls.
//
// The B1 fix (drop FK on tool_run_id) is implicitly verified: the
// pipeline emits ToolStarted log lines before recordToolRunWithID
// inserts the tool_run row. With the FK in place, the batched
// INSERT would fail and zero tool-level lines would persist.
func TestPipeline_FullScan_PersistsLogs(t *testing.T) {
	s := newTestStore(t)
	target := createTarget(t, s, "example.com")

	reg := mockRegistry(fullMockTools()...)
	pipe := New(s, reg)
	sink := NewSQLiteLogSink(s, SQLiteLogSinkOptions{})
	pipe.SetSink(sink)

	result, err := pipe.Run(context.Background(), target.ID, PipelineOptions{ScanType: model.ScanTypeFull})
	require.NoError(t, err)

	// Close synchronously flushes pending lines so the assertions
	// below see the complete log corpus. Idempotent — the deferred
	// second call returns instantly.
	require.NoError(t, sink.Close())
	defer func() { _ = sink.Close() }()

	logs, err := s.ListScanLogs(context.Background(), storage.ScanLogListOptions{
		ScanID: result.ScanID,
		Limit:  500,
	})
	require.NoError(t, err)
	require.NotEmpty(t, logs, "scan_logs must persist events from a full scan")

	// Coverage assertions — spec G2 guarantees structured events for
	// every lifecycle boundary an operator cares about.
	have := func(needle string) bool {
		for _, l := range logs {
			if strings.Contains(l.Text, needle) {
				return true
			}
		}
		return false
	}
	assert.True(t, have("scan started"), "expected scan-started log line")
	assert.True(t, have("scan completed"), "expected scan-completed log line")
	assert.True(t, have("phase=discovery"), "expected phase-started log for discovery")
	assert.True(t, have("phase=assessment"), "expected phase-started log for assessment")
	assert.True(t, have("tool started"), "expected tool-started log line")
	assert.True(t, have("tool completed"), "expected tool-completed log line")

	// Source diversity — the pipeline emits both scanner-level and
	// tool-level log lines. Asserting a tool-named source appears
	// is the operator-visible signal of CLI/UI parity.
	sources := map[string]bool{}
	for _, l := range logs {
		sources[l.Source] = true
	}
	assert.True(t, sources["scanner"], "scanner-level log lines must persist")
	toolSources := 0
	for src := range sources {
		if src != "scanner" {
			toolSources++
		}
	}
	assert.Greater(t, toolSources, 0, "at least one tool-named source (subfinder/dnsx/...) must appear in scan_logs")
}

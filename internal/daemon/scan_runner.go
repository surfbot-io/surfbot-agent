package daemon

// ScanRunner threads a Schedule's resolved EffectiveConfig through the
// existing detection pipeline. The pipeline pre-unmarshals
// EffectiveConfig.ToolConfig into typed *Params on detection.RunOptions
// per tool; each tool reads its typed field and falls back to
// model.DefaultXxxParams() for any zero-valued sub-field. This is the
// SCHED1.2c replacement for SCHED1.2b's LegacyScanRunner — the same
// pre-1.2b orchestration topology is preserved (sequential phase order
// owned by pipeline.Pipeline), only the per-tool params surface changes.
//
// SCHED1.2c also opts ad-hoc scans into the same code path: callers
// pass an EffectiveConfig built from a model.AdHocScanRun's ToolConfig
// (resolved against template + defaults), and the runner does not care
// whether the trigger was a schedule tick or an ad-hoc dispatch.

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/surfbot-io/surfbot-agent/internal/detection"
	"github.com/surfbot-io/surfbot-agent/internal/model"
	"github.com/surfbot-io/surfbot-agent/internal/pipeline"
	"github.com/surfbot-io/surfbot-agent/internal/storage"
)

// scanOrchestrator is the narrow surface ScanRunner needs from the
// pipeline package. Real callers pass *pipeline.Pipeline; tests inject a
// fake to capture the per-tool typed params and assert ctx propagation.
type scanOrchestrator interface {
	Run(ctx context.Context, targetID string, opts pipeline.PipelineOptions) (*pipeline.PipelineResult, error)
}

// ScanRunner satisfies the master ticker's ScanRunner interface
// (declared in internal/daemon/intervalsched). Its Run method's
// signature matches that interface structurally — Go's structural
// subtyping does the wiring at the dependency-injection site.
type ScanRunner struct {
	orchestrator scanOrchestrator
	sink         *pipeline.SQLiteLogSink
	log          *slog.Logger
}

// NewScanRunner wires a ScanRunner around the production pipeline. The
// store and registry are the same ones the rest of the daemon uses.
//
// Issue #52: scheduler-driven scans get the same SQLite log tee as
// CLI / webui-triggered scans. The sink is per-runner (long-lived for
// the daemon process) so its background goroutine survives across
// scans; it's closed when the daemon shuts down via Close().
func NewScanRunner(store storage.Store, registry *detection.Registry, log *slog.Logger) *ScanRunner {
	if log == nil {
		log = slog.Default()
	}
	pipe := pipeline.New(store, registry)
	sink := pipeline.NewSQLiteLogSink(store, pipeline.SQLiteLogSinkOptions{})
	pipe.SetSink(sink)
	return &ScanRunner{
		orchestrator: pipe,
		sink:         sink,
		log:          log,
	}
}

// Close shuts down the underlying log sink. Daemon shutdown calls this
// to flush outstanding scan_logs writes.
func (r *ScanRunner) Close() error {
	if r.sink != nil {
		return r.sink.Close()
	}
	return nil
}

// Run executes a full scan for the target, threading EffectiveConfig.
// ToolConfig through the pipeline so each detection tool sees its
// typed *Params (with defaults filling zero fields). The returned
// scanID is the new pipeline scan row's ID.
func (r *ScanRunner) Run(ctx context.Context, scheduleID, targetID string, effective model.EffectiveConfig) (string, error) {
	if r.orchestrator == nil {
		return "", fmt.Errorf("scan runner: orchestrator is nil")
	}
	result, err := r.orchestrator.Run(ctx, targetID, pipeline.PipelineOptions{
		ScanType:   model.ScanTypeFull,
		ToolConfig: effective.ToolConfig,
	})
	if err != nil {
		if result != nil {
			return result.ScanID, err
		}
		return "", err
	}
	return result.ScanID, nil
}

package daemon

// LegacyScanRunner is the SCHED1.2b bridge between the new master ticker
// (which dispatches per-schedule jobs with a resolved EffectiveConfig) and
// the existing detection pipeline (which runs all enabled tools against a
// target with a single full-scan profile).
//
// SCHED1.2c will replace this with a runner that unmarshals
// EffectiveConfig.ToolConfig into typed *Params and threads them through
// each detection tool. Until then, EffectiveConfig.ToolConfig is
// intentionally ignored — pre-SCHED1 behavior is preserved exactly.

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/surfbot-io/surfbot-agent/internal/detection"
	"github.com/surfbot-io/surfbot-agent/internal/model"
	"github.com/surfbot-io/surfbot-agent/internal/pipeline"
	"github.com/surfbot-io/surfbot-agent/internal/storage"
)

// scanOrchestrator is the narrow surface LegacyScanRunner needs from the
// pipeline package. Real callers pass *pipeline.Pipeline; tests inject a
// fake to assert ctx propagation and that EffectiveConfig is ignored.
type scanOrchestrator interface {
	Run(ctx context.Context, targetID string, opts pipeline.PipelineOptions) (*pipeline.PipelineResult, error)
}

// LegacyScanRunner satisfies the master ticker's ScanRunner interface
// (defined in internal/daemon/intervalsched). Its Run method's signature
// matches that interface structurally, so no explicit interface wiring is
// needed in this commit — Go's structural subtyping does the work once
// scheduler.go declares the interface in commit 2.
type LegacyScanRunner struct {
	orchestrator scanOrchestrator
	log          *slog.Logger
}

// NewLegacyScanRunner wires a runner around the production pipeline. The
// store and registry are the same ones the rest of the daemon uses.
func NewLegacyScanRunner(store storage.Store, registry *detection.Registry, log *slog.Logger) *LegacyScanRunner {
	if log == nil {
		log = slog.Default()
	}
	return &LegacyScanRunner{
		orchestrator: pipeline.New(store, registry),
		log:          log,
	}
}

// Run executes a full scan for the target. The EffectiveConfig.ToolConfig
// is intentionally discarded — SCHED1.2c will start consuming it.
func (r *LegacyScanRunner) Run(ctx context.Context, scheduleID, targetID string, _ model.EffectiveConfig) (string, error) {
	if r.orchestrator == nil {
		return "", fmt.Errorf("legacy scan runner: orchestrator is nil")
	}
	result, err := r.orchestrator.Run(ctx, targetID, pipeline.PipelineOptions{ScanType: model.ScanTypeFull})
	if err != nil {
		if result != nil {
			return result.ScanID, err
		}
		return "", err
	}
	return result.ScanID, nil
}

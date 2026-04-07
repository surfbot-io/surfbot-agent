package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/surfbot-io/surfbot-agent/internal/daemon/intervalsched"
	"github.com/surfbot-io/surfbot-agent/internal/detection"
	"github.com/surfbot-io/surfbot-agent/internal/pipeline"
	"github.com/surfbot-io/surfbot-agent/internal/storage"
)

// pipelineScanRunner implements intervalsched.ScanRunner by walking every
// enabled target and invoking the existing pipeline.Pipeline. It is the
// production glue between the SPEC-X2 scheduler and the SPEC-N6 detection
// pipeline.
type pipelineScanRunner struct {
	store      storage.Store
	registry   *detection.Registry
	quickTools []string
}

func newPipelineScanRunner(store storage.Store, registry *detection.Registry, quickTools []string) *pipelineScanRunner {
	return &pipelineScanRunner{store: store, registry: registry, quickTools: quickTools}
}

// Run iterates enabled targets and runs the pipeline against each. Errors
// are accumulated; the scheduler treats a non-nil error as "scan_fail"
// but still advances the cursor (spec §11 #7).
func (r *pipelineScanRunner) Run(ctx context.Context, profile intervalsched.Profile) error {
	targets, err := r.store.ListTargets(ctx)
	if err != nil {
		return fmt.Errorf("listing targets: %w", err)
	}
	if len(targets) == 0 {
		return nil
	}

	pipe := pipeline.New(r.store, r.registry)
	opts := intervalsched.BuildPipelineOptions(profile, r.quickTools)

	var errs []error
	for _, t := range targets {
		if !t.Enabled {
			continue
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if _, err := pipe.Run(ctx, t.ID, opts); err != nil {
			errs = append(errs, fmt.Errorf("target %s: %w", t.Value, err))
		}
	}
	return errors.Join(errs...)
}

package pipeline

import (
	"context"
	"fmt"

	"github.com/surfbot-io/surfbot-agent/internal/detection"
	"github.com/surfbot-io/surfbot-agent/internal/storage"
)

// Pipeline orchestrates the execution of detection tools in order.
type Pipeline struct {
	store storage.Store
	tools []detection.DetectionTool
}

// New creates a new Pipeline with the given store and tools.
func New(store storage.Store, tools []detection.DetectionTool) *Pipeline {
	return &Pipeline{
		store: store,
		tools: tools,
	}
}

// Run executes the full detection pipeline against the given target.
func (p *Pipeline) Run(_ context.Context, _ string) error {
	return fmt.Errorf("pipeline: not yet implemented")
}

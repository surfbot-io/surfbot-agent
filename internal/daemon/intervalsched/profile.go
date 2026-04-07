package intervalsched

import (
	"github.com/surfbot-io/surfbot-agent/internal/model"
	"github.com/surfbot-io/surfbot-agent/internal/pipeline"
)

// Profile selects how aggressive a scheduled scan should be.
type Profile string

const (
	ProfileFull  Profile = "full"
	ProfileQuick Profile = "quick"
)

// DefaultQuickTools is the whitelist applied when quick_check_tools is
// not configured. Matches the spec §2 default: only httpx + nuclei.
var DefaultQuickTools = []string{"httpx", "nuclei"}

// BuildPipelineOptions translates a Profile (and the configured quick-tool
// whitelist) into the existing pipeline.PipelineOptions consumed by
// pipeline.Run. The full profile passes nothing → all tools run; the
// quick profile sets ScanType=Quick (skips port_scan) AND restricts the
// tool set to the whitelist.
func BuildPipelineOptions(p Profile, quickTools []string) pipeline.PipelineOptions {
	if p == ProfileQuick {
		tools := quickTools
		if len(tools) == 0 {
			tools = DefaultQuickTools
		}
		return pipeline.PipelineOptions{
			ScanType: model.ScanTypeQuick,
			Tools:    tools,
		}
	}
	return pipeline.PipelineOptions{ScanType: model.ScanTypeFull}
}

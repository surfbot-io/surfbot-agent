package detection

import (
	"context"

	"github.com/surfbot-io/surfbot-agent/internal/model"
)

// ToolKind describes how a detection tool is implemented.
type ToolKind string

const (
	ToolKindLibrary ToolKind = "library"
	ToolKindNative  ToolKind = "native"
)

// DetectionTool is the pluggable interface for all scanning/detection tools.
// Each tool receives a list of inputs (targets or assets from previous pipeline stage)
// and returns discovered assets and/or findings.
type DetectionTool interface {
	// Name returns the tool identifier (e.g., "subfinder", "nuclei")
	Name() string

	// Phase returns which pipeline phase this tool runs in
	Phase() string

	// Kind returns how the tool is implemented (library, native)
	Kind() ToolKind

	// Available checks if the tool can run (dependencies present, etc.)
	Available() bool

	// Run executes the tool against the given inputs and returns results.
	// Inputs are string values (domains, IPs, URLs) from the previous pipeline stage.
	Run(ctx context.Context, inputs []string, opts RunOptions) (*RunResult, error)

	// Command returns the CLI subcommand name (e.g., "discover", "resolve").
	Command() string

	// Description returns a one-line description for --help and LLM discovery.
	Description() string

	// InputType returns what inputs this tool expects: "domains", "ips", "hostports", "urls".
	InputType() string

	// OutputTypes returns the asset types this tool produces.
	OutputTypes() []string
}

// RunOptions configures a tool execution.
//
// SCHED1.2c: each detection tool gains a typed *Params pointer. When set
// by the caller (scanRunner from a resolved EffectiveConfig), the tool
// reads its typed field and falls back to model.DefaultXxxParams() for
// unset fields. The legacy RateLimit/Timeout/ExtraArgs surface stays for
// existing callers (CLI sub-commands, tests) — when a typed *Params is
// also supplied it wins per-field.
type RunOptions struct {
	RateLimit int               // requests per second (0 = default)
	Timeout   int               // seconds (0 = default)
	ExtraArgs map[string]string // tool-specific options

	// Typed per-tool params. Each field is read by the matching tool only.
	// Nil means "no per-tool override; derive from defaults + ExtraArgs".
	NucleiParams    *model.NucleiParams
	NaabuParams     *model.NaabuParams
	HttpxParams     *model.HttpxParams
	SubfinderParams *model.SubfinderParams
	DnsxParams      *model.DnsxParams
}

// RunResult holds the output of a tool execution.
type RunResult struct {
	Assets   []model.Asset
	Findings []model.Finding
	ToolRun  model.ToolRun
}

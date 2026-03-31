package remediation

import (
	"context"

	"github.com/surfbot-io/surfbot-agent/internal/model"
)

type ExecutionContext string

const (
	ExecLocal  ExecutionContext = "local"
	ExecRemote ExecutionContext = "remote"
	ExecAny    ExecutionContext = "any"
)

// RemediationTool is the pluggable interface for all remediation tools.
type RemediationTool interface {
	Name() string
	ExecutionContext() ExecutionContext
	Available() bool

	// CanFix checks if this tool can remediate the given finding
	CanFix(finding model.Finding) bool

	// DryRun shows what would be changed without applying
	DryRun(ctx context.Context, finding model.Finding) (*RemediationPlan, error)

	// Apply executes the remediation
	Apply(ctx context.Context, plan *RemediationPlan) (*RemediationResult, error)
}

type RemediationPlan struct {
	FindingID   string
	ToolName    string
	Description string
	Changes     []PlannedChange
	RiskLevel   string
}

type PlannedChange struct {
	Type   string // "file_modify", "command_run", "config_change"
	Target string // file path or command
	Before string // current state (for file_modify)
	After  string // desired state (for file_modify)
}

type RemediationResult struct {
	Success bool
	Message string
	Changes []AppliedChange
}

type AppliedChange struct {
	Type    string
	Target  string
	Applied bool
	Error   string
}

package remediation

import (
	"context"
	"fmt"
	"os/exec"

	"github.com/surfbot-io/surfbot-agent/internal/model"
)

type Nginx struct{}

func NewNginx() *Nginx { return &Nginx{} }

func (n *Nginx) Name() string                    { return "nginx" }
func (n *Nginx) ExecutionContext() ExecutionContext { return ExecLocal }

func (n *Nginx) Available() bool {
	_, err := exec.LookPath("nginx")
	return err == nil
}

func (n *Nginx) CanFix(_ model.Finding) bool { return false }

func (n *Nginx) DryRun(_ context.Context, _ model.Finding) (*RemediationPlan, error) {
	return nil, fmt.Errorf("nginx remediation: not yet implemented")
}

func (n *Nginx) Apply(_ context.Context, _ *RemediationPlan) (*RemediationResult, error) {
	return nil, fmt.Errorf("nginx remediation: not yet implemented")
}

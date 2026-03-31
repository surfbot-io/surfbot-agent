package detection

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	nuclei "github.com/projectdiscovery/nuclei/v3/lib"
	"github.com/projectdiscovery/nuclei/v3/pkg/output"

	"github.com/surfbot-io/surfbot-agent/internal/model"
)

// NucleiTool runs vulnerability assessment using the nuclei Go library.
type NucleiTool struct{}

func NewNucleiTool() *NucleiTool { return &NucleiTool{} }

func (n *NucleiTool) Name() string   { return "nuclei" }
func (n *NucleiTool) Phase() string  { return "assessment" }
func (n *NucleiTool) Kind() ToolKind { return ToolKindLibrary }
func (n *NucleiTool) Available() bool { return true }

func (n *NucleiTool) Run(ctx context.Context, inputs []string, opts RunOptions) (*RunResult, error) {
	if len(inputs) == 0 {
		return &RunResult{}, nil
	}

	severity := "critical,high,medium,low,info"
	if s, ok := opts.ExtraArgs["severity"]; ok && s != "" {
		severity = s
	}

	rateLimit := 150
	if opts.RateLimit > 0 {
		rateLimit = opts.RateLimit
	}

	engineOpts := []nuclei.NucleiSDKOptions{
		nuclei.WithTemplateFilters(nuclei.TemplateFilters{
			Severity: severity,
		}),
		nuclei.DisableUpdateCheck(),
		nuclei.WithGlobalRateLimit(rateLimit, time.Second),
	}

	if templatePath, ok := opts.ExtraArgs["templates"]; ok && templatePath != "" {
		engineOpts = append(engineOpts, nuclei.WithTemplatesOrWorkflows(
			nuclei.TemplateSources{
				Templates: []string{templatePath},
			},
		))
	}

	ne, err := nuclei.NewNucleiEngineCtx(ctx, engineOpts...)
	if err != nil {
		return nil, fmt.Errorf("nuclei engine init: %w", err)
	}
	defer ne.Close()

	ne.LoadTargets(inputs, false)

	var findings []model.Finding
	var mu sync.Mutex

	err = ne.ExecuteWithCallback(func(event *output.ResultEvent) {
		defer func() {
			if r := recover(); r != nil {
				// Protect against panics in callback
			}
		}()

		finding := mapNucleiEvent(event)
		mu.Lock()
		findings = append(findings, finding)
		mu.Unlock()
	})
	if err != nil {
		return nil, fmt.Errorf("nuclei execution: %w", err)
	}

	result := &RunResult{
		Findings: findings,
		ToolRun: model.ToolRun{
			ToolName:      "nuclei",
			Phase:         "assessment",
			Status:        model.ToolRunCompleted,
			FindingsCount: len(findings),
			TargetsCount:  len(inputs),
			Config:        map[string]interface{}{},
		},
	}
	return result, nil
}

func mapNucleiEvent(event *output.ResultEvent) model.Finding {
	cve := ""
	var cvss float64
	if event.Info.Classification != nil {
		cveSlice := event.Info.Classification.CVEID.ToSlice()
		if len(cveSlice) > 0 {
			cve = cveSlice[0]
		}
		cvss = event.Info.Classification.CVSSScore
	}

	var refs []string
	if event.Info.Reference != nil {
		refs = event.Info.Reference.ToSlice()
	}
	if refs == nil {
		refs = []string{}
	}

	return model.Finding{
		TemplateID:   event.TemplateID,
		TemplateName: event.Info.Name,
		Severity:     mapNucleiSeverity(event.Info.SeverityHolder.Severity.String()),
		Title:        event.Info.Name,
		Description:  event.Info.Description,
		Remediation:  event.Info.Remediation,
		References:   refs,
		Evidence:     event.Matched,
		CVSS:         cvss,
		CVE:          cve,
		SourceTool:   "nuclei",
		Confidence:   80.0,
	}
}

func mapNucleiSeverity(s string) model.Severity {
	switch strings.ToLower(s) {
	case "critical":
		return model.SeverityCritical
	case "high":
		return model.SeverityHigh
	case "medium":
		return model.SeverityMedium
	case "low":
		return model.SeverityLow
	default:
		return model.SeverityInfo
	}
}

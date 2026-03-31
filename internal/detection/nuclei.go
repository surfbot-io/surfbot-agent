package detection

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	nuclei "github.com/projectdiscovery/nuclei/v3/lib"
	"github.com/projectdiscovery/nuclei/v3/pkg/installer"
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

	// Ensure templates are installed (transparent auto-download)
	if err := ensureNucleiTemplates(); err != nil {
		return nil, fmt.Errorf("nuclei templates setup: %w", err)
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

// ensureNucleiTemplates downloads nuclei templates if they are not already installed.
func ensureNucleiTemplates() error {
	installer.HideProgressBar = true
	installer.HideUpdateChangesTable = true
	installer.HideReleaseNotes = true

	tm := &installer.TemplateManager{}
	if err := tm.FreshInstallIfNotExists(); err != nil {
		return fmt.Errorf("installing nuclei templates: %w", err)
	}

	// Create .nuclei-ignore if it doesn't exist (prevents ERR log)
	cfg := nuclei.DefaultConfig
	if cfg != nil {
		ignorePath := cfg.GetIgnoreFilePath()
		if _, err := os.Stat(ignorePath); os.IsNotExist(err) {
			os.MkdirAll(strings.TrimSuffix(ignorePath, ".nuclei-ignore"), 0o755) //nolint:errcheck
			os.WriteFile(ignorePath, []byte(""), 0o644)                           //nolint:errcheck
		}
	}

	return nil
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

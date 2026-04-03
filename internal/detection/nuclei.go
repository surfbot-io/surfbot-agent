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

	timeout := 5
	if opts.Timeout > 0 && opts.Timeout < 60 {
		timeout = opts.Timeout
	}

	// Template filtering by scan profile:
	// "fast" (quick scans): only cve + misconfig + exposure tags
	// default (full scans): cve + misconfig + exposure + tech + default-login
	// This reduces ~10k templates to ~2-3k for practical scan times.
	profile := opts.ExtraArgs["profile"]

	filters := nuclei.TemplateFilters{
		Severity:             severity,
		ExcludeProtocolTypes: "headless,file,code",
		ExcludeTags:          []string{"dos", "fuzz", "intrusive"},
	}
	switch profile {
	case "fast":
		// Quick scan: only tech detection + misconfig (~1,900 templates)
		filters.Tags = []string{"tech", "misconfig"}
	default:
		// Full scan: tech + misconfig + exposure (~3,300 templates)
		filters.Tags = []string{"tech", "misconfig", "exposure"}
	}

	templateDirs := defaultTemplateDirs()

	engineOpts := []nuclei.NucleiSDKOptions{
		nuclei.WithTemplateFilters(filters),
		nuclei.WithTemplatesOrWorkflows(nuclei.TemplateSources{
			Templates: templateDirs,
		}),
		nuclei.DisableUpdateCheck(),
		nuclei.WithGlobalRateLimit(rateLimit, time.Second),
		nuclei.WithNetworkConfig(nuclei.NetworkConfig{
			Timeout:           timeout,
			Retries:           2,
			MaxHostError:      30,
			DisableMaxHostErr: true,
			LeaveDefaultPorts: true,
		}),
		nuclei.WithVerbosity(nuclei.VerbosityOptions{
			Silent: true,
		}),
	}

	ne, err := nuclei.NewNucleiEngineCtx(ctx, engineOpts...)
	if err != nil {
		return nil, fmt.Errorf("nuclei engine init: %w", err)
	}
	defer ne.Close()

	ne.LoadTargets(inputs, false)

	var findings []model.Finding
	var mu sync.Mutex
	var skipped int

	err = ne.ExecuteWithCallback(func(event *output.ResultEvent) {
		defer func() {
			if r := recover(); r != nil {
				// Protect against panics in callback
			}
		}()

		// Filter out findings without real evidence
		if !isValidFinding(event) {
			mu.Lock()
			skipped++
			mu.Unlock()
			return
		}

		finding := mapNucleiEvent(event)
		mu.Lock()
		findings = append(findings, finding)
		mu.Unlock()
	})
	// Context deadline/cancellation is not a fatal error if we already have findings —
	// nuclei may not finish all templates within the timeout, and that's OK.
	if err != nil && len(findings) == 0 {
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
			Config: map[string]interface{}{
				"filtered": skipped,
			},
		},
	}
	return result, nil
}

// isValidFinding checks if a nuclei result represents a real finding, not noise.
func isValidFinding(event *output.ResultEvent) bool {
	// Must have a matched-at URL — this means the template actually matched something
	if event.Matched == "" {
		return false
	}
	// Must have a host
	if event.Host == "" {
		return false
	}
	// MatcherStatus false means matchers did not match (negative/inverse result)
	if !event.MatcherStatus {
		return false
	}
	return true
}

// defaultTemplateDirs returns the template directories for a standard scan:
// http, ssl, dns — excludes network/dast/javascript/cloud which are slow or noisy.
func defaultTemplateDirs() []string {
	base := nuclei.DefaultConfig.TemplatesDirectory
	return []string{
		base + "/http",
		base + "/ssl",
		base + "/dns",
	}
}

// fastTemplateDirs returns a minimal set of template dirs for quick scans.
func fastTemplateDirs() []string {
	base := nuclei.DefaultConfig.TemplatesDirectory
	return []string{
		base + "/http",
		base + "/ssl",
		base + "/dns",
	}
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

// mapNucleiEvent converts a nuclei result event to a Finding.
// AssetID is temporarily set to event.Host so the pipeline can resolve it to a real asset ID.
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
		AssetID:      event.Host, // temporary: pipeline resolves to real asset ID
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
		Status:       model.FindingStatusOpen,
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

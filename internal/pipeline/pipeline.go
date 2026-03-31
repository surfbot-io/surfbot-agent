package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/surfbot-io/surfbot-agent/internal/detection"
	"github.com/surfbot-io/surfbot-agent/internal/model"
	"github.com/surfbot-io/surfbot-agent/internal/storage"
)

// PipelineOptions configures a pipeline execution.
type PipelineOptions struct {
	ScanType  model.ScanType // full|quick|discovery (default: full)
	Tools     []string       // Specific tools to run (empty = all available)
	RateLimit int            // Global rate limit (0 = per-tool defaults)
	Timeout   int            // Per-phase timeout in seconds (0 = 300s default)
}

// PipelineResult holds the output of a pipeline execution.
type PipelineResult struct {
	ScanID        string
	Target        string
	TotalAssets   int
	TotalFindings int
	Duration      time.Duration
	Stats         model.ScanStats
	Phases        []PhaseResult
}

// PhaseResult describes the outcome of a single pipeline phase.
type PhaseResult struct {
	ToolName    string        `json:"tool"`
	Phase       string        `json:"phase"`
	Status      string        `json:"status"`
	Duration    time.Duration `json:"-"`
	DurationMs  int64         `json:"duration_ms"`
	InputCount  int           `json:"input_count"`
	OutputCount int           `json:"output_count"`
	Error       string        `json:"error,omitempty"`
}

// Pipeline orchestrates the execution of detection tools in order.
type Pipeline struct {
	store    storage.Store
	registry *detection.Registry
}

// New creates a new Pipeline with the given store and registry.
func New(store storage.Store, registry *detection.Registry) *Pipeline {
	return &Pipeline{
		store:    store,
		registry: registry,
	}
}

// Run executes the full detection pipeline against the given target.
func (p *Pipeline) Run(ctx context.Context, targetID string, opts PipelineOptions) (*PipelineResult, error) {
	if opts.ScanType == "" {
		opts.ScanType = model.ScanTypeFull
	}

	now := time.Now().UTC()
	scan := &model.Scan{
		ID:        uuid.New().String(),
		TargetID:  targetID,
		Type:      opts.ScanType,
		Status:    model.ScanStatusRunning,
		Phase:     "initializing",
		Progress:  0,
		StartedAt: &now,
	}
	if err := p.store.CreateScan(ctx, scan); err != nil {
		return nil, fmt.Errorf("creating scan: %w", err)
	}

	target, err := p.store.GetTarget(ctx, targetID)
	if err != nil {
		return nil, fmt.Errorf("getting target: %w", err)
	}

	tools := p.selectTools(opts)
	inputs := []string{target.Value}
	hostnames := []string{target.Value} // original hostnames for hostname-aware phases
	result := &PipelineResult{
		ScanID: scan.ID,
		Target: target.Value,
	}

	fmt.Fprintf(os.Stderr, "[*] Scan started: %s (%s)\n", target.Value, opts.ScanType)

	for i, tool := range tools {
		// Check for context cancellation
		select {
		case <-ctx.Done():
			scan.Status = model.ScanStatusCancelled
			scan.Phase = "cancelled"
			nowt := time.Now().UTC()
			scan.FinishedAt = &nowt
			p.store.UpdateScan(context.Background(), scan) //nolint:errcheck
			return nil, ctx.Err()
		default:
		}

		if shouldSkip(tool, opts) {
			pr := PhaseResult{
				ToolName: tool.Name(),
				Phase:    tool.Phase(),
				Status:   "skipped",
			}
			result.Phases = append(result.Phases, pr)
			p.recordToolRun(ctx, tool, scan.ID, time.Now(), 0, len(inputs), 0, model.ToolRunSkipped, "")
			continue
		}

		// Update scan phase + progress
		scan.Phase = tool.Phase()
		scan.Progress = float32(i) / float32(len(tools)) * 100
		p.store.UpdateScan(ctx, scan) //nolint:errcheck

		fmt.Fprintf(os.Stderr, "[+] Phase %d/%d: %s — %s\n", i+1, len(tools), tool.Phase(), tool.Name())

		// Per-phase timeout
		phaseTimeout := time.Duration(opts.Timeout) * time.Second
		if phaseTimeout == 0 {
			phaseTimeout = 5 * time.Minute
		}
		phaseCtx, cancel := context.WithTimeout(ctx, phaseTimeout)

		runOpts := detection.RunOptions{
			RateLimit: opts.RateLimit,
			Timeout:   opts.Timeout,
		}

		startTime := time.Now()
		toolResult, toolErr := tool.Run(phaseCtx, inputs, runOpts)
		duration := time.Since(startTime)
		cancel()

		pr := PhaseResult{
			ToolName:   tool.Name(),
			Phase:      tool.Phase(),
			Duration:   duration,
			DurationMs: duration.Milliseconds(),
			InputCount: len(inputs),
		}

		if toolErr != nil {
			pr.Status = "failed"
			pr.Error = toolErr.Error()
			result.Phases = append(result.Phases, pr)

			p.recordToolRun(ctx, tool, scan.ID, startTime, duration, len(inputs), 0, model.ToolRunFailed, toolErr.Error())

			if isHardFailure(tool) {
				scan.Status = model.ScanStatusFailed
				scan.Error = fmt.Sprintf("%s failed: %s", tool.Name(), toolErr.Error())
				nowt := time.Now().UTC()
				scan.FinishedAt = &nowt
				p.store.UpdateScan(ctx, scan) //nolint:errcheck
				return nil, fmt.Errorf("%s: %w", tool.Name(), toolErr)
			}
			fmt.Fprintf(os.Stderr, "    Warning: %s failed: %v\n", tool.Name(), toolErr)
			continue
		}

		// Persist assets
		for j := range toolResult.Assets {
			toolResult.Assets[j].TargetID = targetID
			if toolResult.Assets[j].Status == "" {
				toolResult.Assets[j].Status = model.AssetStatusNew
			}
			p.store.UpsertAsset(ctx, &toolResult.Assets[j]) //nolint:errcheck
		}

		// Persist findings — resolve AssetID from URL/host to actual asset
		if len(toolResult.Findings) > 0 {
			assetLookup := p.buildAssetLookup(ctx, targetID)
			fallbackAssetID := resolveAssetID("", assetLookup)
			persisted, errCount := 0, 0
			var firstErr error
			for j := range toolResult.Findings {
				toolResult.Findings[j].ScanID = scan.ID
				resolved := resolveAssetID(toolResult.Findings[j].AssetID, assetLookup)
				if resolved == "" {
					resolved = fallbackAssetID
				}
				if resolved == "" {
					errCount++
					continue
				}
				toolResult.Findings[j].AssetID = resolved
				if err := p.store.UpsertFinding(ctx, &toolResult.Findings[j]); err != nil {
					errCount++
					if firstErr == nil {
						firstErr = err
					}
					continue
				}
				persisted++
			}
			if errCount > 0 && firstErr != nil {
				fmt.Fprintf(os.Stderr, "    Warning: %d findings failed to persist (first error: %v)\n", errCount, firstErr)
			}
			if persisted > 0 && persisted < len(toolResult.Findings) {
				fmt.Fprintf(os.Stderr, "    Persisted %d/%d findings (duplicates merged)\n", persisted, len(toolResult.Findings))
			}
		}

		// Update stats
		updateStats(&scan.Stats, tool.Phase(), toolResult)

		outputCount := len(toolResult.Assets) + len(toolResult.Findings)
		pr.Status = "completed"
		pr.OutputCount = outputCount
		result.Phases = append(result.Phases, pr)

		p.recordToolRun(ctx, tool, scan.ID, startTime, duration, len(inputs), outputCount, model.ToolRunCompleted, "")

		// Print phase summary
		printPhaseSummary(tool, toolResult)

		// Thread outputs to next phase
		nextInputs := extractInputsForNextPhase(tool.Phase(), toolResult)

		// Track hostnames from discovery phase
		if tool.Phase() == "discovery" {
			if len(nextInputs) == 0 {
				fmt.Fprintf(os.Stderr, "    No subdomains found, using root domain as target\n")
				nextInputs = []string{target.Value}
			}
			hostnames = nextInputs
		}

		// For http_probe: include hostnames alongside IPs/ports so CDN-backed
		// sites can be probed by hostname (not just IP)
		if tool.Phase() == "resolution" || tool.Phase() == "port_scan" {
			nextInputs = mergeHostnames(nextInputs, hostnames)
		}

		// Update inputs for next phase (assessment returns nil — keep previous inputs)
		if nextInputs != nil {
			inputs = nextInputs
		} else if tool.Phase() != "assessment" {
			inputs = []string{}
		}

		// Edge case: httpx returned 0 URLs → skip remaining phases
		if tool.Phase() == "http_probe" && len(inputs) == 0 {
			for k := i + 1; k < len(tools); k++ {
				if shouldSkip(tools[k], opts) {
					continue
				}
				result.Phases = append(result.Phases, PhaseResult{
					ToolName: tools[k].Name(),
					Phase:    tools[k].Phase(),
					Status:   "skipped",
				})
				p.recordToolRun(ctx, tools[k], scan.ID, time.Now(), 0, 0, 0, model.ToolRunSkipped, "no live URLs to assess")
			}
			break
		}
	}

	// Complete scan
	scan.Status = model.ScanStatusCompleted
	scan.Phase = "completed"
	scan.Progress = 100
	finishedAt := time.Now().UTC()
	scan.FinishedAt = &finishedAt
	p.store.UpdateScan(ctx, scan) //nolint:errcheck

	result.Stats = scan.Stats
	result.Duration = finishedAt.Sub(*scan.StartedAt)
	result.TotalAssets = scan.Stats.SubdomainsFound + scan.Stats.IPsResolved + scan.Stats.OpenPorts + scan.Stats.HTTPProbed
	result.TotalFindings = scan.Stats.FindingsTotal

	return result, nil
}

func (p *Pipeline) selectTools(opts PipelineOptions) []detection.DetectionTool {
	allTools := p.registry.Tools()
	if len(opts.Tools) == 0 {
		return allTools
	}

	toolSet := make(map[string]bool, len(opts.Tools))
	for _, name := range opts.Tools {
		toolSet[name] = true
	}

	var selected []detection.DetectionTool
	for _, t := range allTools {
		if toolSet[t.Name()] {
			selected = append(selected, t)
		}
	}
	return selected
}

func (p *Pipeline) recordToolRun(ctx context.Context, tool detection.DetectionTool, scanID string, startedAt time.Time, duration time.Duration, inputCount, outputCount int, status model.ToolRunStatus, errMsg string) {
	finishedAt := startedAt.Add(duration)
	tr := &model.ToolRun{
		ID:            uuid.New().String(),
		ScanID:        scanID,
		ToolName:      tool.Name(),
		Phase:         tool.Phase(),
		Status:        status,
		StartedAt:     startedAt,
		FinishedAt:    &finishedAt,
		DurationMs:    duration.Milliseconds(),
		TargetsCount:  inputCount,
		FindingsCount: outputCount,
		ErrorMessage:  errMsg,
		Config:        map[string]interface{}{},
	}
	p.store.CreateToolRun(ctx, tr) //nolint:errcheck
}

// buildAssetLookup returns a map of asset value → asset ID for all assets of a target.
func (p *Pipeline) buildAssetLookup(ctx context.Context, targetID string) map[string]string {
	assets, err := p.store.ListAssets(ctx, storage.AssetListOptions{TargetID: targetID, Limit: 10000})
	if err != nil {
		return nil
	}
	lookup := make(map[string]string, len(assets))
	for _, a := range assets {
		lookup[a.Value] = a.ID
	}
	return lookup
}

// resolveAssetID maps a host/URL string (from nuclei) to a real asset ID.
// It tries exact match first, then prefix matching, then fallback to any URL asset.
func resolveAssetID(hostHint string, lookup map[string]string) string {
	if lookup == nil || len(lookup) == 0 {
		return ""
	}

	if hostHint != "" {
		// Exact match
		if id, ok := lookup[hostHint]; ok {
			return id
		}
		// Prefix matching (e.g., "https://lacuerda.net:443" ↔ asset value)
		for value, id := range lookup {
			if strings.Contains(hostHint, value) || strings.Contains(value, hostHint) {
				return id
			}
		}
	}

	// Fallback: first URL asset
	for value, id := range lookup {
		if strings.HasPrefix(value, "http") {
			return id
		}
	}
	// Last resort: any asset
	for _, id := range lookup {
		return id
	}
	return ""
}

// mergeHostnames appends hostnames to the input list, deduplicating.
func mergeHostnames(inputs, hostnames []string) []string {
	seen := make(map[string]bool, len(inputs))
	for _, v := range inputs {
		seen[v] = true
	}
	merged := append([]string{}, inputs...)
	for _, h := range hostnames {
		if !seen[h] {
			seen[h] = true
			merged = append(merged, h)
		}
	}
	return merged
}

func shouldSkip(tool detection.DetectionTool, opts PipelineOptions) bool {
	switch opts.ScanType {
	case model.ScanTypeDiscovery:
		return tool.Phase() != "discovery" && tool.Phase() != "resolution"
	case model.ScanTypeQuick:
		return tool.Phase() == "port_scan"
	case model.ScanTypeFull:
		return false
	}
	return false
}

func isHardFailure(tool detection.DetectionTool) bool {
	return tool.Phase() == "discovery"
}

func extractInputsForNextPhase(phase string, result *detection.RunResult) []string {
	switch phase {
	case "discovery":
		var subdomains []string
		for _, a := range result.Assets {
			if a.Type == model.AssetTypeSubdomain {
				subdomains = append(subdomains, a.Value)
			}
		}
		return subdomains

	case "resolution":
		seen := make(map[string]bool)
		var ips []string
		for _, a := range result.Assets {
			if (a.Type == model.AssetTypeIPv4 || a.Type == model.AssetTypeIPv6) && !seen[a.Value] {
				seen[a.Value] = true
				ips = append(ips, a.Value)
			}
		}
		return ips

	case "port_scan":
		var hostports []string
		for _, a := range result.Assets {
			if a.Type == model.AssetTypePort {
				hostports = append(hostports, a.Value)
			}
		}
		return hostports

	case "http_probe":
		var urls []string
		for _, a := range result.Assets {
			if a.Type == model.AssetTypeURL {
				urls = append(urls, a.Value)
			}
		}
		return urls

	case "assessment":
		return nil
	}
	return nil
}

func updateStats(stats *model.ScanStats, phase string, result *detection.RunResult) {
	switch phase {
	case "discovery":
		for _, a := range result.Assets {
			if a.Type == model.AssetTypeSubdomain {
				stats.SubdomainsFound++
			}
		}
	case "resolution":
		for _, a := range result.Assets {
			if a.Type == model.AssetTypeIPv4 || a.Type == model.AssetTypeIPv6 {
				stats.IPsResolved++
			}
		}
	case "port_scan":
		stats.OpenPorts = len(result.Assets)
	case "http_probe":
		for _, a := range result.Assets {
			switch a.Type {
			case model.AssetTypeURL:
				stats.HTTPProbed++
			case model.AssetTypeTechnology:
				stats.TechDetected++
			}
		}
	case "assessment":
		for _, f := range result.Findings {
			stats.FindingsTotal++
			switch f.Severity {
			case model.SeverityCritical:
				stats.FindingsCritical++
			case model.SeverityHigh:
				stats.FindingsHigh++
			case model.SeverityMedium:
				stats.FindingsMedium++
			case model.SeverityLow:
				stats.FindingsLow++
			case model.SeverityInfo:
				stats.FindingsInfo++
			}
		}
	}
}

func printPhaseSummary(tool detection.DetectionTool, result *detection.RunResult) {
	switch tool.Phase() {
	case "discovery":
		count := 0
		for _, a := range result.Assets {
			if a.Type == model.AssetTypeSubdomain {
				count++
			}
		}
		fmt.Fprintf(os.Stderr, "    Found %d subdomains\n", count)
	case "resolution":
		ipv4, ipv6 := 0, 0
		for _, a := range result.Assets {
			switch a.Type {
			case model.AssetTypeIPv4:
				ipv4++
			case model.AssetTypeIPv6:
				ipv6++
			}
		}
		fmt.Fprintf(os.Stderr, "    Resolved %d IPs (%d IPv4, %d IPv6)\n", ipv4+ipv6, ipv4, ipv6)
	case "port_scan":
		hosts := make(map[string]bool)
		for _, a := range result.Assets {
			if a.Type == model.AssetTypePort && a.ParentID != "" {
				hosts[a.ParentID] = true
			}
		}
		fmt.Fprintf(os.Stderr, "    Scanned hosts, found %d open ports\n", len(result.Assets))
	case "http_probe":
		urls, techs := 0, 0
		for _, a := range result.Assets {
			switch a.Type {
			case model.AssetTypeURL:
				urls++
			case model.AssetTypeTechnology:
				techs++
			}
		}
		fmt.Fprintf(os.Stderr, "    Probed endpoints, %d live (%d technologies detected)\n", urls, techs)
	case "assessment":
		crit, high, med, low, info := 0, 0, 0, 0, 0
		for _, f := range result.Findings {
			switch f.Severity {
			case model.SeverityCritical:
				crit++
			case model.SeverityHigh:
				high++
			case model.SeverityMedium:
				med++
			case model.SeverityLow:
				low++
			case model.SeverityInfo:
				info++
			}
		}
		total := len(result.Findings)
		fmt.Fprintf(os.Stderr, "    Scanned %d URLs, found %d findings (%d critical, %d high, %d medium, %d low, %d info)\n",
			len(result.Assets)+total, total, crit, high, med, low, info)
	}
}

// WriteJSONResult writes the pipeline result as JSON to the given path.
func WriteJSONResult(result *PipelineResult, path string) error {
	type jsonResult struct {
		ScanID     string            `json:"scan_id"`
		Target     string            `json:"target"`
		Type       string            `json:"type"`
		Status     string            `json:"status"`
		DurationMs int64             `json:"duration_ms"`
		Stats      model.ScanStats   `json:"stats"`
		Phases     []PhaseResult     `json:"phases"`
	}

	out := jsonResult{
		ScanID:     result.ScanID,
		Target:     result.Target,
		Type:       "full",
		Status:     "completed",
		DurationMs: result.Duration.Milliseconds(),
		Stats:      result.Stats,
		Phases:     result.Phases,
	}

	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling result: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}

// PrintSummary prints the findings summary table to stderr.
func PrintSummary(result *PipelineResult) {
	fmt.Fprintf(os.Stderr, "\nScan completed in %s\n", formatDuration(result.Duration))

	if result.Stats.FindingsTotal > 0 {
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "FINDINGS SUMMARY")
		fmt.Fprintf(os.Stderr, "%-12s %s\n", "Severity", "Count")
		fmt.Fprintln(os.Stderr, strings.Repeat("─", 20))
		if result.Stats.FindingsCritical > 0 {
			fmt.Fprintf(os.Stderr, "%-12s %d\n", "CRITICAL", result.Stats.FindingsCritical)
		}
		if result.Stats.FindingsHigh > 0 {
			fmt.Fprintf(os.Stderr, "%-12s %d\n", "HIGH", result.Stats.FindingsHigh)
		}
		if result.Stats.FindingsMedium > 0 {
			fmt.Fprintf(os.Stderr, "%-12s %d\n", "MEDIUM", result.Stats.FindingsMedium)
		}
		if result.Stats.FindingsLow > 0 {
			fmt.Fprintf(os.Stderr, "%-12s %d\n", "LOW", result.Stats.FindingsLow)
		}
		if result.Stats.FindingsInfo > 0 {
			fmt.Fprintf(os.Stderr, "%-12s %d\n", "INFO", result.Stats.FindingsInfo)
		}
		fmt.Fprintln(os.Stderr, strings.Repeat("─", 20))
		fmt.Fprintf(os.Stderr, "%-12s %d\n", "TOTAL", result.Stats.FindingsTotal)
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Use `surfbot findings list` to see details.")
		fmt.Fprintln(os.Stderr, "Use `surfbot findings show <id>` for full evidence.")
	}
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	m := int(d.Minutes())
	s := int(d.Seconds()) % 60
	return fmt.Sprintf("%dm%02ds", m, s)
}

package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/fatih/color"
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
	DiffSummary   *ChangeSummary
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

	// Snapshot pre-scan assets for diff computation
	// Check if there are previous scans (ListScans returns DESC by created_at,
	// and we just created our scan, so if there are 2+ we have a previous one)
	preScans, _ := p.store.ListScans(ctx, targetID, 2)
	isFirstScan := len(preScans) <= 1
	preAssets, _ := SnapshotAssets(ctx, p.store, targetID)

	// Normalize "new" → "active" before running tools
	p.store.NormalizeAssetStatuses(ctx, targetID) //nolint:errcheck

	tools := p.selectTools(opts)
	inputs := []string{target.Value}
	hostnames := []string{target.Value} // original hostnames for hostname-aware phases
	result := &PipelineResult{
		ScanID: scan.ID,
		Target: target.Value,
	}

	pp := newPipelinePrinter(os.Stderr)
	pp.progress("Scan started: %s (%s)", target.Value, opts.ScanType)

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

		// Check for external cancellation (e.g., API DELETE from another process)
		if fresh, err := p.store.GetScan(ctx, scan.ID); err == nil {
			if fresh.Status == model.ScanStatusCancelled {
				scan.Status = model.ScanStatusCancelled
				scan.Phase = "cancelled"
				nowt := time.Now().UTC()
				scan.FinishedAt = &nowt
				p.store.UpdateScan(context.Background(), scan) //nolint:errcheck
				pp.warn("Scan cancelled externally")
				return nil, fmt.Errorf("scan cancelled")
			}
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

		pp.success("Phase %d/%d: %s — %s", i+1, len(tools), tool.Phase(), tool.Name())

		// Per-phase timeout — assessment (nuclei) gets 10 min, others 5 min
		phaseTimeout := time.Duration(opts.Timeout) * time.Second
		if phaseTimeout == 0 {
			if tool.Phase() == "assessment" {
				phaseTimeout = 10 * time.Minute
			} else {
				phaseTimeout = 5 * time.Minute
			}
		}
		phaseCtx, phaseCancel := context.WithTimeout(ctx, phaseTimeout)

		// Poll DB for external cancellation while the tool runs.
		// When detected, phaseCancel() kills the tool subprocess.
		cancelDone := make(chan struct{})
		go func() {
			defer close(cancelDone)
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-phaseCtx.Done():
					return
				case <-ticker.C:
					if fresh, err := p.store.GetScan(context.Background(), scan.ID); err == nil {
						if fresh.Status == model.ScanStatusCancelled {
							pp.warn("Scan cancelled externally — killing %s", tool.Name())
							phaseCancel()
							return
						}
					}
				}
			}
		}()

		runOpts := detection.RunOptions{
			RateLimit: opts.RateLimit,
			Timeout:   opts.Timeout,
		}
		// Pass scan type to nuclei as profile hint
		if tool.Phase() == "assessment" && opts.ScanType == model.ScanTypeQuick {
			if runOpts.ExtraArgs == nil {
				runOpts.ExtraArgs = map[string]string{}
			}
			runOpts.ExtraArgs["profile"] = "fast"
		}

		startTime := time.Now()
		toolResult, toolErr := tool.Run(phaseCtx, inputs, runOpts)
		duration := time.Since(startTime)
		phaseCancel()
		<-cancelDone // wait for poll goroutine to exit

		// If the tool was killed by external cancellation, stop immediately
		if toolErr != nil {
			if fresh, fErr := p.store.GetScan(context.Background(), scan.ID); fErr == nil && fresh.Status == model.ScanStatusCancelled {
				p.recordToolRun(ctx, tool, scan.ID, startTime, duration, len(inputs), 0, model.ToolRunFailed, "cancelled")
				scan.Status = model.ScanStatusCancelled
				scan.Phase = "cancelled"
				nowt := time.Now().UTC()
				scan.FinishedAt = &nowt
				p.store.UpdateScan(context.Background(), scan) //nolint:errcheck
				pp.warn("Scan cancelled — %s terminated", tool.Name())
				return nil, fmt.Errorf("scan cancelled")
			}
		}

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
			pp.warn("    %s failed: %v", tool.Name(), toolErr)
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
				pp.warn("    %d findings failed to persist (first error: %v)", errCount, firstErr)
			}
			if persisted > 0 && persisted < len(toolResult.Findings) {
				pp.muted("    Persisted %d/%d findings (duplicates merged)\n", persisted, len(toolResult.Findings))
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
				pp.warn("    No subdomains found, using root domain as target")
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

	// Diff phase: compute changes before marking scan as completed
	scan.Phase = "diffing"
	p.store.UpdateScan(ctx, scan) //nolint:errcheck

	postAssets, _ := SnapshotAssets(ctx, p.store, targetID)
	changes := ComputeChanges(targetID, scan.ID, preAssets, postAssets, isFirstScan)
	newFindingCount, resolvedFindingCount := 0, 0

	for i := range changes {
		p.store.CreateAssetChange(ctx, &changes[i]) //nolint:errcheck
	}
	ApplyStatusChanges(ctx, p.store, changes) //nolint:errcheck

	newFindings, resolvedFindings, findingErr := ComputeFindingChanges(ctx, p.store, targetID, scan.ID)
	if findingErr == nil {
		AutoResolveFindings(ctx, p.store, resolvedFindings) //nolint:errcheck
		newFindingCount = len(newFindings)
		resolvedFindingCount = len(resolvedFindings)
	}

	summary := BuildChangeSummary(changes, newFindingCount, resolvedFindingCount)
	result.DiffSummary = &summary
	PrintChangeSummary(summary)

	// Complete scan
	scan.Status = model.ScanStatusCompleted
	scan.Phase = "completed"
	scan.Progress = 100
	finishedAt := time.Now().UTC()
	scan.FinishedAt = &finishedAt
	p.store.UpdateScan(ctx, scan) //nolint:errcheck
	p.store.UpdateTargetLastScan(ctx, scan.TargetID, scan.ID, finishedAt) //nolint:errcheck

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

func dedup(ss []string) []string {
	seen := make(map[string]bool, len(ss))
	var out []string
	for _, s := range ss {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// isIPBasedURL checks if a URL uses a raw IP instead of a hostname.
func isIPBasedURL(u string) bool {
	// Strip protocol
	host := u
	if idx := strings.Index(host, "://"); idx >= 0 {
		host = host[idx+3:]
	}
	// Strip port
	if idx := strings.LastIndex(host, ":"); idx >= 0 {
		host = host[:idx]
	}
	// Strip path
	if idx := strings.Index(host, "/"); idx >= 0 {
		host = host[:idx]
	}
	return net.ParseIP(host) != nil
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
		// Deduplicate URLs for nuclei: prefer hostname-based, one per scheme.
		// httpx produces many URLs (each IP × protocol) but nuclei only needs
		// the unique hostname+scheme combinations.
		seen := make(map[string]bool)
		var hostnameURLs, ipURLs []string
		for _, a := range result.Assets {
			if a.Type != model.AssetTypeURL {
				continue
			}
			if seen[a.Value] {
				continue
			}
			seen[a.Value] = true
			if isIPBasedURL(a.Value) {
				ipURLs = append(ipURLs, a.Value)
			} else {
				hostnameURLs = append(hostnameURLs, a.Value)
			}
		}
		if len(hostnameURLs) > 0 {
			return hostnameURLs
		}
		// If no hostname URLs, keep only unique IP:port combos (max 1 per IP)
		return dedup(ipURLs)

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
	pp := newPipelinePrinter(os.Stderr)
	switch tool.Phase() {
	case "discovery":
		count := 0
		for _, a := range result.Assets {
			if a.Type == model.AssetTypeSubdomain {
				count++
			}
		}
		pp.muted("    Found %d subdomains\n", count)
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
		pp.muted("    Resolved %d IPs (%d IPv4, %d IPv6)\n", ipv4+ipv6, ipv4, ipv6)
	case "port_scan":
		hosts := make(map[string]bool)
		for _, a := range result.Assets {
			if a.Type == model.AssetTypePort && a.ParentID != "" {
				hosts[a.ParentID] = true
			}
		}
		pp.muted("    Scanned hosts, found %d open ports\n", len(result.Assets))
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
		pp.muted("    Probed endpoints, %d live (%d technologies detected)\n", urls, techs)
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
		pp.muted("    Scanned %d URLs, found %d findings (%d critical, %d high, %d medium, %d low, %d info)\n",
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

// PrintSummary prints the findings summary table to stderr with colored severity.
func PrintSummary(result *PipelineResult) {
	pp := newPipelinePrinter(os.Stderr)

	pp.theme.Success.Fprintf(os.Stderr, "\nScan completed in %s\n", formatDuration(result.Duration))

	if result.Stats.FindingsTotal > 0 {
		pp.theme.Bold.Fprintf(os.Stderr, "\nFINDINGS SUMMARY\n")

		w := tabwriter.NewWriter(os.Stderr, 0, 0, 3, ' ', 0)
		pp.theme.Bold.Fprintln(w, "Severity\tCount")
		pp.theme.Muted.Fprintln(w, strings.Repeat("─", 20)+"\t")

		printSeverityRow(w, pp.theme.Critical, "CRITICAL", result.Stats.FindingsCritical, pp.theme.Muted)
		printSeverityRow(w, pp.theme.High, "HIGH", result.Stats.FindingsHigh, pp.theme.Muted)
		printSeverityRow(w, pp.theme.Medium, "MEDIUM", result.Stats.FindingsMedium, pp.theme.Muted)
		printSeverityRow(w, pp.theme.Low, "LOW", result.Stats.FindingsLow, pp.theme.Muted)
		printSeverityRow(w, pp.theme.Info, "INFO", result.Stats.FindingsInfo, pp.theme.Muted)

		pp.theme.Muted.Fprintln(w, strings.Repeat("─", 20)+"\t")
		pp.theme.Bold.Fprintf(w, "%-12s\t%d\n", "TOTAL", result.Stats.FindingsTotal)
		w.Flush()

		fmt.Fprintln(os.Stderr, "")
		pp.theme.Muted.Fprintln(os.Stderr, "Use `surfbot findings list` to see details.")
		pp.theme.Muted.Fprintln(os.Stderr, "Use `surfbot findings show <id>` for full evidence.")
	}
}

func printSeverityRow(w io.Writer, sevColor *color.Color, label string, count int, muted *color.Color) {
	if count > 0 {
		sevColor.Fprintf(w, "%-12s\t%d\n", label, count)
	} else {
		muted.Fprintf(w, "%-12s\t0\n", label)
	}
}

// pipelinePrinter provides colored output helpers for the pipeline package.
// This is a lightweight wrapper (not exported) to keep fatih/color usage
// local to the pipeline package without importing cli.
type pipelinePrinter struct {
	w     io.Writer
	theme *pipelineTheme
}

type pipelineTheme struct {
	Critical *color.Color
	High     *color.Color
	Medium   *color.Color
	Low      *color.Color
	Info     *color.Color
	Success  *color.Color
	Warning  *color.Color
	Error    *color.Color
	Progress *color.Color
	Muted    *color.Color
	Bold     *color.Color
}

func newPipelinePrinter(w io.Writer) *pipelinePrinter {
	return &pipelinePrinter{
		w: w,
		theme: &pipelineTheme{
			Critical: color.New(color.FgRed, color.Bold),
			High:     color.New(color.FgRed),
			Medium:   color.New(color.FgYellow),
			Low:      color.New(color.FgBlue),
			Info:     color.New(color.Faint),
			Success:  color.RGB(0, 229, 153), // Surfbot Signal Green #00E599
			Warning:  color.New(color.FgYellow),
			Error:    color.New(color.FgRed, color.Bold),
			Progress: color.New(color.FgCyan),
			Muted:    color.New(color.Faint),
			Bold:     color.New(color.Bold),
		},
	}
}

func (p *pipelinePrinter) progress(format string, args ...interface{}) {
	p.theme.Progress.Fprintf(p.w, "[*] ")
	fmt.Fprintf(p.w, format+"\n", args...)
}

func (p *pipelinePrinter) success(format string, args ...interface{}) {
	p.theme.Success.Fprintf(p.w, "[+] ")
	fmt.Fprintf(p.w, format+"\n", args...)
}

func (p *pipelinePrinter) warn(format string, args ...interface{}) {
	p.theme.Warning.Fprintf(p.w, "[!] ")
	fmt.Fprintf(p.w, format+"\n", args...)
}

func (p *pipelinePrinter) errorf(format string, args ...interface{}) {
	p.theme.Error.Fprintf(p.w, "[✗] ")
	fmt.Fprintf(p.w, format+"\n", args...)
}

func (p *pipelinePrinter) muted(format string, args ...interface{}) {
	p.theme.Muted.Fprintf(p.w, format, args...)
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	m := int(d.Minutes())
	s := int(d.Seconds()) % 60
	return fmt.Sprintf("%dm%02ds", m, s)
}

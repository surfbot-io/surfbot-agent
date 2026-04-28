package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"sort"
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
//
// SCHED1.2c: ToolConfig carries the per-schedule typed tool params
// resolved by scanRunner from a Schedule's EffectiveConfig. The pipeline
// pre-unmarshals each registered tool's payload into the matching
// detection.RunOptions.<Tool>Params pointer; tools then read their typed
// field, falling back to model.DefaultXxxParams() per-field. ToolConfig
// is optional — when nil/empty the pipeline preserves pre-1.2c behavior.
type PipelineOptions struct {
	ScanType   model.ScanType   // full|quick|discovery (default: full)
	Tools      []string         // Specific tools to run (empty = all available)
	RateLimit  int              // Global rate limit (0 = per-tool defaults)
	Timeout    int              // Per-phase timeout in seconds (0 = 300s default)
	ToolConfig model.ToolConfig // SCHED1.2c per-tool typed params
}

// PipelineResult holds the output of a pipeline execution.
//
// The three aggregate fields (TargetState, Delta, Work) are populated by the
// finalize* functions at end-of-scan. They mirror the three blobs persisted
// to scans.target_state / scans.delta / scans.work. Consumers should read
// these rather than recomputing counts from Phases.
type PipelineResult struct {
	ScanID      string
	Target      string
	Type        model.ScanType
	Duration    time.Duration
	TargetState model.TargetState
	Delta       model.ScanDelta
	Work        model.ScanWork
	Phases      []PhaseResult
}

// applyToolConfig pre-unmarshals the per-tool entry from the
// per-schedule ToolConfig into the matching typed pointer on
// RunOptions. Unknown tool names and unmarshal failures fall through
// silently — each tool is responsible for its own default fallback via
// resolveXxxParams, so a malformed payload just leaves RunOptions
// untouched and the tool runs with defaults.
func applyToolConfig(opts *detection.RunOptions, toolName string, tc model.ToolConfig) {
	raw, ok := tc[toolName]
	if !ok || len(raw) == 0 {
		return
	}
	switch toolName {
	case "nuclei":
		var p model.NucleiParams
		if err := json.Unmarshal(raw, &p); err == nil {
			opts.NucleiParams = &p
		}
	case "naabu":
		var p model.NaabuParams
		if err := json.Unmarshal(raw, &p); err == nil {
			opts.NaabuParams = &p
		}
	case "httpx":
		var p model.HttpxParams
		if err := json.Unmarshal(raw, &p); err == nil {
			opts.HttpxParams = &p
		}
	case "subfinder":
		var p model.SubfinderParams
		if err := json.Unmarshal(raw, &p); err == nil {
			opts.SubfinderParams = &p
		}
	case "dnsx":
		var p model.DnsxParams
		if err := json.Unmarshal(raw, &p); err == nil {
			opts.DnsxParams = &p
		}
	}
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
	ipToHostname := map[string]string{} // primary hostname per resolved IP (SUR-242)

	// resolvedFilterDropped tracks how many discovery hostnames the
	// resolution-evidence filter removed before the port_scan handoff.
	// -1 is the "filter did not run" sentinel (resolution phase hasn't
	// fired yet, or was skipped for this scan_type). Consumed by the
	// port_scan phase-summary line so operators can see the filter at
	// work in the scan log (SPEC-SCAN-PIPELINE-FIX R2).
	resolvedFilterDropped := -1
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
		applyToolConfig(&runOpts, tool.Name(), opts.ToolConfig)

		startTime := time.Now()
		toolResult, toolErr := tool.Run(phaseCtx, inputs, runOpts)
		duration := time.Since(startTime)
		phaseCancel()
		<-cancelDone // wait for poll goroutine to exit

		// If the tool was killed by external cancellation, stop immediately
		if toolErr != nil {
			if fresh, fErr := p.store.GetScan(context.Background(), scan.ID); fErr == nil && fresh.Status == model.ScanStatusCancelled {
				p.recordToolRun(ctx, tool, scan.ID, startTime, duration, len(inputs), 0, model.ToolRunFailed, "canceled")
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

		// Per-scan stats are no longer accumulated here — they are computed
		// from the database at end-of-scan via FinalizeTargetState /
		// FinalizeScanDelta / FinalizeScanWork. See SPEC-QA3.

		outputCount := len(toolResult.Assets) + len(toolResult.Findings)
		pr.Status = "completed"
		pr.OutputCount = outputCount
		result.Phases = append(result.Phases, pr)

		// tool_runs.findings_count is the count of FINDINGS emitted by this
		// tool (pre-storage-dedup). Not the output total — assets and
		// findings are counted separately in the data model.
		//
		// Detection wrappers may populate RunResult.ToolRun with rich
		// telemetry (command args, stderr tail, output summary, exit
		// code). Merge those into the persisted record so the webui and
		// `surfbot scan detail` can show per-tool logs.
		p.recordToolRun(ctx, tool, scan.ID, startTime, duration, len(inputs), len(toolResult.Findings), model.ToolRunCompleted, "", &toolResult.ToolRun)

		// Print phase summary. The port_scan summary surfaces the
		// resolution-filter drop count so operators can see the
		// SPEC-SCAN-PIPELINE-FIX filter at work (R2).
		printPhaseSummary(tool, toolResult, phaseSummaryExtras{
			resolvedFilterDropped: resolvedFilterDropped,
		})

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

		// Build ip→hostname map from resolution output so http_probe can
		// send Host headers matching the user's target scope (SUR-242).
		if tool.Phase() == "resolution" {
			for _, a := range toolResult.Assets {
				if a.Type != model.AssetTypeIPv4 && a.Type != model.AssetTypeIPv6 {
					continue
				}
				if _, dup := ipToHostname[a.Value]; dup {
					continue
				}
				if h, ok := a.Metadata["resolved_from"].(string); ok && h != "" {
					ipToHostname[a.Value] = h
				}
			}

			// Narrow `hostnames` to entries that dnsx actually resolved,
			// so port_scan (naabu) does not waste a dial() per unresolvable
			// subfinder output. See SPEC-SCAN-PIPELINE-FIX: without this,
			// a noisy subfinder run (e.g. 29k abuse-record subdomains for
			// a popular public domain) hands every one of those to naabu
			// and starves the assessment phase inside the scan timeout.
			//
			// Fallback: when the resolution phase emitted no resolved
			// hostnames (either dnsx genuinely found nothing, or its
			// output lacked the resolved_from metadata), keep the full
			// hostname list — the pre-existing hedge for handing naabu
			// SOMETHING rather than nothing.
			filtered, dropped := narrowHostnamesByResolution(hostnames, toolResult)
			if filtered != nil {
				hostnames = filtered
			}
			resolvedFilterDropped = dropped
		}

		// For http_probe: pair each ip:port with its resolved hostname
		// (hostname|ip:port/tcp), and also keep bare hostnames for the
		// CDN fallback path where DNS resolution happens client-side.
		if tool.Phase() == "port_scan" {
			nextInputs = enrichHostports(nextInputs, ipToHostname)
		}
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
	var newFindings, resolvedFindings []model.Finding

	for i := range changes {
		p.store.CreateAssetChange(ctx, &changes[i]) //nolint:errcheck
	}
	ApplyStatusChanges(ctx, p.store, changes) //nolint:errcheck

	var findingErr error
	newFindings, resolvedFindings, findingErr = ComputeFindingChanges(ctx, p.store, targetID, scan.ID)
	if findingErr == nil {
		AutoResolveFindings(ctx, p.store, resolvedFindings) //nolint:errcheck
	}

	// Finalize aggregates from DB ground truth. Order matters for logging:
	// delta first (cheap, depends on already-written asset_changes), then
	// target_state (queries the live tables), then work (reads tool_runs).
	finishedAt := time.Now().UTC()
	duration := finishedAt.Sub(*scan.StartedAt)

	delta, err := FinalizeScanDelta(ctx, p.store, scan.ID, newFindings, resolvedFindings)
	if err != nil {
		pp.warn("finalize delta: %v", err)
	}
	targetState, err := FinalizeTargetState(ctx, p.store, targetID)
	if err != nil {
		pp.warn("finalize target_state: %v", err)
	}
	work, err := FinalizeScanWork(ctx, p.store, scan.ID, duration)
	if err != nil {
		pp.warn("finalize work: %v", err)
	}

	scan.TargetState = targetState
	scan.Delta = delta
	scan.Work = work
	scan.Status = model.ScanStatusCompleted
	scan.Phase = "completed"
	scan.Progress = 100
	scan.FinishedAt = &finishedAt
	p.store.UpdateScan(ctx, scan)                                         //nolint:errcheck
	p.store.UpdateTargetLastScan(ctx, scan.TargetID, scan.ID, finishedAt) //nolint:errcheck

	result.Type = opts.ScanType
	result.Duration = duration
	result.TargetState = targetState
	result.Delta = delta
	result.Work = work

	PrintChangeSummary(delta)

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

// recordToolRun persists a tool_run row. The optional `enrich` argument is
// the ToolRun returned by the tool wrapper — it carries telemetry the
// pipeline can't infer from outside (command args, output_summary, stderr
// tail, tool-specific config). When present, its Config map and
// OutputSummary are merged into the persisted record so the webui can
// show per-tool logs without additional round-trips to the subprocess.
//
// The pipeline-controlled fields (scan_id, timing, counts, status)
// always win — the wrapper doesn't know scan_id and its own timing may
// be slightly off vs. the outer timer.
func (p *Pipeline) recordToolRun(ctx context.Context, tool detection.DetectionTool, scanID string, startedAt time.Time, duration time.Duration, inputCount, outputCount int, status model.ToolRunStatus, errMsg string, enrich ...*model.ToolRun) {
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
	if len(enrich) > 0 && enrich[0] != nil {
		src := enrich[0]
		tr.OutputSummary = src.OutputSummary
		// Wrapper's ErrorMessage wins when it's non-empty and the outer
		// caller didn't pass one. This lets wrappers surface partial-run
		// warnings without being classified as failed.
		if tr.ErrorMessage == "" && src.ErrorMessage != "" {
			tr.ErrorMessage = src.ErrorMessage
		}
		// Wrapper's Status wins when it reports a non-success terminal
		// state (skipped / failed / timeout). The outer caller can only
		// infer "completed" from the absence of a Go error — the wrapper
		// knows when e.g. the binary is missing or the SDK init failed
		// and had to skip. Without this merge, subfinder-not-in-PATH
		// appears as "completed" in the UI.
		switch src.Status {
		case model.ToolRunSkipped, model.ToolRunFailed, model.ToolRunTimeout:
			tr.Status = src.Status
		}
		for k, v := range src.Config {
			tr.Config[k] = v
		}
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

// enrichHostports rewrites "ip:port/tcp" inputs as "hostname|ip:port/tcp" when
// the IP has a known resolved hostname. Entries whose IP is unknown to the map
// pass through unchanged (IP-pure probing). See SUR-242.
func enrichHostports(hostports []string, ipToHostname map[string]string) []string {
	if len(ipToHostname) == 0 {
		return hostports
	}
	out := make([]string, 0, len(hostports))
	for _, hp := range hostports {
		ip := ipFromHostport(hp)
		if ip == "" {
			out = append(out, hp)
			continue
		}
		if h, ok := ipToHostname[ip]; ok && h != "" {
			out = append(out, h+"|"+hp)
			continue
		}
		out = append(out, hp)
	}
	return out
}

// ipFromHostport extracts the IP from an "ip:port[/tcp]" string. Supports
// bracketed IPv6 literals. Returns "" on parse failure.
func ipFromHostport(hp string) string {
	body := strings.TrimSuffix(hp, "/tcp")
	if strings.HasPrefix(body, "[") {
		close := strings.LastIndex(body, "]")
		if close < 0 {
			return ""
		}
		return body[1:close]
	}
	idx := strings.LastIndex(body, ":")
	if idx < 0 {
		return ""
	}
	return body[:idx]
}

// narrowHostnamesByResolution returns the subset of `hostnames` that
// produced at least one resolved IP during the resolution phase, based
// on the `resolved_from` metadata on the phase's emitted IP assets. The
// second return is the count of hostnames dropped by the filter.
//
// When the resolution phase emits no hostname evidence (no IP assets,
// or none carrying a resolved_from tag), the function returns nil for
// the filtered slice and 0 for the drop count — callers treat nil as
// "keep the original list" so the naabu fallback hedge stays intact.
//
// Ordering of the surviving hostnames is preserved from the input
// slice; duplicates are not deduped here because `mergeHostnames`
// (downstream) already dedupes against the port_scan input list.
func narrowHostnamesByResolution(hostnames []string, result *detection.RunResult) ([]string, int) {
	if result == nil {
		return nil, 0
	}
	resolved := map[string]bool{}
	for _, a := range result.Assets {
		if a.Type != model.AssetTypeIPv4 && a.Type != model.AssetTypeIPv6 {
			continue
		}
		h, ok := a.Metadata["resolved_from"].(string)
		if !ok || h == "" {
			continue
		}
		resolved[h] = true
	}
	if len(resolved) == 0 {
		return nil, 0
	}
	out := make([]string, 0, len(hostnames))
	dropped := 0
	for _, h := range hostnames {
		if resolved[h] {
			out = append(out, h)
			continue
		}
		dropped++
	}
	return out, dropped
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
		// Skip ports that completed the handshake but returned no data
		// (status=filtered). These are typically load balancer SYN-ACK
		// responders with nothing behind them; probing them wastes the
		// 10s httpx timeout per scheme. See SPEC-QA2 R9.
		var hostports []string
		for _, a := range result.Assets {
			if a.Type != model.AssetTypePort {
				continue
			}
			if status, ok := a.Metadata["status"].(string); ok && status == "filtered" {
				continue
			}
			hostports = append(hostports, a.Value)
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

// phaseSummaryExtras carries side-channel telemetry into
// printPhaseSummary for fields that aren't derivable from the tool
// result alone. resolvedFilterDropped = -1 means "the filter did not
// run / the phase does not consume it" — only the port_scan case
// actually reads it today.
type phaseSummaryExtras struct {
	resolvedFilterDropped int
}

func printPhaseSummary(tool detection.DetectionTool, result *detection.RunResult, extras phaseSummaryExtras) {
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
		open, filtered := 0, 0
		for _, a := range result.Assets {
			if a.Type != model.AssetTypePort {
				continue
			}
			if a.ParentID != "" {
				hosts[a.ParentID] = true
			}
			if status, ok := a.Metadata["status"].(string); ok && status == "filtered" {
				filtered++
			} else {
				open++
			}
		}
		resolvedFilter := ""
		if extras.resolvedFilterDropped >= 0 {
			resolvedFilter = fmt.Sprintf(" resolved_filter=%d", extras.resolvedFilterDropped)
		}
		if filtered > 0 {
			pp.muted("    Scanned hosts, found %d open ports, %d filtered%s\n", open, filtered, resolvedFilter)
		} else {
			pp.muted("    Scanned hosts, found %d open ports%s\n", open, resolvedFilter)
		}
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
		// Pre-dedup emission count. The final per-scan findings total is
		// reported at the end from the DB (see PrintSummary) — this phase
		// line just tells the operator what nuclei emitted right now.
		pp.muted("    Emitted %d findings (%d critical, %d high, %d medium, %d low, %d info)\n",
			total, crit, high, med, low, info)
	}
}

// JSONResult is the canonical JSON shape written by WriteJSONResult. Exposed
// so consumers (webui, CI helpers) can share the type instead of re-declaring
// it. Mirrors the scan.target_state / scan.delta / scan.work model exactly.
type JSONResult struct {
	ScanID      string            `json:"scan_id"`
	Target      string            `json:"target"`
	Type        string            `json:"type"`
	Status      string            `json:"status"`
	DurationMs  int64             `json:"duration_ms"`
	TargetState model.TargetState `json:"target_state"`
	Delta       model.ScanDelta   `json:"delta"`
	Work        model.ScanWork    `json:"work"`
	Phases      []PhaseResult     `json:"phases"`
}

// WriteJSONResult writes the pipeline result as JSON to the given path.
// Shape version: agent-spec 2.0 (see docs/agent-spec.md).
func WriteJSONResult(result *PipelineResult, path string) error {
	scanType := string(result.Type)
	if scanType == "" {
		scanType = string(model.ScanTypeFull)
	}
	out := JSONResult{
		ScanID:      result.ScanID,
		Target:      result.Target,
		Type:        scanType,
		Status:      "completed",
		DurationMs:  result.Duration.Milliseconds(),
		TargetState: result.TargetState,
		Delta:       result.Delta,
		Work:        result.Work,
		Phases:      result.Phases,
	}

	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling result: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}

// PrintSummary prints a three-section human summary to stderr:
//
//	TARGET STATE — what currently exists on the target
//	CHANGES      — what this scan changed (printed separately via PrintChangeSummary)
//	WORK         — telemetry of the execution
//
// The CHANGES block is emitted earlier in the flow by PrintChangeSummary.
// PrintSummary handles state + findings table + work.
func PrintSummary(result *PipelineResult) {
	pp := newPipelinePrinter(os.Stderr)

	pp.theme.Success.Fprintf(os.Stderr, "\nScan completed in %s\n", formatDuration(result.Duration))

	printTargetStateBlock(pp, result.TargetState)
	printFindingsBlock(pp, result.TargetState)
	printWorkBlock(pp, result.Work)
}

//nolint:errcheck // stderr writes are unrecoverable and not actionable
func printTargetStateBlock(pp *pipelinePrinter, state model.TargetState) {
	if state.AssetsTotal == 0 && state.FindingsOpenTotal == 0 {
		return
	}

	pp.theme.Bold.Fprintf(os.Stderr, "\nTARGET STATE\n")

	// Render asset counts in a stable order. Known types first in pipeline
	// order; any unknown types (from tools that aren't built-in) follow
	// alphabetically so the output is deterministic.
	known := []model.AssetType{
		model.AssetTypeSubdomain, model.AssetTypeIPv4, model.AssetTypeIPv6,
		model.AssetTypePort, model.AssetTypeURL, model.AssetTypeTechnology,
		model.AssetTypeDomain, model.AssetTypeService,
	}
	seen := make(map[model.AssetType]bool, len(known))
	parts := make([]string, 0, len(state.AssetsByType))
	for _, t := range known {
		if n, ok := state.AssetsByType[t]; ok && n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, pluralize(string(t), n)))
			seen[t] = true
		}
	}
	extras := make([]string, 0)
	for t, n := range state.AssetsByType {
		if !seen[t] && n > 0 {
			extras = append(extras, fmt.Sprintf("%d %s", n, pluralize(string(t), n)))
		}
	}
	sort.Strings(extras)
	parts = append(parts, extras...)

	if len(parts) > 0 {
		pp.muted("%s", "  "+strings.Join(parts, " · ")+"\n")
	}

	// Ports detail (open/filtered/…) when any ports exist.
	if len(state.PortsByStatus) > 0 {
		portParts := make([]string, 0, len(state.PortsByStatus))
		statusOrder := []string{"open", "filtered", "closed", "unknown"}
		seenStatus := make(map[string]bool)
		for _, st := range statusOrder {
			if n := state.PortsByStatus[st]; n > 0 {
				portParts = append(portParts, fmt.Sprintf("%d %s", n, st))
				seenStatus[st] = true
			}
		}
		extraStatus := make([]string, 0)
		for st, n := range state.PortsByStatus {
			if !seenStatus[st] && n > 0 {
				extraStatus = append(extraStatus, fmt.Sprintf("%d %s", n, st))
			}
		}
		sort.Strings(extraStatus)
		portParts = append(portParts, extraStatus...)
		if len(portParts) > 0 {
			pp.muted("%s", "  ports: "+strings.Join(portParts, ", ")+"\n")
		}
	}
}

//nolint:errcheck // all terminal writes below are unrecoverable and not actionable
func printFindingsBlock(pp *pipelinePrinter, state model.TargetState) {
	if state.FindingsOpenTotal == 0 {
		return
	}

	pp.theme.Bold.Fprintf(os.Stderr, "\nFINDINGS (open)\n")

	w := tabwriter.NewWriter(os.Stderr, 0, 0, 3, ' ', 0)
	pp.theme.Bold.Fprintln(w, "Severity\tCount")
	pp.theme.Muted.Fprintln(w, strings.Repeat("─", 20)+"\t")

	printSeverityRow(w, pp.theme.Critical, "CRITICAL", state.FindingsOpen[model.SeverityCritical], pp.theme.Muted)
	printSeverityRow(w, pp.theme.High, "HIGH", state.FindingsOpen[model.SeverityHigh], pp.theme.Muted)
	printSeverityRow(w, pp.theme.Medium, "MEDIUM", state.FindingsOpen[model.SeverityMedium], pp.theme.Muted)
	printSeverityRow(w, pp.theme.Low, "LOW", state.FindingsOpen[model.SeverityLow], pp.theme.Muted)
	printSeverityRow(w, pp.theme.Info, "INFO", state.FindingsOpen[model.SeverityInfo], pp.theme.Muted)

	pp.theme.Muted.Fprintln(w, strings.Repeat("─", 20)+"\t")
	pp.theme.Bold.Fprintf(w, "%-12s\t%d\n", "TOTAL", state.FindingsOpenTotal)
	w.Flush()

	fmt.Fprintln(os.Stderr, "")
	pp.theme.Muted.Fprintln(os.Stderr, "Use `surfbot findings list` to see details.")
	pp.theme.Muted.Fprintln(os.Stderr, "Use `surfbot findings show <id>` for full evidence.")
}

//nolint:errcheck // stderr writes are unrecoverable and not actionable
func printWorkBlock(pp *pipelinePrinter, work model.ScanWork) {
	if work.ToolsRun == 0 {
		return
	}
	pp.theme.Bold.Fprintf(os.Stderr, "\nWORK\n")
	details := fmt.Sprintf("  %d %s run", work.ToolsRun, pluralize("tool", work.ToolsRun))
	if work.ToolsFailed > 0 {
		details += fmt.Sprintf(", %d failed", work.ToolsFailed)
	}
	if work.ToolsSkipped > 0 {
		details += fmt.Sprintf(", %d skipped", work.ToolsSkipped)
	}
	if work.RawEmissions > 0 {
		details += fmt.Sprintf(" · %d raw emissions (pre-dedup)", work.RawEmissions)
	}
	pp.muted("%s\n", details)
	if len(work.PhasesRun) > 0 {
		pp.muted("%s", "  phases: "+strings.Join(work.PhasesRun, " → ")+"\n")
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

# SPEC: N6 — Asset Discovery Pipeline

**For:** Claude Code implementation
**Author:** Product Officer (Claude)
**Date:** 2026-03-31
**Scope:** SUR-166 (N6) — Connect 5 detection tools into `surfbot scan example.com`
**Depends on:** N1+N2 (scaffold), N3+N5 (storage + models), N4 (detection tools)
**Go version:** 1.24+
**Reference:** surfbot-api `internal/scanner/orchestrator.go` (903 lines) — port orchestration patterns

---

## What This Spec Delivers

After implementation, a user can run:

```bash
$ surfbot scan example.com
```

And get:

```
[*] Scan started: example.com (full)
[+] Phase 1/5: discovery — subfinder
    Found 87 subdomains
[+] Phase 2/5: resolution — dnsx
    Resolved 42 IPs (34 IPv4, 8 IPv6)
[+] Phase 3/5: port_scan — naabu
    Scanned 42 hosts, found 156 open ports
[+] Phase 4/5: http_probe — httpx
    Probed 156 endpoints, 89 live (12 technologies detected)
[+] Phase 5/5: assessment — nuclei
    Scanned 89 URLs, found 23 findings (2 critical, 5 high, 8 medium, 4 low, 4 info)

Scan completed in 2m34s

FINDINGS SUMMARY
Severity    Count
─────────────────
CRITICAL    2
HIGH        5
MEDIUM      8
LOW         4
INFO        4
─────────────────
TOTAL       23

Use `surfbot findings list` to see details.
Use `surfbot findings show <id>` for full evidence.
```

---

## 1. Pipeline Orchestrator

**File:** `internal/pipeline/pipeline.go`

Replace the current stub with a full implementation.

```go
type Pipeline struct {
    store    storage.Store
    registry *detection.Registry
}

func New(store storage.Store, registry *detection.Registry) *Pipeline

func (p *Pipeline) Run(ctx context.Context, targetID string, opts PipelineOptions) (*PipelineResult, error)
```

### PipelineOptions

```go
type PipelineOptions struct {
    ScanType  model.ScanType // full|quick|discovery (default: full)
    Tools     []string       // Specific tools to run (empty = all available)
    RateLimit int            // Global rate limit (0 = per-tool defaults)
    Timeout   int            // Per-phase timeout in seconds (0 = 300s default)
}
```

### PipelineResult

```go
type PipelineResult struct {
    ScanID        string
    TotalAssets   int
    TotalFindings int
    Duration      time.Duration
    Stats         model.ScanStats
    Phases        []PhaseResult
}

type PhaseResult struct {
    ToolName     string
    Phase        string
    Status       string // completed|failed|skipped
    Duration     time.Duration
    InputCount   int
    OutputCount  int
    Error        string // empty if success
}
```

---

## 2. Pipeline.Run — Step by Step

### 2.1 Initialize scan

```go
scan := &model.Scan{
    ID:        uuid.New().String(),
    TargetID:  targetID,
    Type:      opts.ScanType,
    Status:    model.ScanStatusRunning,
    Phase:     "initializing",
    Progress:  0,
    StartedAt: timePtr(time.Now()),
}
p.store.CreateScan(ctx, scan)
```

### 2.2 Resolve target

```go
target, err := p.store.GetTarget(ctx, targetID)
// target.Value = "example.com"
// This is the seed input for Phase 1
```

### 2.3 Execute phases sequentially

The pipeline executes tools in registry order. Each phase receives output from the previous phase as input.

```go
var inputs []string = []string{target.Value} // seed

for i, tool := range p.registry.AvailableTools() {
    // Skip if scan type doesn't include this phase
    if shouldSkip(tool, opts) {
        recordSkippedToolRun(...)
        continue
    }

    // Update scan phase + progress
    scan.Phase = tool.Phase()
    scan.Progress = float32(i) / float32(len(tools)) * 100
    p.store.UpdateScan(ctx, scan)

    // Print phase header
    fmt.Printf("[+] Phase %d/%d: %s — %s\n", i+1, len(tools), tool.Phase(), tool.Name())

    // Build RunOptions
    runOpts := detection.RunOptions{
        RateLimit: opts.RateLimit,
        Timeout:   opts.Timeout,
    }

    // Execute tool
    startTime := time.Now()
    result, err := tool.Run(ctx, inputs, runOpts)
    duration := time.Since(startTime)

    // Record tool run
    toolRun := buildToolRun(tool, scan.ID, startTime, duration, len(inputs), result, err)
    p.store.CreateToolRun(ctx, &toolRun)

    // Handle result
    if err != nil {
        if isHardFailure(tool) {
            // Discovery failure = abort
            scan.Status = model.ScanStatusFailed
            scan.Error = err.Error()
            p.store.UpdateScan(ctx, scan)
            return nil, err
        }
        // Soft failure — log and continue with empty result
        fmt.Printf("    Warning: %s failed: %v\n", tool.Name(), err)
        continue
    }

    // Persist assets
    for _, asset := range result.Assets {
        asset.TargetID = targetID
        p.store.UpsertAsset(ctx, &asset)
    }

    // Persist findings
    for _, finding := range result.Findings {
        finding.ScanID = scan.ID
        p.store.UpsertFinding(ctx, &finding)
    }

    // Update stats
    updateStats(&scan.Stats, tool.Phase(), result)

    // Print phase summary
    printPhaseSummary(tool, result, duration)

    // Thread outputs → next phase inputs
    inputs = extractInputsForNextPhase(tool.Phase(), result)
}
```

### 2.4 Complete scan

```go
scan.Status = model.ScanStatusCompleted
scan.Phase = "completed"
scan.Progress = 100
scan.FinishedAt = timePtr(time.Now())
p.store.UpdateScan(ctx, scan)
```

---

## 3. Data Threading Between Phases

This is the critical logic — each phase produces outputs that become inputs for the next phase.

```go
func extractInputsForNextPhase(phase string, result *detection.RunResult) []string {
    switch phase {
    case "discovery":
        // subfinder outputs subdomains → dnsx needs subdomains to resolve
        var subdomains []string
        for _, a := range result.Assets {
            if a.Type == model.AssetTypeSubdomain {
                subdomains = append(subdomains, a.Value)
            }
        }
        return subdomains

    case "resolution":
        // dnsx outputs IPs → naabu needs IPs to port scan
        var ips []string
        for _, a := range result.Assets {
            if a.Type == model.AssetTypeIPv4 || a.Type == model.AssetTypeIPv6 {
                ips = append(ips, a.Value)
            }
        }
        return deduplicate(ips)

    case "port_scan":
        // naabu outputs ip:port/tcp → httpx needs ip:port pairs to probe
        var hostports []string
        for _, a := range result.Assets {
            if a.Type == model.AssetTypePort {
                hostports = append(hostports, a.Value) // "10.0.0.1:443/tcp"
            }
        }
        return hostports

    case "http_probe":
        // httpx outputs URLs → nuclei needs URLs to scan for vulns
        var urls []string
        for _, a := range result.Assets {
            if a.Type == model.AssetTypeURL {
                urls = append(urls, a.Value) // "https://example.com:443"
            }
        }
        return urls

    case "assessment":
        // nuclei is the last phase — no next inputs needed
        return nil
    }
    return nil
}
```

**Edge cases:**
- If discovery returns 0 subdomains → abort scan (hard failure, nothing to resolve)
- If resolution returns 0 IPs → continue with empty port scan (soft), httpx still tries subdomains
- If port scan returns 0 open ports → httpx still tries common ports (80, 443) on each IP
- If httpx returns 0 live URLs → nuclei is skipped (nothing to assess)

---

## 4. Scan Types

```go
func shouldSkip(tool detection.DetectionTool, opts PipelineOptions) bool {
    switch opts.ScanType {
    case model.ScanTypeDiscovery:
        // Only run discovery + resolution
        return tool.Phase() != "discovery" && tool.Phase() != "resolution"
    case model.ScanTypeQuick:
        // Skip port scan (use default ports 80,443 for httpx)
        return tool.Phase() == "port_scan"
    case model.ScanTypeFull:
        // Run everything
        return false
    }
    return false
}
```

| Scan Type | Phases Run | Use Case |
|-----------|-----------|----------|
| `discovery` | subfinder → dnsx | Quick recon, just find subdomains and IPs |
| `quick` | subfinder → dnsx → httpx → nuclei (skip port scan) | Fast scan, probe only 80/443 |
| `full` | subfinder → dnsx → naabu → httpx → nuclei | Complete scan, all ports |

For `quick` type, httpx receives IPs directly (not ip:port pairs) and probes default ports (80, 443) on each IP.

---

## 5. Stats Tracking

```go
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
            if a.Type == model.AssetTypeURL {
                stats.HTTPProbed++
            }
            if a.Type == model.AssetTypeTechnology {
                stats.TechDetected++
            }
        }
    case "assessment":
        for _, f := range result.Findings {
            stats.FindingsTotal++
            switch f.Severity {
            case model.SeverityCritical: stats.FindingsCritical++
            case model.SeverityHigh:     stats.FindingsHigh++
            case model.SeverityMedium:   stats.FindingsMedium++
            case model.SeverityLow:      stats.FindingsLow++
            case model.SeverityInfo:     stats.FindingsInfo++
            }
        }
    }
}
```

**ScanStats struct** — add fields to existing model if missing:

```go
type ScanStats struct {
    SubdomainsFound  int `json:"subdomains_found"`
    IPsResolved      int `json:"ips_resolved"`
    OpenPorts        int `json:"open_ports"`
    HTTPProbed       int `json:"http_probed"`
    TechDetected     int `json:"tech_detected"`
    FindingsTotal    int `json:"findings_total"`
    FindingsCritical int `json:"findings_critical"`
    FindingsHigh     int `json:"findings_high"`
    FindingsMedium   int `json:"findings_medium"`
    FindingsLow      int `json:"findings_low"`
    FindingsInfo     int `json:"findings_info"`
}
```

---

## 6. Error Handling

### Hard failures (abort scan)

```go
func isHardFailure(tool detection.DetectionTool) bool {
    // Only discovery is a hard failure — without subdomains, nothing else works
    return tool.Phase() == "discovery"
}
```

### Soft failures (log and continue)

All other phases are soft failures. The pipeline logs a warning and continues with whatever data it has. This matches surfbot-api's behavior where port scan/httpx/nuclei failures don't abort the job.

### Context cancellation

```go
// Check before each phase
select {
case <-ctx.Done():
    scan.Status = model.ScanStatusCancelled
    p.store.UpdateScan(ctx, scan)
    return nil, ctx.Err()
default:
}
```

### Per-phase timeouts

```go
phaseTimeout := time.Duration(opts.Timeout) * time.Second
if phaseTimeout == 0 {
    phaseTimeout = 5 * time.Minute // default
}
phaseCtx, cancel := context.WithTimeout(ctx, phaseTimeout)
defer cancel()

result, err := tool.Run(phaseCtx, inputs, runOpts)
```

---

## 7. CLI Scan Command

**File:** `internal/cli/scan.go`

Replace the current stub.

```go
var scanCmd = &cobra.Command{
    Use:   "scan [target]",
    Short: "Run a security scan against a target",
    Long:  "Runs the full detection pipeline: discovery → resolution → port scan → http probe → vulnerability assessment",
    Args:  cobra.ExactArgs(1),
    RunE:  runScan,
}

func init() {
    scanCmd.Flags().StringP("type", "t", "full", "Scan type: full, quick, or discovery")
    scanCmd.Flags().StringSlice("tools", nil, "Specific tools to run (comma-separated)")
    scanCmd.Flags().IntP("rate-limit", "r", 0, "Global rate limit (requests/second)")
    scanCmd.Flags().Int("timeout", 300, "Per-phase timeout in seconds")
    scanCmd.Flags().StringP("output", "o", "", "Output results to file (JSON)")
}
```

### runScan behavior

```go
func runScan(cmd *cobra.Command, args []string) error {
    targetValue := args[0]

    // 1. Open storage
    store, err := storage.Open(config.DBPath())

    // 2. Auto-create target if it doesn't exist
    target := autoCreateTarget(store, targetValue)

    // 3. Build pipeline
    registry := detection.NewRegistry()
    pipe := pipeline.New(store, registry)

    // 4. Parse flags into PipelineOptions
    opts := pipeline.PipelineOptions{
        ScanType:  parseScanType(cmd),
        Tools:     parseTools(cmd),
        RateLimit: parseRateLimit(cmd),
        Timeout:   parseTimeout(cmd),
    }

    // 5. Run pipeline
    result, err := pipe.Run(cmd.Context(), target.ID, opts)

    // 6. Print summary
    printScanSummary(result)

    // 7. Optionally write JSON output
    if outputPath != "" {
        writeJSONOutput(result, outputPath)
    }

    return nil
}
```

### Auto-create target

```go
func autoCreateTarget(store storage.Store, value string) *model.Target {
    // Check if target exists
    targets, _ := store.ListTargets(ctx)
    for _, t := range targets {
        if t.Value == value {
            return &t
        }
    }
    // Create new target
    target := model.Target{
        ID:     uuid.New().String(),
        Value:  value,
        Type:   detectTargetType(value), // domain|ipv4|ipv6|cidr
        Status: model.TargetStatusActive,
    }
    store.CreateTarget(ctx, &target)
    return &target
}
```

---

## 8. Storage Methods Required

Check `internal/storage/storage.go` and add any missing methods.

### Must exist (from N3+N5):

- `CreateTarget`, `GetTarget`, `ListTargets` ✓
- `CreateScan`, `GetScan`, `UpdateScan`, `ListScans` — may need `UpdateScan`
- `UpsertAsset`, `ListAssets` — may need `UpsertAsset` (ON CONFLICT logic)
- `UpsertFinding`, `ListFindings` — may need `UpsertFinding`
- `CreateToolRun`, `UpdateToolRun` — may need both

### UpsertAsset SQL

```sql
INSERT INTO assets (id, target_id, parent_id, type, value, status, tags, metadata, first_seen, last_seen, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(target_id, value) DO UPDATE SET
    last_seen = excluded.last_seen,
    status = CASE
        WHEN assets.status = 'disappeared' THEN 'returned'
        ELSE excluded.status
    END,
    metadata = excluded.metadata,
    updated_at = excluded.updated_at
```

### UpsertFinding SQL

```sql
INSERT INTO findings (id, asset_id, scan_id, template_id, template_name, severity, title, description,
    references, remediation, evidence, cvss, cve, status, source_tool, confidence,
    first_seen, last_seen, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(scan_id, template_id, source_tool) DO UPDATE SET
    last_seen = excluded.last_seen,
    evidence = excluded.evidence,
    updated_at = excluded.updated_at
```

---

## 9. Output Formatting

### Terminal output (default)

The pipeline prints progress as it runs (see example in "What This Spec Delivers" section above).

After completion, print findings summary table.

### JSON output (--output flag)

```json
{
  "scan_id": "abc-123",
  "target": "example.com",
  "type": "full",
  "status": "completed",
  "duration_ms": 154000,
  "stats": {
    "subdomains_found": 87,
    "ips_resolved": 42,
    "open_ports": 156,
    "http_probed": 89,
    "tech_detected": 12,
    "findings_total": 23,
    "findings_critical": 2,
    "findings_high": 5,
    "findings_medium": 8,
    "findings_low": 4,
    "findings_info": 4
  },
  "phases": [
    {"tool": "subfinder", "phase": "discovery", "status": "completed", "duration_ms": 12000, "input_count": 1, "output_count": 87},
    {"tool": "dnsx", "phase": "resolution", "status": "completed", "duration_ms": 5000, "input_count": 87, "output_count": 42},
    {"tool": "naabu", "phase": "port_scan", "status": "completed", "duration_ms": 45000, "input_count": 42, "output_count": 156},
    {"tool": "httpx", "phase": "http_probe", "status": "completed", "duration_ms": 23000, "input_count": 156, "output_count": 89},
    {"tool": "nuclei", "phase": "assessment", "status": "completed", "duration_ms": 69000, "input_count": 89, "output_count": 23}
  ]
}
```

---

## 10. Update `surfbot status` Command

**File:** `internal/cli/status.go`

Update the existing status command to include last scan info:

```
$ surfbot status

Surfbot Agent v0.1.0
Database: ~/.surfbot/surfbot.db (2.4 MB)
Targets: 3
Assets: 1,247
Findings: 23 (2 critical, 5 high)
Last scan: example.com — 2m34s ago (completed)
Tools: 5/5 available
```

---

## 11. Tests

### 11.1 Pipeline Tests

**File:** `internal/pipeline/pipeline_test.go`

1. **TestPipelineFullScan** — Run full pipeline with mock tools. Verify all 5 phases execute in order, data threads correctly between phases, scan status transitions (queued→running→completed), stats updated correctly.

2. **TestPipelineDiscoveryScan** — Run with `ScanTypeDiscovery`. Only subfinder + dnsx run, others skipped.

3. **TestPipelineQuickScan** — Run with `ScanTypeQuick`. Port scan skipped, httpx receives IPs directly.

4. **TestPipelineDiscoveryFailure** — subfinder returns error. Pipeline aborts, scan status = failed.

5. **TestPipelineSoftFailure** — naabu returns error. Pipeline continues, httpx still runs.

6. **TestPipelineEmptyResults** — subfinder returns 0 subdomains. Pipeline aborts with meaningful error.

7. **TestPipelineCancellation** — Cancel context mid-pipeline. Scan status = cancelled.

8. **TestDataThreading** — Verify extractInputsForNextPhase returns correct asset types per phase.

### 11.2 Mock Detection Tools

Create `internal/pipeline/mock_test.go` with mock tools that return predefined results:

```go
type MockTool struct {
    name    string
    phase   string
    assets  []model.Asset
    findings []model.Finding
    err     error
}

func (m *MockTool) Run(ctx context.Context, inputs []string, opts detection.RunOptions) (*detection.RunResult, error) {
    if m.err != nil {
        return nil, m.err
    }
    return &detection.RunResult{Assets: m.assets, Findings: m.findings}, nil
}
```

### 11.3 CLI Scan Tests

**File:** `internal/cli/scan_test.go`

9. **TestScanCommandArgs** — Verify flag parsing: --type, --tools, --rate-limit, --timeout, --output.

10. **TestAutoCreateTarget** — Target doesn't exist → auto-created. Target exists → reused.

### 11.4 Storage Tests

**File:** `internal/storage/storage_test.go` (extend existing)

11. **TestUpsertAsset** — Insert new asset, then upsert same value → last_seen updated, no duplicate.

12. **TestUpsertFinding** — Insert finding, then upsert same template_id + source_tool → updated, not duplicated.

13. **TestScanCRUD** — CreateScan, GetScan, UpdateScan (status, stats, progress).

**Do NOT make real network calls in pipeline tests.** Use mock tools only.

---

## 12. Acceptance Criteria

1. `surfbot scan example.com` executes all 5 phases and prints progress + findings summary
2. `surfbot scan example.com --type discovery` runs only subfinder + dnsx
3. `surfbot scan example.com --type quick` skips port scan
4. `surfbot scan example.com --output results.json` writes JSON output file
5. Assets persisted to SQLite with correct parent_id hierarchy (subdomain→IP→port→URL)
6. Findings persisted with correct scan_id, severity, CVE, evidence
7. Tool runs recorded with status, duration, input/output counts
8. Scan progress tracked (0→100%) with phase transitions
9. Discovery failure aborts scan; other phase failures are soft (continue)
10. Context cancellation stops the pipeline gracefully
11. `surfbot status` shows last scan info
12. `go test ./internal/pipeline/... -v` → all tests pass
13. `go test ./... -v -race` → entire suite passes (including new storage tests)
14. `go build ./cmd/surfbot` compiles cleanly

---

## 13. Prompt for Claude Code

```
Read SPEC-N6-pipeline.md in this repo and implement the asset discovery pipeline. Key rules:

1. Replace the stub in internal/pipeline/pipeline.go with full Pipeline orchestrator
2. Pipeline executes tools from Registry in order: subfinder → dnsx → naabu → httpx → nuclei
3. Data threading: each phase outputs assets that become inputs for the next phase
4. extractInputsForNextPhase: discovery→subdomains, resolution→IPs, port_scan→hostports, http_probe→URLs
5. Three scan types: full (all phases), quick (skip port scan), discovery (subfinder + dnsx only)
6. Hard failure: discovery fails → abort. Soft failure: all others → log warning, continue
7. Add missing storage methods: UpdateScan, UpsertAsset, UpsertFinding, CreateToolRun
8. UpsertAsset uses ON CONFLICT(target_id, value) DO UPDATE
9. UpsertFinding uses ON CONFLICT(scan_id, template_id, source_tool) DO UPDATE
10. Update surfbot scan command: accepts target arg, --type, --tools, --rate-limit, --timeout, --output flags
11. Auto-create target if it doesn't exist
12. Print progress per phase + findings summary table at end
13. Update surfbot status command to show last scan info
14. Write all 13 tests specified (mock tools, no real network calls in pipeline tests)
15. Add ScanStats struct if it doesn't exist in model
16. Per-phase timeout via context.WithTimeout (default 5 min)
17. Scan status transitions: running → completed|failed|cancelled
```

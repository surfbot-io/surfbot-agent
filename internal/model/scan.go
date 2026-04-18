package model

import "time"

type ScanType string

const (
	ScanTypeFull      ScanType = "full"
	ScanTypeQuick     ScanType = "quick"
	ScanTypeDiscovery ScanType = "discovery"
)

type ScanStatus string

const (
	ScanStatusQueued    ScanStatus = "queued"
	ScanStatusRunning   ScanStatus = "running"
	ScanStatusCompleted ScanStatus = "completed"
	ScanStatusFailed    ScanStatus = "failed"
	ScanStatusCancelled ScanStatus = "cancelled"
)

// RemediationStatus mirrors the remediations table constraint. Declared here
// so TargetState can aggregate by status even though the storage layer does
// not yet expose remediation CRUD (placeholder until remediation tooling
// lands — see `internal/remediation/`). The vocabulary is open for counting
// purposes: any status string appears as a key in TargetState.Remediations.
type RemediationStatus string

const (
	RemediationStatusPlanned    RemediationStatus = "planned"
	RemediationStatusApproved   RemediationStatus = "approved"
	RemediationStatusRunning    RemediationStatus = "running"
	RemediationStatusCompleted  RemediationStatus = "completed"
	RemediationStatusFailed     RemediationStatus = "failed"
	RemediationStatusRolledBack RemediationStatus = "rolled_back"
)

// TargetState is the snapshot of a target's observed state at the moment a
// scan completes. Source of truth: queries against the assets, findings, and
// remediations tables filtered by target_id. TargetState answers "what does
// the target look like now?" — distinct from ScanDelta (what this scan
// changed) and ScanWork (what this scan did).
//
// The map-based fields are intentionally open: a new detection tool that
// emits a new AssetType automatically appears as a key in AssetsByType
// without any schema, struct, or agent-spec structural change. LLM consumers
// must tolerate unknown keys.
type TargetState struct {
	// AssetsByType is the count of active+new+returned assets per AssetType.
	// Disappeared/inactive/ignored assets are excluded — TargetState reflects
	// what currently exists, not historical presence.
	AssetsByType map[AssetType]int `json:"assets_by_type"`
	AssetsTotal  int               `json:"assets_total"`

	// PortsByStatus buckets port_service assets by metadata.status
	// ("open", "filtered", anything a future scanner emits). Empty when the
	// target has no port_service assets.
	PortsByStatus map[string]int `json:"ports_by_status,omitempty"`

	// FindingsOpen counts findings with status=open by severity. FindingsByStatus
	// counts findings across every status bucket (open/acknowledged/resolved/…).
	FindingsOpen      map[Severity]int      `json:"findings_open"`
	FindingsOpenTotal int                   `json:"findings_open_total"`
	FindingsByStatus  map[FindingStatus]int `json:"findings_by_status"`

	// Remediations counts remediation records by status. Empty until the
	// remediation storage layer is implemented; the field exists in the
	// contract so LLM consumers can assume stable shape.
	Remediations map[RemediationStatus]int `json:"remediations"`
}

// ScanDelta captures what changed in this scan versus the previous state of
// the target. Source: the asset_changes table (scoped by scan_id) plus
// finding-status transitions derived from first_seen/last_seen timestamps.
//
// Delta answers "what did this scan discover or resolve?" — not "what does
// the target look like now?" (that's TargetState).
type ScanDelta struct {
	NewAssets         map[AssetType]int `json:"new_assets"`
	DisappearedAssets map[AssetType]int `json:"disappeared_assets"`
	ModifiedAssets    map[AssetType]int `json:"modified_assets"`

	NewFindings      map[Severity]int `json:"new_findings"`
	ResolvedFindings map[Severity]int `json:"resolved_findings"`
	ReturnedFindings map[Severity]int `json:"returned_findings"`

	// IsBaseline is true when this is the first scan of a target: all
	// discovered assets appear as "new" but they do not represent actual
	// additions — the delta is mostly informational.
	IsBaseline bool `json:"is_baseline"`
}

// ScanWork records what the scan did — telemetry of the execution itself,
// independent of what it observed. Source: the tool_runs table filtered by
// scan_id plus scan timing.
type ScanWork struct {
	DurationMs   int64    `json:"duration_ms"`
	ToolsRun     int      `json:"tools_run"`
	ToolsFailed  int      `json:"tools_failed"`
	ToolsSkipped int      `json:"tools_skipped"`
	PhasesRun    []string `json:"phases_run"`

	// RawEmissions is the sum of findings emitted by tools before storage
	// dedup — useful for debugging tool noise (e.g. nuclei emitted 50
	// findings but storage merged them into 3 unique rows).
	RawEmissions int `json:"raw_emissions"`
}

// Scan describes one pipeline execution against a target. The three
// aggregate fields are populated at the end of the run:
//   - TargetState: snapshot of the target observed by this scan.
//   - Delta:       what this scan changed relative to prior state.
//   - Work:        telemetry of the execution itself.
//
// These three concepts replace the legacy ScanStats struct which conflated
// state, delta, and work into a single mutable accumulator.
type Scan struct {
	ID          string      `json:"id"`
	TargetID    string      `json:"target_id"`
	Type        ScanType    `json:"type"`
	Status      ScanStatus  `json:"status"`
	Phase       string      `json:"phase"`
	Progress    float32     `json:"progress"`
	TargetState TargetState `json:"target_state"`
	Delta       ScanDelta   `json:"delta"`
	Work        ScanWork    `json:"work"`
	StartedAt   *time.Time  `json:"started_at,omitempty"`
	FinishedAt  *time.Time  `json:"finished_at,omitempty"`
	Error       string      `json:"error,omitempty"`
	CreatedAt   time.Time   `json:"created_at"`
	UpdatedAt   time.Time   `json:"updated_at"`
}

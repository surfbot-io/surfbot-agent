package cli

// Schema is a minimal JSON-Schema-ish fragment. We don't use a full
// jsonschema library to avoid a new dependency; these hand-coded fragments
// are pinned by golden tests in agent_spec_test.go. If internal/model
// structs gain new fields, the golden test will fail and the schema here
// must be updated deliberately (see docs/agent-spec.md).
type Schema struct {
	Type                 string            `json:"type,omitempty"`
	Description          string            `json:"description,omitempty"`
	Properties           map[string]Schema `json:"properties,omitempty"`
	Items                *Schema           `json:"items,omitempty"`
	Ref                  string            `json:"$ref,omitempty"`
	Enum                 []string          `json:"enum,omitempty"`
	Required             []string          `json:"required,omitempty"`
	AdditionalProperties *bool             `json:"additionalProperties,omitempty"`
}

// BuildTypeSchemas returns the map of named JSON Schema fragments used by
// every command's input/output schema_ref. Kept alphabetically ordered by
// relying on map iteration being stable in the encoder via SetIndent +
// explicit key ordering in tests.
func BuildTypeSchemas() map[string]Schema {
	str := Schema{Type: "string"}
	strArr := Schema{Type: "array", Items: &Schema{Type: "string"}}
	num := Schema{Type: "number"}
	integer := Schema{Type: "integer"}
	boolean := Schema{Type: "boolean"}
	obj := Schema{Type: "object"}

	// Asset.type is declared as a plain string (not an enum) because new
	// detection tools may introduce new AssetType values without a spec
	// version bump. The `KnownAssetTypes` list below documents the current
	// built-in vocabulary; consumers must tolerate unseen values.
	asset := Schema{
		Type: "object",
		Properties: map[string]Schema{
			"id":         str,
			"target_id":  str,
			"parent_id":  str,
			"type":       {Type: "string", Description: "AssetType. Open vocabulary. Built-in values: domain, subdomain, ipv4, ipv6, port_service, url, technology, service. Detection tools may emit additional values — consumers must tolerate unknown AssetType strings."},
			"value":      str,
			"status":     {Type: "string", Enum: []string{"active", "new", "disappeared", "returned", "inactive", "ignored"}},
			"tags":       strArr,
			"metadata":   obj,
			"first_seen": str,
			"last_seen":  str,
			"created_at": str,
			"updated_at": str,
		},
		Required: []string{"id", "type", "value", "status"},
	}

	// Finding.scan_id tracks the LATEST scan that observed this finding
	// (updated on every upsert). Finding.first_seen_scan_id is immutable
	// and names the scan that first discovered it. To recover a scan's
	// complete observation set, query findings WHERE scan_id = <scan>.
	finding := Schema{
		Type: "object",
		Properties: map[string]Schema{
			"id":                 str,
			"asset_id":           str,
			"scan_id":            {Type: "string", Description: "id of the most recent scan that observed this finding (mutable)"},
			"first_seen_scan_id": {Type: "string", Description: "id of the scan that first discovered this finding (immutable)"},
			"template_id":        str,
			"template_name":      str,
			"severity":           {Type: "string", Enum: []string{"critical", "high", "medium", "low", "info"}},
			"title":              str,
			"description":        str,
			"references":         strArr,
			"remediation":        str,
			"evidence":           str,
			"cvss":               num,
			"cve":                str,
			"status":             {Type: "string", Enum: []string{"open", "acknowledged", "resolved", "false_positive", "ignored"}},
			"source_tool":        str,
			"confidence":         num,
			"first_seen":         str,
			"last_seen":          str,
			"resolved_at":        str,
			"created_at":         str,
			"updated_at":         str,
		},
		Required: []string{"id", "asset_id", "severity", "title", "status"},
	}

	target := Schema{
		Type: "object",
		Properties: map[string]Schema{
			"id":           str,
			"value":        str,
			"type":         {Type: "string", Enum: []string{"domain", "cidr", "ip"}},
			"scope":        {Type: "string", Enum: []string{"external", "internal", "both"}},
			"enabled":      boolean,
			"last_scan_id": str,
			"last_scan_at": str,
			"created_at":   str,
			"updated_at":   str,
		},
		Required: []string{"id", "value", "type"},
	}

	toolRun := Schema{
		Type: "object",
		Properties: map[string]Schema{
			"id":             str,
			"scan_id":        str,
			"tool_name":      str,
			"phase":          str,
			"status":         {Type: "string", Enum: []string{"running", "completed", "failed", "skipped", "timeout"}},
			"started_at":     str,
			"finished_at":    str,
			"duration_ms":    integer,
			"targets_count":  integer,
			"findings_count": integer,
			"output_summary": str,
			"error_message":  str,
			"exit_code":      integer,
			"config":         obj,
		},
		Required: []string{"id", "tool_name", "status"},
	}

	// --- Scan aggregates (spec 2.0) ---
	//
	// A scan is described by three semantically distinct aggregates:
	//
	//   target_state — what the target looks like at scan completion.
	//                  Derived from assets/findings queries. State, not
	//                  event: if the scan doesn't cover a phase, the
	//                  corresponding counts reflect whatever was there
	//                  before (nothing is reset).
	//
	//   delta        — what this scan changed vs. the target's prior
	//                  state. Derived from asset_changes (scan_id) and
	//                  finding status transitions.
	//
	//   work         — telemetry of the execution itself. Derived from
	//                  tool_runs (scan_id) and scan timing.
	//
	// All count-bucket fields (assets_by_type, new_assets, findings_open,
	// …) are JSON objects keyed by the relevant enum string. The enum
	// vocabularies are open for AssetType (new tools may add keys) and
	// closed for Severity / FindingStatus / RemediationStatus. Consumers
	// must tolerate unknown keys regardless.
	assetTypeCounts := Schema{Type: "object", Description: "Counts keyed by AssetType (open vocabulary). Example: {\"subdomain\": 12, \"port_service\": 5}"}
	severityCounts := Schema{Type: "object", Description: "Counts keyed by Severity (critical|high|medium|low|info)"}
	findingStatusCounts := Schema{Type: "object", Description: "Counts keyed by FindingStatus (open|acknowledged|resolved|false_positive|ignored)"}
	remediationStatusCounts := Schema{Type: "object", Description: "Counts keyed by RemediationStatus (planned|approved|running|completed|failed|rolled_back). Empty until remediation tooling lands."}
	stringCounts := Schema{Type: "object", Description: "Counts keyed by arbitrary string. Port status values (open|filtered|unknown|…) are emitted by the port_scan tool."}

	targetState := Schema{
		Type:        "object",
		Description: "Snapshot of the target's observed state at scan completion. Answers \"what exists now?\"",
		Properties: map[string]Schema{
			"assets_by_type":      assetTypeCounts,
			"assets_total":        integer,
			"ports_by_status":     stringCounts,
			"findings_open":       severityCounts,
			"findings_open_total": integer,
			"findings_by_status":  findingStatusCounts,
			"remediations":        remediationStatusCounts,
		},
	}

	scanDelta := Schema{
		Type:        "object",
		Description: "What this scan changed vs. the prior state of the target. Baseline scans report is_baseline=true with empty buckets.",
		Properties: map[string]Schema{
			"new_assets":         assetTypeCounts,
			"disappeared_assets": assetTypeCounts,
			"modified_assets":    assetTypeCounts,
			"new_findings":       severityCounts,
			"resolved_findings":  severityCounts,
			"returned_findings":  severityCounts,
			"is_baseline":        boolean,
		},
	}

	scanWork := Schema{
		Type:        "object",
		Description: "Telemetry of the scan execution. Independent of what was observed or changed.",
		Properties: map[string]Schema{
			"duration_ms":   integer,
			"tools_run":     integer,
			"tools_failed":  integer,
			"tools_skipped": integer,
			"phases_run":    strArr,
			"raw_emissions": {Type: "integer", Description: "Sum of findings emitted by tools before storage dedup. Useful for debugging tool noise (e.g. nuclei emitted 50 findings but storage merged them into 3 unique rows)."},
		},
	}

	scan := Schema{
		Type: "object",
		Properties: map[string]Schema{
			"id":           str,
			"target_id":    str,
			"type":         {Type: "string", Enum: []string{"full", "quick", "discovery"}},
			"status":       {Type: "string", Enum: []string{"queued", "running", "completed", "failed", "cancelled"}}, //nolint:misspell // "cancelled" is the value used throughout the codebase (model.ScanStatusCancelled); match the DB enum exactly.
			"phase":        str,
			"progress":     num,
			"started_at":   str,
			"finished_at":  str,
			"error":        str,
			"target_state": targetState,
			"delta":        scanDelta,
			"work":         scanWork,
		},
		Required: []string{"id", "type", "status"},
	}

	score := Schema{
		Type: "object",
		Properties: map[string]Schema{
			"value": num,
			"grade": str,
		},
	}

	status := Schema{
		Type: "object",
		Properties: map[string]Schema{
			"running": boolean,
			"phase":   str,
			"message": str,
		},
	}

	domainList := Schema{
		Type: "object",
		Properties: map[string]Schema{
			"domains": strArr,
		},
		Required: []string{"domains"},
	}

	return map[string]Schema{
		"Asset":       asset,
		"AssetList":   {Type: "object", Properties: map[string]Schema{"assets": {Type: "array", Items: &Schema{Ref: "#/types/Asset"}}}, Required: []string{"assets"}},
		"Finding":     finding,
		"FindingList": {Type: "object", Properties: map[string]Schema{"findings": {Type: "array", Items: &Schema{Ref: "#/types/Finding"}}}, Required: []string{"findings"}},
		"Target":      target,
		"ToolRun":     toolRun,
		"ScanResult":  scan,
		"TargetState": targetState,
		"ScanDelta":   scanDelta,
		"ScanWork":    scanWork,
		"Score":       score,
		"Status":      status,
		"DomainList":  domainList,
	}
}

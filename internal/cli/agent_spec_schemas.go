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

	asset := Schema{
		Type: "object",
		Properties: map[string]Schema{
			"id":         str,
			"target_id":  str,
			"parent_id":  str,
			"type":       {Type: "string", Enum: []string{"domain", "subdomain", "ipv4", "ipv6", "port_service", "url", "technology", "service"}},
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

	finding := Schema{
		Type: "object",
		Properties: map[string]Schema{
			"id":            str,
			"asset_id":      str,
			"scan_id":       str,
			"template_id":   str,
			"template_name": str,
			"severity":      {Type: "string", Enum: []string{"critical", "high", "medium", "low", "info"}},
			"title":         str,
			"description":   str,
			"references":    strArr,
			"remediation":   str,
			"evidence":      str,
			"cvss":          num,
			"cve":           str,
			"status":        {Type: "string", Enum: []string{"open", "acknowledged", "resolved", "false_positive", "ignored"}},
			"source_tool":   str,
			"confidence":    num,
			"first_seen":    str,
			"last_seen":     str,
			"resolved_at":   str,
			"created_at":    str,
			"updated_at":    str,
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

	scan := Schema{
		Type: "object",
		Properties: map[string]Schema{
			"id":          str,
			"target_id":   str,
			"type":        {Type: "string", Enum: []string{"full", "quick", "discovery"}},
			"status":      {Type: "string", Enum: []string{"queued", "running", "completed", "failed", "cancelled"}},
			"phase":       str,
			"progress":    num,
			"started_at":  str,
			"finished_at": str,
			"error":       str,
			"stats": {
				Type: "object",
				Properties: map[string]Schema{
					"subdomains_found":  integer,
					"ips_resolved":      integer,
					"ports_scanned":     integer,
					"open_ports":        integer,
					"http_probed":       integer,
					"tech_detected":     integer,
					"findings_total":    integer,
					"findings_critical": integer,
					"findings_high":     integer,
					"findings_medium":   integer,
					"findings_low":      integer,
					"findings_info":     integer,
				},
			},
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
		"Score":       score,
		"Status":      status,
		"DomainList":  domainList,
	}
}

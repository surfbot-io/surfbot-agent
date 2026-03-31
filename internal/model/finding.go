package model

import "time"

type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityHigh     Severity = "high"
	SeverityMedium   Severity = "medium"
	SeverityLow      Severity = "low"
	SeverityInfo     Severity = "info"
)

type FindingStatus string

const (
	FindingStatusOpen          FindingStatus = "open"
	FindingStatusAcknowledged  FindingStatus = "acknowledged"
	FindingStatusResolved      FindingStatus = "resolved"
	FindingStatusFalsePositive FindingStatus = "false_positive"
	FindingStatusIgnored       FindingStatus = "ignored"
)

type Finding struct {
	ID           string        `json:"id"`
	AssetID      string        `json:"asset_id"`
	ScanID       string        `json:"scan_id,omitempty"`
	TemplateID   string        `json:"template_id"`
	TemplateName string        `json:"template_name"`
	Severity     Severity      `json:"severity"`
	Title        string        `json:"title"`
	Description  string        `json:"description"`
	References   []string      `json:"references"`
	Remediation  string        `json:"remediation"`
	Evidence     string        `json:"evidence"`
	CVSS         float64       `json:"cvss,omitempty"`
	CVE          string        `json:"cve,omitempty"`
	Status       FindingStatus `json:"status"`
	SourceTool   string        `json:"source_tool"`
	Confidence   float64       `json:"confidence"`
	FirstSeen    time.Time     `json:"first_seen"`
	LastSeen     time.Time     `json:"last_seen"`
	ResolvedAt   *time.Time    `json:"resolved_at,omitempty"`
	CreatedAt    time.Time     `json:"created_at"`
	UpdatedAt    time.Time     `json:"updated_at"`
}

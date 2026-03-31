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

type ScanStats struct {
	SubdomainsFound  int `json:"subdomains_found"`
	IPsResolved      int `json:"ips_resolved"`
	PortsScanned     int `json:"ports_scanned"`
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

type Scan struct {
	ID         string     `json:"id"`
	TargetID   string     `json:"target_id"`
	Type       ScanType   `json:"type"`
	Status     ScanStatus `json:"status"`
	Phase      string     `json:"phase"`
	Progress   float32    `json:"progress"`
	Stats      ScanStats  `json:"stats"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	Error      string     `json:"error,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

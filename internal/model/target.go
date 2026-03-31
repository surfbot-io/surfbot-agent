package model

import "time"

// Target replaces "domain" concept from surfbot-api.
// Agent targets can be domains, CIDRs, or IPs.
type TargetType string

const (
	TargetTypeDomain TargetType = "domain"
	TargetTypeCIDR   TargetType = "cidr"
	TargetTypeIP     TargetType = "ip"
)

type TargetScope string

const (
	TargetScopeExternal TargetScope = "external"
	TargetScopeInternal TargetScope = "internal"
	TargetScopeBoth     TargetScope = "both"
)

type Target struct {
	ID         string      `json:"id"`
	Value      string      `json:"value"`
	Type       TargetType  `json:"type"`
	Scope      TargetScope `json:"scope"`
	Enabled    bool        `json:"enabled"`
	LastScanID string      `json:"last_scan_id,omitempty"`
	LastScanAt *time.Time  `json:"last_scan_at,omitempty"`
	CreatedAt  time.Time   `json:"created_at"`
	UpdatedAt  time.Time   `json:"updated_at"`
}

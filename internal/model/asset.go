package model

import "time"

type AssetType string

const (
	AssetTypeDomain     AssetType = "domain"
	AssetTypeSubdomain  AssetType = "subdomain"
	AssetTypeIPv4       AssetType = "ipv4"
	AssetTypeIPv6       AssetType = "ipv6"
	AssetTypePort       AssetType = "port_service"
	AssetTypeURL        AssetType = "url"
	AssetTypeTechnology AssetType = "technology"
	AssetTypeService    AssetType = "service"
)

type AssetStatus string

const (
	AssetStatusActive      AssetStatus = "active"
	AssetStatusNew         AssetStatus = "new"
	AssetStatusDisappeared AssetStatus = "disappeared"
	AssetStatusReturned    AssetStatus = "returned"
	AssetStatusInactive    AssetStatus = "inactive"
	AssetStatusIgnored     AssetStatus = "ignored"
)

type Asset struct {
	ID        string         `json:"id"`
	TargetID  string         `json:"target_id"`
	ParentID  string         `json:"parent_id,omitempty"`
	Type      AssetType      `json:"type"`
	Value     string         `json:"value"`
	Status    AssetStatus    `json:"status"`
	Tags      []string       `json:"tags"`
	Metadata  map[string]any `json:"metadata"`
	FirstSeen time.Time      `json:"first_seen"`
	LastSeen  time.Time      `json:"last_seen"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

package model

import "time"

// AssetType identifies what kind of entity an Asset represents.
//
// The set of constants below is the vocabulary currently produced by built-in
// detection tools. It is intentionally open-ended: a new tool may introduce a
// new AssetType constant without requiring a SQL migration — the database
// column is unconstrained and validation happens in Go. LLM consumers must
// tolerate unknown AssetType strings.
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

// KnownAssetTypes is the vocabulary documented in the agent-spec. Used by
// spec generation and by tests that want to iterate over the built-in set.
// New detection tools can emit AssetTypes outside this list; storage does
// not reject them.
var KnownAssetTypes = []AssetType{
	AssetTypeDomain,
	AssetTypeSubdomain,
	AssetTypeIPv4,
	AssetTypeIPv6,
	AssetTypePort,
	AssetTypeURL,
	AssetTypeTechnology,
	AssetTypeService,
}

type AssetStatus string

const (
	AssetStatusActive      AssetStatus = "active"
	AssetStatusNew         AssetStatus = "new"
	AssetStatusDisappeared AssetStatus = "disappeared"
	AssetStatusReturned    AssetStatus = "returned"
	AssetStatusInactive    AssetStatus = "inactive"
	AssetStatusIgnored     AssetStatus = "ignored"
)

// AssetStatusIsLive reports whether an asset status represents an asset
// currently present on the target. Used by TargetState counting so that
// disappeared/inactive/ignored assets don't inflate the current-state view.
func AssetStatusIsLive(s AssetStatus) bool {
	switch s {
	case AssetStatusActive, AssetStatusNew, AssetStatusReturned:
		return true
	}
	return false
}

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

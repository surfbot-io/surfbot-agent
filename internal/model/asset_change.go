package model

import "time"

type ChangeType string

const (
	ChangeTypeAppeared    ChangeType = "appeared"
	ChangeTypeDisappeared ChangeType = "disappeared"
	ChangeTypeModified    ChangeType = "modified"
)

type Significance string

const (
	SignificanceCritical Significance = "critical"
	SignificanceHigh     Significance = "high"
	SignificanceMedium   Significance = "medium"
	SignificanceLow      Significance = "low"
	SignificanceInfo     Significance = "info"
	SignificanceNoise    Significance = "noise"
)

type AssetChange struct {
	ID           string         `json:"id"`
	TargetID     string         `json:"target_id"`
	ScanID       string         `json:"scan_id"`
	AssetID      string         `json:"asset_id,omitempty"`
	ChangeType   ChangeType     `json:"change_type"`
	Significance Significance   `json:"significance"`
	AssetType    string         `json:"asset_type"`
	AssetValue   string         `json:"asset_value"`
	PreviousMeta map[string]any `json:"previous_meta,omitempty"`
	CurrentMeta  map[string]any `json:"current_meta,omitempty"`
	Summary      string         `json:"summary"`
	Baseline     bool           `json:"baseline"`
	CreatedAt    time.Time      `json:"created_at"`
}

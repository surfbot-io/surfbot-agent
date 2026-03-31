package model

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFindingSerialization(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	f := Finding{
		ID:           "f-1",
		AssetID:      "a-1",
		TemplateID:   "CVE-2024-1234",
		TemplateName: "Test Template",
		Severity:     SeverityHigh,
		Title:        "Test Finding",
		Status:       FindingStatusOpen,
		SourceTool:   "nuclei",
		Confidence:   85.0,
		FirstSeen:    now,
		LastSeen:     now,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	data, err := json.Marshal(f)
	require.NoError(t, err)

	var decoded Finding
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, f.ID, decoded.ID)
	assert.Equal(t, f.Severity, decoded.Severity)
	assert.Equal(t, f.Status, decoded.Status)
	assert.Equal(t, f.Confidence, decoded.Confidence)
}

func TestTargetTypes(t *testing.T) {
	assert.Equal(t, TargetType("domain"), TargetTypeDomain)
	assert.Equal(t, TargetType("cidr"), TargetTypeCIDR)
	assert.Equal(t, TargetType("ip"), TargetTypeIP)
}

func TestAssetTypes(t *testing.T) {
	types := []AssetType{
		AssetTypeDomain, AssetTypeSubdomain, AssetTypeIPv4,
		AssetTypeIPv6, AssetTypePort, AssetTypeURL,
		AssetTypeTechnology, AssetTypeService,
	}
	assert.Len(t, types, 8)
}

func TestScanStatusValues(t *testing.T) {
	statuses := []ScanStatus{
		ScanStatusQueued, ScanStatusRunning, ScanStatusCompleted,
		ScanStatusFailed, ScanStatusCancelled,
	}
	assert.Len(t, statuses, 5)
}

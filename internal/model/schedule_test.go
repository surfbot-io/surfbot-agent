package model

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func tsUTC(t *testing.T, s string) time.Time {
	t.Helper()
	tm, err := time.Parse(time.RFC3339, s)
	require.NoError(t, err)
	return tm.UTC()
}

func TestSchedule_JSONRoundTrip(t *testing.T) {
	tmplID := "tmpl-01"
	lastScanID := "scan-01"
	status := ScheduleRunSuccess
	nextRun := tsUTC(t, "2026-04-20T02:00:00Z")
	lastRun := tsUTC(t, "2026-04-19T02:00:00Z")

	orig := Schedule{
		ID:         "sched-01",
		TargetID:   "tgt-01",
		Name:       "weekly-full",
		RRule:      "FREQ=WEEKLY;BYDAY=MO;BYHOUR=2",
		DTStart:    tsUTC(t, "2026-04-01T02:00:00Z"),
		Timezone:   "UTC",
		TemplateID: &tmplID,
		ToolConfig: ToolConfig{
			"nuclei": json.RawMessage(`{"severity":["critical","high"]}`),
		},
		Overrides: []string{"rrule"},
		MaintenanceWindow: &MaintenanceWindow{
			RRule:       "FREQ=DAILY;BYHOUR=2",
			DurationSec: 7200,
			Timezone:    "UTC",
		},
		Enabled:       true,
		NextRunAt:     &nextRun,
		LastRunAt:     &lastRun,
		LastRunStatus: &status,
		LastScanID:    &lastScanID,
		CreatedAt:     tsUTC(t, "2026-04-01T00:00:00Z"),
		UpdatedAt:     tsUTC(t, "2026-04-19T02:03:00Z"),
	}

	raw, err := json.Marshal(orig)
	require.NoError(t, err)

	var got Schedule
	require.NoError(t, json.Unmarshal(raw, &got))

	if !reflect.DeepEqual(orig, got) {
		t.Fatalf("round-trip mismatch\norig: %#v\ngot:  %#v", orig, got)
	}
}

func TestTemplate_JSONRoundTrip(t *testing.T) {
	orig := Template{
		ID:          "tmpl-01",
		Name:        "prod-critical",
		Description: "critical-severity probe, every 6h",
		RRule:       "FREQ=HOURLY;INTERVAL=6",
		Timezone:    "UTC",
		ToolConfig: ToolConfig{
			"nuclei": json.RawMessage(`{"severity":["critical"]}`),
		},
		IsSystem:  true,
		CreatedAt: tsUTC(t, "2026-04-01T00:00:00Z"),
		UpdatedAt: tsUTC(t, "2026-04-19T02:03:00Z"),
	}

	raw, err := json.Marshal(orig)
	require.NoError(t, err)
	var got Template
	require.NoError(t, json.Unmarshal(raw, &got))
	assert.Equal(t, orig, got)
}

func TestBlackoutWindow_JSONRoundTrip(t *testing.T) {
	targetID := "tgt-01"
	orig := BlackoutWindow{
		ID:          "bl-01",
		Scope:       BlackoutScopeTarget,
		TargetID:    &targetID,
		Name:        "nightly-maintenance",
		RRule:       "FREQ=DAILY;BYHOUR=2",
		DurationSec: 7200,
		Timezone:    "UTC",
		Enabled:     true,
		CreatedAt:   tsUTC(t, "2026-04-01T00:00:00Z"),
		UpdatedAt:   tsUTC(t, "2026-04-19T00:00:00Z"),
	}

	raw, err := json.Marshal(orig)
	require.NoError(t, err)
	var got BlackoutWindow
	require.NoError(t, json.Unmarshal(raw, &got))
	assert.Equal(t, orig, got)
}

func TestScheduleDefaults_JSONRoundTrip(t *testing.T) {
	tmplID := "tmpl-default"
	orig := ScheduleDefaults{
		DefaultTemplateID: &tmplID,
		DefaultRRule:      "FREQ=DAILY;BYHOUR=2",
		DefaultTimezone:   "UTC",
		DefaultToolConfig: ToolConfig{
			"naabu": json.RawMessage(`{"ports":"top-1000"}`),
		},
		DefaultMaintenanceWindow: &MaintenanceWindow{
			RRule:       "FREQ=DAILY;BYHOUR=2",
			DurationSec: 3600,
			Timezone:    "UTC",
		},
		MaxConcurrentScans: 4,
		RunOnStart:         false,
		JitterSeconds:      60,
		UpdatedAt:          tsUTC(t, "2026-04-19T00:00:00Z"),
	}
	raw, err := json.Marshal(orig)
	require.NoError(t, err)
	var got ScheduleDefaults
	require.NoError(t, json.Unmarshal(raw, &got))
	assert.Equal(t, orig, got)
}

func TestAdHocScanRun_JSONRoundTrip(t *testing.T) {
	tmplID := "tmpl-01"
	scanID := "scan-01"
	started := tsUTC(t, "2026-04-19T02:00:00Z")
	completed := tsUTC(t, "2026-04-19T02:03:00Z")
	orig := AdHocScanRun{
		ID:         "ah-01",
		TargetID:   "tgt-01",
		TemplateID: &tmplID,
		ToolConfig: ToolConfig{
			"nuclei": json.RawMessage(`{"tags":["cve-2025"]}`),
		},
		InitiatedBy: "cli",
		Reason:      "zero-day verification",
		ScanID:      &scanID,
		Status:      AdHocCompleted,
		RequestedAt: tsUTC(t, "2026-04-19T01:59:00Z"),
		StartedAt:   &started,
		CompletedAt: &completed,
	}
	raw, err := json.Marshal(orig)
	require.NoError(t, err)
	var got AdHocScanRun
	require.NoError(t, json.Unmarshal(raw, &got))
	assert.Equal(t, orig, got)
}

func TestToolConfig_GetSetTool(t *testing.T) {
	type dummyParams struct {
		Threads int      `json:"threads,omitempty"`
		Tags    []string `json:"tags,omitempty"`
	}

	tc := ToolConfig{}
	require.NoError(t, SetTool(tc, "nuclei", dummyParams{Threads: 10, Tags: []string{"cve"}}))

	got, ok := GetTool[dummyParams](tc, "nuclei")
	require.True(t, ok)
	assert.Equal(t, 10, got.Threads)
	assert.Equal(t, []string{"cve"}, got.Tags)

	_, ok = GetTool[dummyParams](tc, "unknown")
	assert.False(t, ok)

	// Malformed payload: GetTool returns false without panicking.
	tc["broken"] = json.RawMessage(`"not an object"`)
	_, ok = GetTool[dummyParams](tc, "broken")
	assert.False(t, ok)
}

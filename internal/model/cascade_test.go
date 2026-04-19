package model

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestResolveEffectiveConfig_SpecAcceptance pins the R20 example: schedule
// overrides nuclei.severity; template supplies nuclei.rate_limit and
// naabu entirely; effective config merges correctly.
func TestResolveEffectiveConfig_SpecAcceptance(t *testing.T) {
	tmpl := &Template{
		ID:       "tmpl-01",
		Name:     "prod-critical",
		RRule:    "FREQ=DAILY",
		Timezone: "UTC",
		ToolConfig: ToolConfig{
			"nuclei": json.RawMessage(`{"severity":["low","info"],"rate_limit":100}`),
			"naabu":  json.RawMessage(`{"ports":"top-1000"}`),
		},
	}
	tmplID := tmpl.ID
	sched := Schedule{
		ID:         "sched-01",
		TemplateID: &tmplID,
		ToolConfig: ToolConfig{
			"nuclei": json.RawMessage(`{"severity":["critical","high"]}`),
		},
		// Note: Overrides left empty — tool_config merges without an
		// explicit entry per SPEC-SCHED1 R20.
	}
	defaults := ScheduleDefaults{DefaultTimezone: "UTC"}

	eff, err := ResolveEffectiveConfig(sched, tmpl, defaults)
	require.NoError(t, err)
	assert.Equal(t, "FREQ=DAILY", eff.RRule)
	assert.Equal(t, "UTC", eff.Timezone)

	var nuclei map[string]any
	require.NoError(t, json.Unmarshal(eff.ToolConfig["nuclei"], &nuclei))
	assert.Equal(t, []any{"critical", "high"}, nuclei["severity"])
	assert.Equal(t, float64(100), nuclei["rate_limit"])

	var naabu map[string]any
	require.NoError(t, json.Unmarshal(eff.ToolConfig["naabu"], &naabu))
	assert.Equal(t, "top-1000", naabu["ports"])
}

// TestResolveEffectiveConfig_TopLevelFields is the table-driven coverage
// for the override × layer combinations of rrule, timezone, and
// maintenance_window.
func TestResolveEffectiveConfig_TopLevelFields(t *testing.T) {
	schedMW := &MaintenanceWindow{RRule: "FREQ=DAILY;BYHOUR=0", DurationSec: 600, Timezone: "UTC"}
	tmplMW := &MaintenanceWindow{RRule: "FREQ=DAILY;BYHOUR=1", DurationSec: 1200, Timezone: "UTC"}
	defMW := &MaintenanceWindow{RRule: "FREQ=DAILY;BYHOUR=2", DurationSec: 1800, Timezone: "UTC"}

	cases := []struct {
		name      string
		sched     Schedule
		tmpl      *Template
		defaults  ScheduleDefaults
		wantRRule string
		wantTZ    string
		wantMW    *MaintenanceWindow
	}{
		{
			name:      "override wins: rrule",
			sched:     Schedule{RRule: "FREQ=HOURLY", Timezone: "UTC", Overrides: []string{"rrule"}},
			tmpl:      &Template{RRule: "FREQ=DAILY", Timezone: "UTC"},
			defaults:  ScheduleDefaults{DefaultRRule: "FREQ=WEEKLY", DefaultTimezone: "UTC"},
			wantRRule: "FREQ=HOURLY",
			wantTZ:    "UTC",
		},
		{
			name:      "template wins when no override",
			sched:     Schedule{RRule: "FREQ=HOURLY", Timezone: "UTC"},
			tmpl:      &Template{RRule: "FREQ=DAILY", Timezone: "UTC"},
			defaults:  ScheduleDefaults{DefaultRRule: "FREQ=WEEKLY", DefaultTimezone: "UTC"},
			wantRRule: "FREQ=DAILY",
			wantTZ:    "UTC",
		},
		{
			name:      "defaults win when no template and no override",
			sched:     Schedule{},
			tmpl:      nil,
			defaults:  ScheduleDefaults{DefaultRRule: "FREQ=WEEKLY", DefaultTimezone: "UTC"},
			wantRRule: "FREQ=WEEKLY",
			wantTZ:    "UTC",
		},
		{
			name:      "standalone schedule supplies rrule without override list",
			sched:     Schedule{RRule: "FREQ=HOURLY", Timezone: "UTC"},
			tmpl:      nil,
			defaults:  ScheduleDefaults{},
			wantRRule: "FREQ=HOURLY",
			wantTZ:    "UTC",
		},
		{
			name:      "timezone override wins",
			sched:     Schedule{RRule: "FREQ=DAILY", Timezone: "Europe/Madrid", Overrides: []string{"timezone"}},
			tmpl:      &Template{RRule: "FREQ=DAILY", Timezone: "UTC"},
			defaults:  ScheduleDefaults{},
			wantRRule: "FREQ=DAILY",
			wantTZ:    "Europe/Madrid",
		},
		{
			name:     "maintenance_window override",
			sched:    Schedule{RRule: "FREQ=DAILY", Timezone: "UTC", Overrides: []string{"maintenance_window"}, MaintenanceWindow: schedMW},
			tmpl:     &Template{RRule: "FREQ=DAILY", Timezone: "UTC", MaintenanceWindow: tmplMW},
			defaults: ScheduleDefaults{DefaultMaintenanceWindow: defMW},
			wantMW:   schedMW,
		},
		{
			name:     "maintenance_window from template when not overridden",
			sched:    Schedule{RRule: "FREQ=DAILY", Timezone: "UTC", MaintenanceWindow: schedMW},
			tmpl:     &Template{RRule: "FREQ=DAILY", Timezone: "UTC", MaintenanceWindow: tmplMW},
			defaults: ScheduleDefaults{DefaultMaintenanceWindow: defMW},
			wantMW:   tmplMW,
		},
		{
			name:     "maintenance_window from defaults when template empty",
			sched:    Schedule{RRule: "FREQ=DAILY", Timezone: "UTC"},
			tmpl:     &Template{RRule: "FREQ=DAILY", Timezone: "UTC"},
			defaults: ScheduleDefaults{DefaultMaintenanceWindow: defMW},
			wantMW:   defMW,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			eff, err := ResolveEffectiveConfig(c.sched, c.tmpl, c.defaults)
			require.NoError(t, err)
			if c.wantRRule != "" {
				assert.Equal(t, c.wantRRule, eff.RRule)
			}
			if c.wantTZ != "" {
				assert.Equal(t, c.wantTZ, eff.Timezone)
			}
			if c.wantMW != nil {
				require.NotNil(t, eff.MaintenanceWindow)
				assert.Equal(t, *c.wantMW, *eff.MaintenanceWindow)
			}
		})
	}
}

func TestResolveEffectiveConfig_RequiredFieldError(t *testing.T) {
	_, err := ResolveEffectiveConfig(Schedule{}, nil, ScheduleDefaults{})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrRequiredFieldUnresolved))
	assert.Contains(t, err.Error(), "rrule")

	_, err = ResolveEffectiveConfig(Schedule{RRule: "FREQ=DAILY"}, nil, ScheduleDefaults{})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrRequiredFieldUnresolved))
	assert.Contains(t, err.Error(), "timezone")
}

func TestResolveEffectiveConfig_ToolConfigFallthrough(t *testing.T) {
	// No schedule tool_config, no template → defaults supply everything.
	defaults := ScheduleDefaults{
		DefaultRRule:    "FREQ=DAILY",
		DefaultTimezone: "UTC",
		DefaultToolConfig: ToolConfig{
			"nuclei": json.RawMessage(`{"rate_limit":50}`),
		},
	}
	eff, err := ResolveEffectiveConfig(Schedule{}, nil, defaults)
	require.NoError(t, err)
	var n map[string]any
	require.NoError(t, json.Unmarshal(eff.ToolConfig["nuclei"], &n))
	assert.Equal(t, float64(50), n["rate_limit"])
}

func TestResolveEffectiveConfig_PerToolKeyMerge(t *testing.T) {
	// defaults only supply naabu; template supplies nuclei; schedule
	// overrides one sub-field of nuclei. Effective: all three present,
	// schedule's nuclei fields win where specified.
	defaults := ScheduleDefaults{
		DefaultRRule:    "FREQ=DAILY",
		DefaultTimezone: "UTC",
		DefaultToolConfig: ToolConfig{
			"naabu": json.RawMessage(`{"ports":"top-100"}`),
		},
	}
	tmpl := &Template{
		RRule:    "FREQ=DAILY",
		Timezone: "UTC",
		ToolConfig: ToolConfig{
			"nuclei": json.RawMessage(`{"severity":["low"],"rate_limit":100}`),
		},
	}
	sched := Schedule{
		RRule:    "FREQ=DAILY",
		Timezone: "UTC",
		ToolConfig: ToolConfig{
			"nuclei": json.RawMessage(`{"severity":["critical"]}`),
		},
	}
	eff, err := ResolveEffectiveConfig(sched, tmpl, defaults)
	require.NoError(t, err)

	var nuclei map[string]any
	require.NoError(t, json.Unmarshal(eff.ToolConfig["nuclei"], &nuclei))
	assert.Equal(t, []any{"critical"}, nuclei["severity"])
	assert.Equal(t, float64(100), nuclei["rate_limit"])

	var naabu map[string]any
	require.NoError(t, json.Unmarshal(eff.ToolConfig["naabu"], &naabu))
	assert.Equal(t, "top-100", naabu["ports"])
}

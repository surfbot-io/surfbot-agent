package model

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ErrRequiredFieldUnresolved is returned by ResolveEffectiveConfig when
// no layer (schedule, template, defaults) provides a value for a field
// the scheduler needs. The error message names the offending field.
var ErrRequiredFieldUnresolved = errors.New("required field unresolved")

// EffectiveConfig is the fully-resolved scheduling configuration used at
// dispatch time. All pointer/optional fields on Schedule / Template /
// ScheduleDefaults have been walked to a concrete value.
type EffectiveConfig struct {
	RRule             string
	Timezone          string
	ToolConfig        ToolConfig
	MaintenanceWindow *MaintenanceWindow
}

// ResolveEffectiveConfig walks schedule → template → defaults per
// SPEC-SCHED1 R18 rules:
//
//   - rrule / timezone / maintenance_window use the schedule's value iff
//     the field name is listed in Schedule.Overrides. Otherwise fall
//     through to the template (if non-nil) then to defaults.
//   - tool_config merges shallowly per tool name across layers: defaults
//     → template → schedule. Keys present in schedule replace same-named
//     keys in template, but tool params not mentioned at the schedule
//     layer inherit from the template / defaults. Merging within a
//     single tool's params is also shallow: schedule's "nuclei.severity"
//     overrides, but schedule's missing "nuclei.rate_limit" inherits.
//
// Returns ErrRequiredFieldUnresolved (wrapped with the field name) when
// rrule or timezone cannot be resolved.
func ResolveEffectiveConfig(s Schedule, tmpl *Template, defaults ScheduleDefaults) (EffectiveConfig, error) {
	overridden := overrideSet(s.Overrides)

	rrule := pickString("rrule", overridden, s.RRule, tmplRRule(tmpl), defaults.DefaultRRule)
	if rrule == "" {
		return EffectiveConfig{}, fmt.Errorf("%w: rrule", ErrRequiredFieldUnresolved)
	}
	tz := pickString("timezone", overridden, s.Timezone, tmplTimezone(tmpl), defaults.DefaultTimezone)
	if tz == "" {
		return EffectiveConfig{}, fmt.Errorf("%w: timezone", ErrRequiredFieldUnresolved)
	}

	mw := resolveMaintenanceWindow(overridden, s, tmpl, defaults)
	tc := resolveToolConfig(s, tmpl, defaults)

	return EffectiveConfig{
		RRule:             rrule,
		Timezone:          tz,
		ToolConfig:        tc,
		MaintenanceWindow: mw,
	}, nil
}

func overrideSet(overrides []string) map[string]bool {
	set := make(map[string]bool, len(overrides))
	for _, name := range overrides {
		set[name] = true
	}
	return set
}

func pickString(field string, overridden map[string]bool, sched, tmpl, def string) string {
	if overridden[field] && sched != "" {
		return sched
	}
	if tmpl != "" {
		return tmpl
	}
	if def != "" {
		return def
	}
	// Final fallback: if the schedule supplied a value and the override
	// flag is not set, still use it rather than erroring. This keeps
	// standalone schedules (no template) working without requiring an
	// explicit override list.
	if sched != "" {
		return sched
	}
	return ""
}

func tmplRRule(t *Template) string {
	if t == nil {
		return ""
	}
	return t.RRule
}

func tmplTimezone(t *Template) string {
	if t == nil {
		return ""
	}
	return t.Timezone
}

func resolveMaintenanceWindow(overridden map[string]bool, s Schedule, tmpl *Template, defaults ScheduleDefaults) *MaintenanceWindow {
	if overridden["maintenance_window"] && s.MaintenanceWindow != nil {
		return s.MaintenanceWindow
	}
	if tmpl != nil && tmpl.MaintenanceWindow != nil {
		return tmpl.MaintenanceWindow
	}
	if defaults.DefaultMaintenanceWindow != nil {
		return defaults.DefaultMaintenanceWindow
	}
	if s.MaintenanceWindow != nil {
		return s.MaintenanceWindow
	}
	return nil
}

func resolveToolConfig(s Schedule, tmpl *Template, defaults ScheduleDefaults) ToolConfig {
	out := ToolConfig{}

	var layers []ToolConfig
	if defaults.DefaultToolConfig != nil {
		layers = append(layers, defaults.DefaultToolConfig)
	}
	if tmpl != nil && tmpl.ToolConfig != nil {
		layers = append(layers, tmpl.ToolConfig)
	}
	if s.ToolConfig != nil {
		layers = append(layers, s.ToolConfig)
	}

	for _, layer := range layers {
		for name, raw := range layer {
			existing, ok := out[name]
			if !ok {
				out[name] = raw
				continue
			}
			out[name] = shallowMergeJSONObjects(existing, raw)
		}
	}
	return out
}

// shallowMergeJSONObjects merges two JSON objects key-by-key, with
// `overlay` winning. If either side fails to decode as an object the
// overlay is returned unchanged — the schedule's intent is authoritative.
func shallowMergeJSONObjects(base, overlay json.RawMessage) json.RawMessage {
	var baseMap, overlayMap map[string]json.RawMessage
	if err := json.Unmarshal(base, &baseMap); err != nil {
		return overlay
	}
	if err := json.Unmarshal(overlay, &overlayMap); err != nil {
		return overlay
	}
	for k, v := range overlayMap {
		baseMap[k] = v
	}
	merged, err := json.Marshal(baseMap)
	if err != nil {
		return overlay
	}
	return merged
}

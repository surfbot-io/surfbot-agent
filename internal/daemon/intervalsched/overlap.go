package intervalsched

import (
	"fmt"
	"time"

	extrrule "github.com/teambition/rrule-go"

	"github.com/surfbot-io/surfbot-agent/internal/model"
)

// OverlapConflict records a pair of occurrences — one from the
// candidate schedule, one from an existing schedule — whose estimated
// run windows would overlap.
type OverlapConflict struct {
	ScheduleID  string
	OccurrenceA time.Time // candidate's occurrence (UTC)
	OccurrenceB time.Time // existing schedule's occurrence (UTC)
}

// ValidateNoOverlap expands the candidate and each existing schedule's
// rrule over [now, now+horizon] and returns every pair of occurrences
// (one per pair, each sharing a target) whose starts sit within
// estimatedDuration of each other.
//
// The function is pure: no store access, no wall-clock reads inside.
// Callers drive "now" through the clock by passing a horizon; the
// implementation uses time.Now as the lower bound.
func ValidateNoOverlap(
	candidate model.Schedule,
	candidateTmpl *model.Template,
	existing []model.Schedule,
	existingTmpls map[string]model.Template,
	defaults model.ScheduleDefaults,
	horizon time.Duration,
	estimatedDuration time.Duration,
) ([]OverlapConflict, error) {
	now := time.Now()
	return validateNoOverlapAt(candidate, candidateTmpl, existing, existingTmpls, defaults, now, horizon, estimatedDuration)
}

func validateNoOverlapAt(
	candidate model.Schedule,
	candidateTmpl *model.Template,
	existing []model.Schedule,
	existingTmpls map[string]model.Template,
	defaults model.ScheduleDefaults,
	now time.Time,
	horizon time.Duration,
	estimatedDuration time.Duration,
) ([]OverlapConflict, error) {
	candOccs, err := expandSchedule(candidate, candidateTmpl, defaults, now, horizon)
	if err != nil {
		return nil, fmt.Errorf("expanding candidate: %w", err)
	}
	if len(candOccs) == 0 {
		return nil, nil
	}

	var conflicts []OverlapConflict
	for _, ex := range existing {
		if ex.ID == candidate.ID {
			continue
		}
		if ex.TargetID != candidate.TargetID {
			continue
		}
		var tmpl *model.Template
		if ex.TemplateID != nil && existingTmpls != nil {
			if t, ok := existingTmpls[*ex.TemplateID]; ok {
				tmpl = &t
			}
		}
		exOccs, err := expandSchedule(ex, tmpl, defaults, now, horizon)
		if err != nil {
			return nil, fmt.Errorf("expanding schedule %s: %w", ex.ID, err)
		}
		for _, a := range candOccs {
			for _, b := range exOccs {
				diff := a.Sub(b)
				if diff < 0 {
					diff = -diff
				}
				if diff < estimatedDuration {
					conflicts = append(conflicts, OverlapConflict{
						ScheduleID:  ex.ID,
						OccurrenceA: a.UTC(),
						OccurrenceB: b.UTC(),
					})
				}
			}
		}
	}
	return conflicts, nil
}

// expandSchedule produces the list of occurrences of `s` between `now`
// and `now+horizon`, honoring the effective rrule and timezone.
func expandSchedule(s model.Schedule, tmpl *model.Template, defaults model.ScheduleDefaults, now time.Time, horizon time.Duration) ([]time.Time, error) {
	eff, err := model.ResolveEffectiveConfig(s, tmpl, defaults)
	if err != nil {
		return nil, err
	}
	loc, err := time.LoadLocation(eff.Timezone)
	if err != nil {
		return nil, fmt.Errorf("loading timezone %q: %w", eff.Timezone, err)
	}
	opt, err := extrrule.StrToROption(eff.RRule)
	if err != nil {
		return nil, fmt.Errorf("parsing rrule: %w", err)
	}
	if !s.DTStart.IsZero() {
		opt.Dtstart = s.DTStart.In(loc)
	} else if !opt.Dtstart.IsZero() {
		opt.Dtstart = opt.Dtstart.In(loc)
	} else {
		opt.Dtstart = now.In(loc)
	}
	rule, err := extrrule.NewRRule(*opt)
	if err != nil {
		return nil, fmt.Errorf("building rrule: %w", err)
	}
	return rule.Between(now, now.Add(horizon), true), nil
}

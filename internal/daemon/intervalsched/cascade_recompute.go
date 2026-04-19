package intervalsched

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/surfbot-io/surfbot-agent/internal/storage"
)

// RecomputeNextRunForTemplate walks every schedule referencing
// templateID and recomputes its next_run_at via `expander`. Schedules
// that override the template's rrule or timezone are skipped (their
// cadence is set at the schedule layer and is unaffected by the
// template change).
//
// Returns the number of schedules whose next_run_at was updated. Errors
// expanding a single schedule do not abort the loop — they are logged
// and the schedule is skipped; `affected` reflects successes only.
//
// SCHED1.2a ships this as library code; SCHED1.2b wires it into
// TemplateStore.Update so the cascade fires automatically.
func RecomputeNextRunForTemplate(
	ctx context.Context,
	templateID string,
	schedStore storage.ScheduleStore,
	tmplStore storage.TemplateStore,
	expander *RRuleExpander,
) (int, error) {
	if expander == nil {
		return 0, fmt.Errorf("recompute: expander is required")
	}
	tmpl, err := tmplStore.Get(ctx, templateID)
	if err != nil {
		return 0, fmt.Errorf("recompute: get template %s: %w", templateID, err)
	}
	schedules, err := schedStore.ListByTemplate(ctx, templateID)
	if err != nil {
		return 0, fmt.Errorf("recompute: list schedules for template %s: %w", templateID, err)
	}

	var affected int
	for _, s := range schedules {
		if overrideContains(s.Overrides, "rrule") || overrideContains(s.Overrides, "timezone") {
			continue
		}
		next, err := expander.ComputeNextRunAt(s, tmpl)
		if err != nil {
			slog.Default().Error("recompute: expand schedule failed",
				"schedule_id", s.ID, "template_id", templateID, "error", err)
			continue
		}
		if err := schedStore.SetNextRunAt(ctx, s.ID, next); err != nil {
			slog.Default().Error("recompute: set next_run_at failed",
				"schedule_id", s.ID, "template_id", templateID, "error", err)
			continue
		}
		affected++
	}
	return affected, nil
}

func overrideContains(overrides []string, name string) bool {
	for _, o := range overrides {
		if o == name {
			return true
		}
	}
	return false
}

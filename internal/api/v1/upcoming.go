package v1

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"time"

	extrrule "github.com/teambition/rrule-go"

	"github.com/surfbot-io/surfbot-agent/internal/model"
)

const (
	defaultUpcomingHorizon = 24 * time.Hour
	maxUpcomingHorizon     = 30 * 24 * time.Hour
	defaultUpcomingLimit   = 100
	maxUpcomingLimit       = 1000
)

// UpcomingFiring describes a single scheduled run in the horizon window.
type UpcomingFiring struct {
	ScheduleID string    `json:"schedule_id"`
	TargetID   string    `json:"target_id"`
	TemplateID *string   `json:"template_id,omitempty"`
	FiresAt    time.Time `json:"fires_at"`
}

// UpcomingBlackout is a single occurrence of a blackout window that
// intersects the horizon. Emitted so UIs can render shaded regions in
// the calendar without re-expanding the rrule client-side.
type UpcomingBlackout struct {
	BlackoutID string    `json:"blackout_id"`
	StartsAt   time.Time `json:"starts_at"`
	EndsAt     time.Time `json:"ends_at"`
}

// UpcomingResponse is the GET /api/v1/schedules/upcoming body.
type UpcomingResponse struct {
	Items              []UpcomingFiring   `json:"items"`
	HorizonEnd         time.Time          `json:"horizon_end"`
	BlackoutsInHorizon []UpcomingBlackout `json:"blackouts_in_horizon"`
}

func (h *handlers) routeUpcoming(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, "GET")
		return
	}

	q := r.URL.Query()
	horizon := defaultUpcomingHorizon
	if v := q.Get("horizon"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			writeProblem(w, http.StatusBadRequest, "/problems/invalid-query",
				"Invalid horizon", "must be a positive duration", nil)
			return
		}
		if d > maxUpcomingHorizon {
			d = maxUpcomingHorizon
		}
		horizon = d
	}

	limit := defaultUpcomingLimit
	p := ParsePagination(r, maxUpcomingLimit)
	if p.Limit > 0 {
		limit = p.Limit
	}

	now := time.Now().UTC()
	end := now.Add(horizon)

	defaults := model.ScheduleDefaults{}
	if h.deps.DefaultsStore != nil {
		if d, err := h.deps.DefaultsStore.Get(r.Context()); err == nil && d != nil {
			defaults = *d
		}
	}

	var schedules []model.Schedule
	var err error
	if tgt := q.Get("target_id"); tgt != "" {
		schedules, err = h.deps.ScheduleStore.ListByTarget(r.Context(), tgt)
	} else {
		schedules, err = h.deps.ScheduleStore.ListAll(r.Context())
	}
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "/problems/store",
			"Listing schedules failed", err.Error(), nil)
		return
	}

	// Collect all firings. Paused schedules are skipped.
	var firings []UpcomingFiring
	for _, s := range schedules {
		if !s.Enabled {
			continue
		}
		tmpl := h.loadUpcomingTemplate(r.Context(), s.TemplateID)
		occs, err := expandScheduleBetween(s, tmpl, defaults, now, end)
		if err != nil {
			continue
		}
		for _, occ := range occs {
			firings = append(firings, UpcomingFiring{
				ScheduleID: s.ID,
				TargetID:   s.TargetID,
				TemplateID: s.TemplateID,
				FiresAt:    occ.UTC(),
			})
		}
	}

	sort.Slice(firings, func(i, j int) bool {
		return firings[i].FiresAt.Before(firings[j].FiresAt)
	})
	if len(firings) > limit {
		firings = firings[:limit]
	}

	// Blackouts inside the horizon.
	blackouts := []UpcomingBlackout{}
	if h.deps.BlackoutStore != nil {
		all, berr := h.deps.BlackoutStore.List(r.Context())
		if berr == nil {
			for _, b := range all {
				if !b.Enabled {
					continue
				}
				occs, oerr := blackoutOccurrencesIn(b, now, end)
				if oerr != nil {
					continue
				}
				blackouts = append(blackouts, occs...)
			}
		}
	}
	sort.Slice(blackouts, func(i, j int) bool {
		return blackouts[i].StartsAt.Before(blackouts[j].StartsAt)
	})

	if firings == nil {
		firings = []UpcomingFiring{}
	}
	writeJSON(w, http.StatusOK, UpcomingResponse{
		Items:              firings,
		HorizonEnd:         end,
		BlackoutsInHorizon: blackouts,
	})
}

func (h *handlers) loadUpcomingTemplate(ctx context.Context, id *string) *model.Template {
	if id == nil || *id == "" || h.deps.TemplateStore == nil {
		return nil
	}
	t, err := h.deps.TemplateStore.Get(ctx, *id)
	if err != nil {
		return nil
	}
	return t
}

// expandScheduleBetween enumerates all occurrences of `s` between `from`
// and `to` using the effective rrule / timezone from the cascade. A
// thin re-implementation of intervalsched.expandSchedule (which is
// private); duplicating the ~15 LOC is cheaper than elevating it.
func expandScheduleBetween(s model.Schedule, tmpl *model.Template, defaults model.ScheduleDefaults, from, to time.Time) ([]time.Time, error) {
	eff, err := model.ResolveEffectiveConfig(s, tmpl, defaults)
	if err != nil {
		return nil, err
	}
	loc, err := time.LoadLocation(eff.Timezone)
	if err != nil {
		return nil, fmt.Errorf("tz: %w", err)
	}
	opt, err := extrrule.StrToROption(eff.RRule)
	if err != nil {
		return nil, fmt.Errorf("rrule: %w", err)
	}
	if !s.DTStart.IsZero() {
		opt.Dtstart = s.DTStart.In(loc)
	} else if !opt.Dtstart.IsZero() {
		opt.Dtstart = opt.Dtstart.In(loc)
	} else {
		opt.Dtstart = from.In(loc)
	}
	rule, err := extrrule.NewRRule(*opt)
	if err != nil {
		return nil, err
	}
	return rule.Between(from, to, true), nil
}

func blackoutOccurrencesIn(b model.BlackoutWindow, from, to time.Time) ([]UpcomingBlackout, error) {
	loc, err := time.LoadLocation(b.Timezone)
	if err != nil {
		return nil, err
	}
	opt, err := extrrule.StrToROption(b.RRule)
	if err != nil {
		return nil, err
	}
	if opt.Dtstart.IsZero() {
		opt.Dtstart = b.CreatedAt.In(loc)
	} else {
		opt.Dtstart = opt.Dtstart.In(loc)
	}
	rule, err := extrrule.NewRRule(*opt)
	if err != nil {
		return nil, err
	}
	dur := time.Duration(b.DurationSec) * time.Second
	// Start the scan blackoutHorizonPast before `from` so an in-progress
	// window whose start precedes the horizon still shows up.
	lower := from.Add(-7 * 24 * time.Hour)
	occs := rule.Between(lower, to, true)
	out := make([]UpcomingBlackout, 0, len(occs))
	for _, occ := range occs {
		endAt := occ.Add(dur)
		if endAt.Before(from) {
			continue
		}
		if occ.After(to) {
			break
		}
		out = append(out, UpcomingBlackout{
			BlackoutID: b.ID,
			StartsAt:   occ.UTC(),
			EndsAt:     endAt.UTC(),
		})
	}
	return out, nil
}

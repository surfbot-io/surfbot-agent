package intervalsched

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"time"

	extrrule "github.com/teambition/rrule-go"

	"github.com/surfbot-io/surfbot-agent/internal/model"
)

// ErrBlackoutSaturated is returned by RRuleExpander.ComputeNextRunAt
// when the blackout-skip loop fails to find a free slot within the
// safety budget. In production this signals a misconfigured blackout
// that covers the entire rrule horizon.
var ErrBlackoutSaturated = errors.New("blackout saturated rrule horizon")

// maxBlackoutSkipIterations caps the blackout-skip loop defensively.
const maxBlackoutSkipIterations = 100

// RRuleExpander computes the next fire time for a schedule, honoring
// the effective rrule, timezone, jitter, and any active blackout
// windows (which are skipped over).
type RRuleExpander struct {
	defaults  model.ScheduleDefaults
	blackouts *BlackoutEvaluator
	clock     Clock

	mu        sync.Mutex
	jitterRNG *rand.Rand
}

// NewRRuleExpander constructs an expander. `seed` seeds the jitter RNG;
// callers that want nondeterministic jitter pass time.Now().UnixNano().
func NewRRuleExpander(defaults model.ScheduleDefaults, blackouts *BlackoutEvaluator, clock Clock, seed int64) *RRuleExpander {
	if clock == nil {
		clock = NewRealClock()
	}
	return &RRuleExpander{
		defaults:  defaults,
		blackouts: blackouts,
		clock:     clock,
		jitterRNG: rand.New(rand.NewSource(seed)),
	}
}

// ComputeNextRunAt returns the next UTC fire time for `s`, or nil when
// the schedule can never fire again (UNTIL in past, COUNT exhausted).
// Returns ErrBlackoutSaturated when blackouts cover the rrule horizon.
func (e *RRuleExpander) ComputeNextRunAt(s model.Schedule, tmpl *model.Template) (*time.Time, error) {
	eff, err := model.ResolveEffectiveConfig(s, tmpl, e.defaults)
	if err != nil {
		return nil, fmt.Errorf("resolving effective config: %w", err)
	}

	loc, err := time.LoadLocation(eff.Timezone)
	if err != nil {
		return nil, fmt.Errorf("loading timezone %q: %w", eff.Timezone, err)
	}

	opt, err := extrrule.StrToROption(eff.RRule)
	if err != nil {
		return nil, fmt.Errorf("parsing rrule: %w", err)
	}
	dtstart := s.DTStart
	if !dtstart.IsZero() {
		opt.Dtstart = dtstart.In(loc)
	} else if !opt.Dtstart.IsZero() {
		opt.Dtstart = opt.Dtstart.In(loc)
	} else {
		opt.Dtstart = e.clock.Now().In(loc)
	}

	rule, err := extrrule.NewRRule(*opt)
	if err != nil {
		return nil, fmt.Errorf("building rrule: %w", err)
	}

	now := e.clock.Now()
	candidate := rule.After(now, false)
	if candidate.IsZero() {
		return nil, nil
	}
	candidate = candidate.Add(e.nextJitter())

	if e.blackouts != nil {
		for i := 0; i < maxBlackoutSkipIterations; i++ {
			active, _ := e.blackouts.IsActive(s.TargetID, candidate)
			if !active {
				out := candidate.UTC()
				return &out, nil
			}
			end := e.blackouts.NextWindowEnd(s.TargetID, candidate)
			if end == nil {
				// Defensive: IsActive said yes but NextWindowEnd said no.
				// Nudge forward by 1s and retry.
				candidate = candidate.Add(time.Second)
				continue
			}
			next := rule.After(*end, false)
			if next.IsZero() {
				return nil, nil
			}
			candidate = next.Add(e.nextJitter())
		}
		return nil, ErrBlackoutSaturated
	}

	out := candidate.UTC()
	return &out, nil
}

func (e *RRuleExpander) nextJitter() time.Duration {
	if e.defaults.JitterSeconds <= 0 {
		return 0
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return time.Duration(e.jitterRNG.Intn(e.defaults.JitterSeconds+1)) * time.Second
}

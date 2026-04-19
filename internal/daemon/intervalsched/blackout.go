package intervalsched

import (
	"context"
	"fmt"
	"sync"
	"time"

	extrrule "github.com/teambition/rrule-go"

	"github.com/surfbot-io/surfbot-agent/internal/model"
	"github.com/surfbot-io/surfbot-agent/internal/storage"
)

// cacheTTL controls how stale a BlackoutEvaluator's snapshot may be
// before IsActive triggers an implicit Refresh. Callers on hot paths
// should Refresh explicitly rather than relying on the implicit TTL.
const cacheTTL = 30 * time.Second

// blackoutHorizonPast is how far back IsActive scans for occurrences
// whose duration still covers `at`. A blackout that started N seconds
// ago is active at `at` only if N < duration; scanning back 7 days
// accommodates multi-day weekend windows.
const blackoutHorizonPast = 7 * 24 * time.Hour

// BlackoutEvaluator answers "is target T subject to an active blackout
// at time at?". It caches the current set of enabled windows (global +
// per-target) plus the parsed RRule for each. Refresh rebuilds the
// cache from the store.
//
// The evaluator is safe for concurrent use. When IsActive observes a
// stale or never-populated cache it does an implicit Refresh behind the
// lock — callers on hot paths should Refresh explicitly before heavy
// batches to keep that work off the evaluation path.
type BlackoutEvaluator struct {
	store storage.BlackoutStore

	mu           sync.Mutex
	global       []cachedWindow
	perTarget    map[string][]cachedWindow
	lastRefresh  time.Time
	refreshErr   error
	populatedOK  bool
}

type cachedWindow struct {
	window model.BlackoutWindow
	rule   *extrrule.RRule
	loc    *time.Location
}

// NewBlackoutEvaluator constructs an evaluator backed by the given
// store. Callers must Refresh at least once; IsActive / NextWindowEnd
// will do so lazily if needed.
func NewBlackoutEvaluator(store storage.BlackoutStore) *BlackoutEvaluator {
	return &BlackoutEvaluator{
		store:     store,
		perTarget: map[string][]cachedWindow{},
	}
}

// Refresh reloads all enabled blackout windows from the store and
// re-parses their RRULEs. Safe to call concurrently with IsActive /
// NextWindowEnd.
func (e *BlackoutEvaluator) Refresh(ctx context.Context) error {
	windows, err := e.store.List(ctx)
	if err != nil {
		e.mu.Lock()
		e.refreshErr = err
		e.mu.Unlock()
		return fmt.Errorf("listing blackouts: %w", err)
	}

	global := make([]cachedWindow, 0)
	perTarget := map[string][]cachedWindow{}

	for _, w := range windows {
		if !w.Enabled {
			continue
		}
		cached, err := buildCachedWindow(w)
		if err != nil {
			// Skip broken windows rather than poisoning the cache. In
			// production they can't land — the store validates on
			// Create/Update — but defensive skip keeps us honest.
			continue
		}
		switch w.Scope {
		case model.BlackoutScopeGlobal:
			global = append(global, cached)
		case model.BlackoutScopeTarget:
			if w.TargetID == nil || *w.TargetID == "" {
				continue
			}
			perTarget[*w.TargetID] = append(perTarget[*w.TargetID], cached)
		}
	}

	e.mu.Lock()
	e.global = global
	e.perTarget = perTarget
	e.lastRefresh = time.Now()
	e.refreshErr = nil
	e.populatedOK = true
	e.mu.Unlock()
	return nil
}

func buildCachedWindow(w model.BlackoutWindow) (cachedWindow, error) {
	loc, err := time.LoadLocation(w.Timezone)
	if err != nil {
		return cachedWindow{}, fmt.Errorf("loading timezone %q: %w", w.Timezone, err)
	}
	opt, err := extrrule.StrToROption(w.RRule)
	if err != nil {
		return cachedWindow{}, fmt.Errorf("parsing rrule: %w", err)
	}
	if opt.Dtstart.IsZero() {
		opt.Dtstart = w.CreatedAt.In(loc)
	} else {
		opt.Dtstart = opt.Dtstart.In(loc)
	}
	rule, err := extrrule.NewRRule(*opt)
	if err != nil {
		return cachedWindow{}, fmt.Errorf("building rrule: %w", err)
	}
	return cachedWindow{window: w, rule: rule, loc: loc}, nil
}

// IsActive reports whether targetID is inside a blackout window at
// time `at`. Global windows are evaluated first; the first hit wins.
// Returns (false, nil) otherwise.
//
// Window boundaries are [start, start+duration): a window starting at
// 12:00 with duration 1h is active at 12:00:00 but not at 13:00:00.
func (e *BlackoutEvaluator) IsActive(targetID string, at time.Time) (bool, *model.BlackoutWindow) {
	e.ensureFreshLocked()

	e.mu.Lock()
	globals := e.global
	targetWindows := append([]cachedWindow(nil), e.perTarget[targetID]...)
	e.mu.Unlock()

	if w := firstActive(globals, at); w != nil {
		return true, w
	}
	if w := firstActive(targetWindows, at); w != nil {
		return true, w
	}
	return false, nil
}

// NextWindowEnd returns the end time of the currently-active blackout
// window for targetID at `at`. Returns nil when no window is active.
func (e *BlackoutEvaluator) NextWindowEnd(targetID string, at time.Time) *time.Time {
	active, w := e.IsActive(targetID, at)
	if !active || w == nil {
		return nil
	}
	end := findActiveEnd(e, targetID, *w, at)
	return &end
}

func (e *BlackoutEvaluator) ensureFreshLocked() {
	e.mu.Lock()
	stale := !e.populatedOK || time.Since(e.lastRefresh) > cacheTTL
	e.mu.Unlock()
	if !stale {
		return
	}
	_ = e.Refresh(context.Background())
}

// firstActive returns the first window in `windows` that covers `at`,
// or nil. The returned pointer references a copy of the stored
// model.BlackoutWindow.
func firstActive(windows []cachedWindow, at time.Time) *model.BlackoutWindow {
	for i := range windows {
		if occurrenceCovers(windows[i], at) {
			w := windows[i].window
			return &w
		}
	}
	return nil
}

// occurrenceCovers reports whether any occurrence of cw covers `at`.
func occurrenceCovers(cw cachedWindow, at time.Time) bool {
	dur := time.Duration(cw.window.DurationSec) * time.Second
	if dur <= 0 {
		return false
	}
	atInTZ := at.In(cw.loc)
	lower := atInTZ.Add(-blackoutHorizonPast)
	// Between(lower, atInTZ+1ns, true) returns every occurrence whose
	// start is inside the inclusive range. Any such occurrence whose
	// start+duration covers `at` makes the window active.
	upper := atInTZ.Add(time.Nanosecond)
	for _, t := range cw.rule.Between(lower, upper, true) {
		end := t.Add(dur)
		if (atInTZ.Equal(t) || atInTZ.After(t)) && atInTZ.Before(end) {
			return true
		}
	}
	return false
}

// findActiveEnd scans cached windows for `targetID` (and globals) to
// locate the currently-active occurrence of `w` and return its end
// time. If no occurrence is found (unexpected — caller just confirmed
// one), it falls back to at + 1s to avoid infinite loops upstream.
func findActiveEnd(e *BlackoutEvaluator, targetID string, w model.BlackoutWindow, at time.Time) time.Time {
	e.mu.Lock()
	var candidates []cachedWindow
	for i := range e.global {
		if e.global[i].window.ID == w.ID {
			candidates = append(candidates, e.global[i])
		}
	}
	for i := range e.perTarget[targetID] {
		if e.perTarget[targetID][i].window.ID == w.ID {
			candidates = append(candidates, e.perTarget[targetID][i])
		}
	}
	e.mu.Unlock()

	for _, cw := range candidates {
		if end, ok := activeOccurrenceEnd(cw, at); ok {
			return end
		}
	}
	return at.Add(time.Second)
}

func activeOccurrenceEnd(cw cachedWindow, at time.Time) (time.Time, bool) {
	dur := time.Duration(cw.window.DurationSec) * time.Second
	if dur <= 0 {
		return time.Time{}, false
	}
	atInTZ := at.In(cw.loc)
	lower := atInTZ.Add(-blackoutHorizonPast)
	upper := atInTZ.Add(time.Nanosecond)
	for _, t := range cw.rule.Between(lower, upper, true) {
		end := t.Add(dur)
		if (atInTZ.Equal(t) || atInTZ.After(t)) && atInTZ.Before(end) {
			return end, true
		}
	}
	return time.Time{}, false
}

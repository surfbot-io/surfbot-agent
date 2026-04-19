package intervalsched

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/surfbot-io/surfbot-agent/internal/model"
)

// frozenClock implements the package's Clock interface with a frozen now.
type frozenClock struct{ now time.Time }

func (f frozenClock) Now() time.Time                   { return f.now }
func (f frozenClock) NewTimer(d time.Duration) Timer   { return &frozenTimer{} }

type frozenTimer struct{}

func (*frozenTimer) C() <-chan time.Time { return nil }
func (*frozenTimer) Stop() bool          { return true }

func mkSchedule(rrule, tz string, dtstart time.Time, targetID string) model.Schedule {
	return model.Schedule{
		ID:       "s-" + targetID,
		TargetID: targetID,
		Name:     "n",
		RRule:    rrule,
		Timezone: tz,
		DTStart:  dtstart,
		Enabled:  true,
	}
}

func TestRRuleExpander_Basic(t *testing.T) {
	t.Parallel()
	defaults := model.ScheduleDefaults{JitterSeconds: 0}
	now := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)
	exp := NewRRuleExpander(defaults, nil, frozenClock{now: now}, 1)

	dtstart := time.Date(2026, 4, 18, 2, 0, 0, 0, time.UTC)
	s := mkSchedule("FREQ=DAILY;BYHOUR=2;BYMINUTE=0;BYSECOND=0", "UTC", dtstart, "t")

	next, err := exp.ComputeNextRunAt(s, nil)
	if err != nil {
		t.Fatalf("ComputeNextRunAt: %v", err)
	}
	if next == nil {
		t.Fatalf("expected a next time")
	}
	want := time.Date(2026, 4, 20, 2, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("got %s, want %s", next, want)
	}
}

func TestRRuleExpander_UntilInPast(t *testing.T) {
	t.Parallel()
	defaults := model.ScheduleDefaults{}
	now := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)
	exp := NewRRuleExpander(defaults, nil, frozenClock{now: now}, 1)

	dtstart := time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC)
	s := mkSchedule("FREQ=DAILY;UNTIL=20200101T000000Z", "UTC", dtstart, "t")

	next, err := exp.ComputeNextRunAt(s, nil)
	if err != nil {
		t.Fatalf("ComputeNextRunAt: %v", err)
	}
	if next != nil {
		t.Fatalf("expected nil, got %s", next)
	}
}

func TestRRuleExpander_CountExhausted(t *testing.T) {
	t.Parallel()
	defaults := model.ScheduleDefaults{}
	now := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)
	exp := NewRRuleExpander(defaults, nil, frozenClock{now: now}, 1)

	// DTSTART 5 days ago, COUNT=3 → fires have all happened.
	dtstart := now.AddDate(0, 0, -5)
	s := mkSchedule("FREQ=DAILY;COUNT=3", "UTC", dtstart, "t")

	next, err := exp.ComputeNextRunAt(s, nil)
	if err != nil {
		t.Fatalf("ComputeNextRunAt: %v", err)
	}
	if next != nil {
		t.Fatalf("expected nil, got %s", next)
	}
}

func TestRRuleExpander_DST_Spring(t *testing.T) {
	t.Parallel()
	defaults := model.ScheduleDefaults{}
	// 2026-03-08 is US spring-forward (02:00 → 03:00 local).
	loc, _ := time.LoadLocation("America/New_York")
	// Start before the gap.
	dtstart := time.Date(2026, 3, 7, 2, 30, 0, 0, loc)
	now := time.Date(2026, 3, 8, 0, 0, 0, 0, time.UTC)
	exp := NewRRuleExpander(defaults, nil, frozenClock{now: now}, 1)
	s := mkSchedule("FREQ=DAILY;BYHOUR=2;BYMINUTE=30;BYSECOND=0", "America/New_York", dtstart, "t")

	next, err := exp.ComputeNextRunAt(s, nil)
	if err != nil {
		t.Fatalf("ComputeNextRunAt: %v", err)
	}
	if next == nil {
		t.Fatalf("expected next time")
	}
	// Must be strictly after `now`. We don't pin exactly when rrule-go
	// puts the missing 02:30 — just that it doesn't return before now.
	if !next.After(now) {
		t.Fatalf("next %s should be after now %s", next, now)
	}
}

func TestRRuleExpander_DST_Fall(t *testing.T) {
	t.Parallel()
	defaults := model.ScheduleDefaults{}
	loc, _ := time.LoadLocation("America/New_York")
	// Start before fall-back.
	dtstart := time.Date(2026, 10, 30, 1, 30, 0, 0, loc)
	now := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	exp := NewRRuleExpander(defaults, nil, frozenClock{now: now}, 1)
	s := mkSchedule("FREQ=DAILY;BYHOUR=1;BYMINUTE=30;BYSECOND=0", "America/New_York", dtstart, "t")

	next, err := exp.ComputeNextRunAt(s, nil)
	if err != nil {
		t.Fatalf("ComputeNextRunAt: %v", err)
	}
	if next == nil {
		t.Fatalf("expected next time")
	}
	if !next.After(now) {
		t.Fatalf("next %s should be after %s", next, now)
	}
}

func TestRRuleExpander_JitterDeterministic(t *testing.T) {
	t.Parallel()
	defaults := model.ScheduleDefaults{JitterSeconds: 60}
	now := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)
	dtstart := time.Date(2026, 4, 18, 2, 0, 0, 0, time.UTC)
	s := mkSchedule("FREQ=DAILY;BYHOUR=2;BYMINUTE=0;BYSECOND=0", "UTC", dtstart, "t")

	exp1 := NewRRuleExpander(defaults, nil, frozenClock{now: now}, 42)
	exp2 := NewRRuleExpander(defaults, nil, frozenClock{now: now}, 42)

	n1, err := exp1.ComputeNextRunAt(s, nil)
	if err != nil {
		t.Fatalf("ComputeNextRunAt 1: %v", err)
	}
	n2, err := exp2.ComputeNextRunAt(s, nil)
	if err != nil {
		t.Fatalf("ComputeNextRunAt 2: %v", err)
	}
	if n1 == nil || n2 == nil {
		t.Fatalf("expected both non-nil")
	}
	if !n1.Equal(*n2) {
		t.Fatalf("same seed yielded different times: %s vs %s", n1, n2)
	}
}

func TestRRuleExpander_BlackoutSkip(t *testing.T) {
	t.Parallel()
	defaults := model.ScheduleDefaults{}
	now := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)

	// Blackout [12:00, 15:00) UTC, daily.
	store := newFakeStore()
	store.set([]model.BlackoutWindow{
		mkWindow("g", model.BlackoutScopeGlobal, nil,
			fmt.Sprintf("DTSTART:%s\nRRULE:FREQ=DAILY;BYHOUR=12;BYMINUTE=0;BYSECOND=0", now.Format("20060102T150405Z")),
			3*time.Hour, "UTC", now),
	})
	ev := NewBlackoutEvaluator(store)
	if err := ev.Refresh(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	exp := NewRRuleExpander(defaults, ev, frozenClock{now: now}, 1)
	dtstart := time.Date(2026, 4, 19, 0, 0, 0, 0, time.UTC)
	s := mkSchedule("FREQ=HOURLY", "UTC", dtstart, "t")

	next, err := exp.ComputeNextRunAt(s, nil)
	if err != nil {
		t.Fatalf("ComputeNextRunAt: %v", err)
	}
	if next == nil {
		t.Fatalf("expected a next time")
	}
	blackoutEnd := time.Date(2026, 4, 19, 15, 0, 0, 0, time.UTC)
	if next.Before(blackoutEnd) {
		t.Fatalf("next %s should be at or after blackout end %s", next, blackoutEnd)
	}
}

func TestRRuleExpander_BlackoutSaturated(t *testing.T) {
	t.Parallel()
	defaults := model.ScheduleDefaults{}
	now := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)

	// Blackout that covers every hour: FREQ=HOURLY, duration=3600s.
	store := newFakeStore()
	store.set([]model.BlackoutWindow{
		mkWindow("g", model.BlackoutScopeGlobal, nil,
			fmt.Sprintf("DTSTART:%s\nRRULE:FREQ=HOURLY", now.Format("20060102T150405Z")),
			time.Hour, "UTC", now),
	})
	ev := NewBlackoutEvaluator(store)
	if err := ev.Refresh(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	exp := NewRRuleExpander(defaults, ev, frozenClock{now: now}, 1)
	dtstart := time.Date(2026, 4, 19, 0, 0, 0, 0, time.UTC)
	s := mkSchedule("FREQ=HOURLY", "UTC", dtstart, "t")

	_, err := exp.ComputeNextRunAt(s, nil)
	if !errors.Is(err, ErrBlackoutSaturated) {
		t.Fatalf("expected ErrBlackoutSaturated, got %v", err)
	}
}


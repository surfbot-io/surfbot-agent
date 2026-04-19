package intervalsched

import (
	"testing"
	"time"

	"github.com/surfbot-io/surfbot-agent/internal/model"
)

func makeSched(id, targetID, rrule string, dtstart time.Time) model.Schedule {
	return model.Schedule{
		ID:       id,
		TargetID: targetID,
		Name:     id,
		RRule:    rrule,
		Timezone: "UTC",
		DTStart:  dtstart,
		Enabled:  true,
	}
}

func TestValidateNoOverlap_Clean(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 19, 0, 0, 0, 0, time.UTC)
	dtstart := now.Add(-time.Hour)
	cand := makeSched("cand", "T", "FREQ=HOURLY;BYMINUTE=0", dtstart)
	other := makeSched("other", "T", "FREQ=HOURLY;BYMINUTE=30", dtstart)

	conflicts, err := validateNoOverlapAt(cand, nil, []model.Schedule{other}, nil,
		model.ScheduleDefaults{}, now, 24*time.Hour, 10*time.Minute)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if len(conflicts) != 0 {
		t.Fatalf("expected 0 conflicts, got %d: %+v", len(conflicts), conflicts)
	}
}

func TestValidateNoOverlap_Conflict(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 19, 0, 0, 0, 0, time.UTC)
	dtstart := now.Add(-time.Hour)
	cand := makeSched("cand", "T", "FREQ=HOURLY;BYMINUTE=0", dtstart)
	other := makeSched("other", "T", "FREQ=HOURLY;BYMINUTE=0", dtstart)

	conflicts, err := validateNoOverlapAt(cand, nil, []model.Schedule{other}, nil,
		model.ScheduleDefaults{}, now, 24*time.Hour, 10*time.Minute)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if len(conflicts) == 0 {
		t.Fatalf("expected conflicts")
	}
	// Over 24h and hourly → ≥ 24 conflicts.
	if len(conflicts) < 24 {
		t.Fatalf("expected ≥24 conflicts, got %d", len(conflicts))
	}
}

func TestValidateNoOverlap_DifferentTargets(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 19, 0, 0, 0, 0, time.UTC)
	dtstart := now.Add(-time.Hour)
	cand := makeSched("cand", "T", "FREQ=HOURLY;BYMINUTE=0", dtstart)
	other := makeSched("other", "U", "FREQ=HOURLY;BYMINUTE=0", dtstart)

	conflicts, err := validateNoOverlapAt(cand, nil, []model.Schedule{other}, nil,
		model.ScheduleDefaults{}, now, 24*time.Hour, 10*time.Minute)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if len(conflicts) != 0 {
		t.Fatalf("different target should yield no conflicts, got %d", len(conflicts))
	}
}

func TestValidateNoOverlap_EstimatedDuration(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 19, 0, 0, 0, 0, time.UTC)
	dtstart := now.Add(-time.Hour)
	cand := makeSched("cand", "T", "FREQ=HOURLY;BYMINUTE=0", dtstart)
	other := makeSched("other", "T", "FREQ=HOURLY;BYMINUTE=30", dtstart)

	// 60-min estimated duration: :00 and :30 slots collide.
	conflicts, err := validateNoOverlapAt(cand, nil, []model.Schedule{other}, nil,
		model.ScheduleDefaults{}, now, 6*time.Hour, 60*time.Minute)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if len(conflicts) == 0 {
		t.Fatalf("expected conflicts with 60m estimated duration")
	}
}

func TestValidateNoOverlap_EmptyExisting(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 19, 0, 0, 0, 0, time.UTC)
	dtstart := now.Add(-time.Hour)
	cand := makeSched("cand", "T", "FREQ=HOURLY", dtstart)
	conflicts, err := validateNoOverlapAt(cand, nil, nil, nil,
		model.ScheduleDefaults{}, now, 24*time.Hour, 10*time.Minute)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if len(conflicts) != 0 {
		t.Fatalf("expected no conflicts, got %d", len(conflicts))
	}
}

func TestValidateNoOverlap_CandidateUntilPast(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 19, 0, 0, 0, 0, time.UTC)
	dtstart := time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC)
	cand := makeSched("cand", "T", "FREQ=DAILY;UNTIL=20200101T000000Z", dtstart)
	other := makeSched("other", "T", "FREQ=HOURLY", now.Add(-time.Hour))
	conflicts, err := validateNoOverlapAt(cand, nil, []model.Schedule{other}, nil,
		model.ScheduleDefaults{}, now, 24*time.Hour, 10*time.Minute)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if len(conflicts) != 0 {
		t.Fatalf("candidate with UNTIL in past should yield no conflicts, got %d", len(conflicts))
	}
}

func TestValidateNoOverlap_SelfExcluded(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 19, 0, 0, 0, 0, time.UTC)
	dtstart := now.Add(-time.Hour)
	s := makeSched("same", "T", "FREQ=HOURLY", dtstart)
	// Pass the candidate itself in the existing list — must be skipped.
	conflicts, err := validateNoOverlapAt(s, nil, []model.Schedule{s}, nil,
		model.ScheduleDefaults{}, now, 6*time.Hour, 10*time.Minute)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if len(conflicts) != 0 {
		t.Fatalf("self should be excluded, got %d conflicts", len(conflicts))
	}
}

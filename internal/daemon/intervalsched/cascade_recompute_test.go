package intervalsched

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/surfbot-io/surfbot-agent/internal/model"
	"github.com/surfbot-io/surfbot-agent/internal/storage"
)

// fakeTemplateStore is a minimal in-memory stand-in for
// storage.TemplateStore.
type fakeTemplateStore struct {
	mu        sync.Mutex
	templates map[string]model.Template
}

func newFakeTemplateStore() *fakeTemplateStore {
	return &fakeTemplateStore{templates: map[string]model.Template{}}
}

func (f *fakeTemplateStore) Create(ctx context.Context, t *model.Template) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.templates[t.ID] = *t
	return nil
}
func (f *fakeTemplateStore) Get(ctx context.Context, id string) (*model.Template, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.templates[id]
	if !ok {
		return nil, storage.ErrNotFound
	}
	return &t, nil
}
func (f *fakeTemplateStore) GetByName(ctx context.Context, name string) (*model.Template, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeTemplateStore) List(ctx context.Context) ([]model.Template, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeTemplateStore) Update(ctx context.Context, t *model.Template) error {
	return errors.New("not implemented")
}
func (f *fakeTemplateStore) Delete(ctx context.Context, id string) error {
	return errors.New("not implemented")
}

var _ storage.TemplateStore = (*fakeTemplateStore)(nil)

// fakeScheduleStore supports the recompute flow: ListByTemplate and
// SetNextRunAt. Other methods return errors.
type fakeScheduleStore struct {
	mu        sync.Mutex
	schedules []model.Schedule
	nextRun   map[string]*time.Time
	setErr    map[string]error // schedule-id → error to return from SetNextRunAt
}

func newFakeScheduleStore() *fakeScheduleStore {
	return &fakeScheduleStore{
		nextRun: map[string]*time.Time{},
		setErr:  map[string]error{},
	}
}

func (f *fakeScheduleStore) Create(ctx context.Context, s *model.Schedule) error {
	return errors.New("not implemented")
}
func (f *fakeScheduleStore) Get(ctx context.Context, id string) (*model.Schedule, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeScheduleStore) GetByTargetAndName(ctx context.Context, targetID, name string) (*model.Schedule, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeScheduleStore) ListByTarget(ctx context.Context, targetID string) ([]model.Schedule, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeScheduleStore) ListAll(ctx context.Context) ([]model.Schedule, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeScheduleStore) ListDue(ctx context.Context, now time.Time, limit int) ([]model.Schedule, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeScheduleStore) ListByTemplate(ctx context.Context, templateID string) ([]model.Schedule, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []model.Schedule
	for _, s := range f.schedules {
		if s.TemplateID != nil && *s.TemplateID == templateID {
			out = append(out, s)
		}
	}
	return out, nil
}
func (f *fakeScheduleStore) Update(ctx context.Context, s *model.Schedule) error {
	return errors.New("not implemented")
}
func (f *fakeScheduleStore) SetNextRunAt(ctx context.Context, id string, next *time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.setErr[id]; err != nil {
		return err
	}
	f.nextRun[id] = next
	return nil
}
func (f *fakeScheduleStore) RecordRun(ctx context.Context, id string, status model.ScheduleRunStatus, scanID *string, at time.Time) error {
	return errors.New("not implemented")
}
func (f *fakeScheduleStore) Delete(ctx context.Context, id string) error {
	return errors.New("not implemented")
}
func (f *fakeScheduleStore) CountByTarget(ctx context.Context, targetID string) (int, error) {
	return 0, errors.New("not implemented")
}

var _ storage.ScheduleStore = (*fakeScheduleStore)(nil)

func mkCascadeSched(id string, tmplID string, overrides []string) model.Schedule {
	return model.Schedule{
		ID:         id,
		TargetID:   "t-" + id,
		Name:       id,
		RRule:      "FREQ=DAILY;BYHOUR=2",
		Timezone:   "UTC",
		DTStart:    time.Date(2026, 4, 18, 2, 0, 0, 0, time.UTC),
		TemplateID: &tmplID,
		Overrides:  overrides,
		Enabled:    true,
	}
}

func TestRecomputeNextRunForTemplate_Mixed(t *testing.T) {
	t.Parallel()
	ts := newFakeTemplateStore()
	_ = ts.Create(context.Background(), &model.Template{
		ID:       "T1",
		Name:     "nightly",
		RRule:    "FREQ=DAILY;BYHOUR=3",
		Timezone: "UTC",
	})

	ss := newFakeScheduleStore()
	// 10 schedules: 3 override rrule, 2 override timezone, 1 overrides both,
	// 4 override nothing → 4 affected.
	ss.schedules = []model.Schedule{
		mkCascadeSched("s1", "T1", []string{"rrule"}),
		mkCascadeSched("s2", "T1", []string{"rrule"}),
		mkCascadeSched("s3", "T1", []string{"rrule"}),
		mkCascadeSched("s4", "T1", []string{"timezone"}),
		mkCascadeSched("s5", "T1", []string{"timezone"}),
		mkCascadeSched("s6", "T1", []string{"rrule", "timezone"}),
		mkCascadeSched("s7", "T1", nil),
		mkCascadeSched("s8", "T1", []string{}),
		mkCascadeSched("s9", "T1", []string{"maintenance_window"}),
		mkCascadeSched("s10", "T1", []string{"tool_config"}),
	}

	now := time.Date(2026, 4, 19, 0, 0, 0, 0, time.UTC)
	exp := NewRRuleExpander(model.ScheduleDefaults{}, nil, frozenClock{now: now}, 1)

	affected, err := RecomputeNextRunForTemplate(context.Background(), "T1", ss, ts, exp)
	if err != nil {
		t.Fatalf("RecomputeNextRunForTemplate: %v", err)
	}
	if affected != 4 {
		t.Fatalf("expected 4 affected, got %d", affected)
	}
	// Confirm the unaffected schedules were not touched.
	for _, id := range []string{"s1", "s2", "s3", "s4", "s5", "s6"} {
		if _, ok := ss.nextRun[id]; ok {
			t.Fatalf("schedule %s should not have been updated", id)
		}
	}
	for _, id := range []string{"s7", "s8", "s9", "s10"} {
		if _, ok := ss.nextRun[id]; !ok {
			t.Fatalf("schedule %s should have been updated", id)
		}
	}
}

func TestRecomputeNextRunForTemplate_NoSchedules(t *testing.T) {
	t.Parallel()
	ts := newFakeTemplateStore()
	_ = ts.Create(context.Background(), &model.Template{
		ID:       "T-empty",
		RRule:    "FREQ=DAILY",
		Timezone: "UTC",
	})
	ss := newFakeScheduleStore()
	exp := NewRRuleExpander(model.ScheduleDefaults{}, nil, frozenClock{now: time.Now()}, 1)

	affected, err := RecomputeNextRunForTemplate(context.Background(), "T-empty", ss, ts, exp)
	if err != nil {
		t.Fatalf("RecomputeNextRunForTemplate: %v", err)
	}
	if affected != 0 {
		t.Fatalf("expected 0 affected, got %d", affected)
	}
}

func TestRecomputeNextRunForTemplate_TemplateNotFound(t *testing.T) {
	t.Parallel()
	ts := newFakeTemplateStore()
	ss := newFakeScheduleStore()
	exp := NewRRuleExpander(model.ScheduleDefaults{}, nil, frozenClock{now: time.Now()}, 1)

	_, err := RecomputeNextRunForTemplate(context.Background(), "nonexistent", ss, ts, exp)
	if err == nil {
		t.Fatalf("expected error for missing template")
	}
	if !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expected wrapped ErrNotFound, got %v", err)
	}
}

func TestRecomputeNextRunForTemplate_ExpanderError(t *testing.T) {
	t.Parallel()
	// Template with an invalid timezone — every non-overriding schedule
	// hits a LoadLocation failure inside the expander. The cascade must
	// continue-on-error, returning nil and affected=0.
	ts := newFakeTemplateStore()
	_ = ts.Create(context.Background(), &model.Template{
		ID:       "Tbad",
		RRule:    "FREQ=DAILY",
		Timezone: "Not/A/Zone",
	})
	ss := newFakeScheduleStore()
	ss.schedules = []model.Schedule{
		mkCascadeSched("s1", "Tbad", nil),
		mkCascadeSched("s2", "Tbad", nil),
	}

	now := time.Date(2026, 4, 19, 0, 0, 0, 0, time.UTC)
	exp := NewRRuleExpander(model.ScheduleDefaults{}, nil, frozenClock{now: now}, 1)

	affected, err := RecomputeNextRunForTemplate(context.Background(), "Tbad", ss, ts, exp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if affected != 0 {
		t.Fatalf("expected 0 affected (all expander errors), got %d", affected)
	}
	if len(ss.nextRun) != 0 {
		t.Fatalf("no schedule should have been updated, got %d", len(ss.nextRun))
	}
}

func TestRecomputeNextRunForTemplate_SetNextRunAtError(t *testing.T) {
	t.Parallel()
	ts := newFakeTemplateStore()
	_ = ts.Create(context.Background(), &model.Template{
		ID:       "T1",
		RRule:    "FREQ=DAILY",
		Timezone: "UTC",
	})
	ss := newFakeScheduleStore()
	ss.schedules = []model.Schedule{
		mkCascadeSched("a", "T1", nil),
		mkCascadeSched("b", "T1", nil),
	}
	ss.setErr["a"] = errors.New("forced")

	now := time.Date(2026, 4, 19, 0, 0, 0, 0, time.UTC)
	exp := NewRRuleExpander(model.ScheduleDefaults{}, nil, frozenClock{now: now}, 1)

	affected, err := RecomputeNextRunForTemplate(context.Background(), "T1", ss, ts, exp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if affected != 1 {
		t.Fatalf("expected 1 affected, got %d", affected)
	}
	if _, ok := ss.nextRun["b"]; !ok {
		t.Fatalf("expected b updated")
	}
}

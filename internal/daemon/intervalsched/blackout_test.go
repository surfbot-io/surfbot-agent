package intervalsched

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/surfbot-io/surfbot-agent/internal/model"
	"github.com/surfbot-io/surfbot-agent/internal/storage"
)

// fakeBlackoutStore is an in-memory implementation of
// storage.BlackoutStore sufficient for the evaluator tests. Only the
// methods the evaluator consumes are real; the rest return errors.
type fakeBlackoutStore struct {
	mu      sync.Mutex
	windows []model.BlackoutWindow
}

func newFakeStore() *fakeBlackoutStore { return &fakeBlackoutStore{} }

func (f *fakeBlackoutStore) set(ws []model.BlackoutWindow) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.windows = append([]model.BlackoutWindow(nil), ws...)
}

func (f *fakeBlackoutStore) Create(ctx context.Context, b *model.BlackoutWindow) error {
	return errors.New("not implemented")
}
func (f *fakeBlackoutStore) Get(ctx context.Context, id string) (*model.BlackoutWindow, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeBlackoutStore) List(ctx context.Context) ([]model.BlackoutWindow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]model.BlackoutWindow(nil), f.windows...), nil
}
func (f *fakeBlackoutStore) ListByScope(ctx context.Context, scope model.BlackoutScope) ([]model.BlackoutWindow, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeBlackoutStore) ListByTarget(ctx context.Context, targetID string) ([]model.BlackoutWindow, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeBlackoutStore) ListActive(ctx context.Context, targetID string) ([]model.BlackoutWindow, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeBlackoutStore) Update(ctx context.Context, b *model.BlackoutWindow) error {
	return errors.New("not implemented")
}
func (f *fakeBlackoutStore) Delete(ctx context.Context, id string) error {
	return errors.New("not implemented")
}

// ensure fakeBlackoutStore satisfies the interface at compile time.
var _ storage.BlackoutStore = (*fakeBlackoutStore)(nil)

func mustRefresh(t *testing.T, e *BlackoutEvaluator) {
	t.Helper()
	if err := e.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
}

// mkWindow builds a BlackoutWindow with sensible defaults. Caller
// supplies scope/target/rrule/duration/tz.
func mkWindow(id string, scope model.BlackoutScope, targetID *string, rrule string, dur time.Duration, tz string, dtstart time.Time) model.BlackoutWindow {
	return model.BlackoutWindow{
		ID:          id,
		Scope:       scope,
		TargetID:    targetID,
		Name:        "w-" + id,
		RRule:       rrule,
		DurationSec: int(dur / time.Second),
		Timezone:    tz,
		Enabled:     true,
		CreatedAt:   dtstart,
		UpdatedAt:   dtstart,
	}
}

func TestBlackoutEvaluator_NoWindows(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	e := NewBlackoutEvaluator(store)
	mustRefresh(t, e)

	if active, w := e.IsActive("t1", time.Now()); active || w != nil {
		t.Fatalf("expected inactive, got active=%v w=%v", active, w)
	}
}

func TestBlackoutEvaluator_GlobalActive(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	// DAILY BYHOUR=0 window with 24h duration → always active.
	dtstart := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	store.set([]model.BlackoutWindow{
		mkWindow("g1", model.BlackoutScopeGlobal, nil,
			fmt.Sprintf("DTSTART:%s\nRRULE:FREQ=DAILY;BYHOUR=0;BYMINUTE=0;BYSECOND=0", dtstart.Format("20060102T150405Z")),
			24*time.Hour, "UTC", dtstart),
	})
	e := NewBlackoutEvaluator(store)
	mustRefresh(t, e)

	now := time.Date(2026, 4, 19, 12, 30, 0, 0, time.UTC)
	for _, targetID := range []string{"t1", "t2", "whatever"} {
		if active, w := e.IsActive(targetID, now); !active || w == nil {
			t.Fatalf("target %s: expected active, got active=%v w=%v", targetID, active, w)
		}
	}
}

func TestBlackoutEvaluator_TargetScoped(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	dtstart := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	targetID := "T"
	store.set([]model.BlackoutWindow{
		mkWindow("t1", model.BlackoutScopeTarget, &targetID,
			fmt.Sprintf("DTSTART:%s\nRRULE:FREQ=DAILY;BYHOUR=0", dtstart.Format("20060102T150405Z")),
			24*time.Hour, "UTC", dtstart),
	})
	e := NewBlackoutEvaluator(store)
	mustRefresh(t, e)

	now := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)
	if active, _ := e.IsActive("T", now); !active {
		t.Fatalf("T should be active")
	}
	if active, _ := e.IsActive("U", now); active {
		t.Fatalf("U should not be active")
	}
}

func TestBlackoutEvaluator_BothActive(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	dtstart := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	targetID := "T"
	store.set([]model.BlackoutWindow{
		mkWindow("g", model.BlackoutScopeGlobal, nil,
			fmt.Sprintf("DTSTART:%s\nRRULE:FREQ=DAILY;BYHOUR=0", dtstart.Format("20060102T150405Z")),
			24*time.Hour, "UTC", dtstart),
		mkWindow("tt", model.BlackoutScopeTarget, &targetID,
			fmt.Sprintf("DTSTART:%s\nRRULE:FREQ=DAILY;BYHOUR=0", dtstart.Format("20060102T150405Z")),
			24*time.Hour, "UTC", dtstart),
	})
	e := NewBlackoutEvaluator(store)
	mustRefresh(t, e)

	now := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)
	active, w := e.IsActive("T", now)
	if !active || w == nil {
		t.Fatalf("expected active")
	}
	if w.ID != "g" {
		t.Fatalf("expected global to win tiebreak; got %s", w.ID)
	}
}

func TestBlackoutEvaluator_Boundary(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	// DTSTART at 12:00, duration 1h.
	dtstart := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)
	store.set([]model.BlackoutWindow{
		mkWindow("g", model.BlackoutScopeGlobal, nil,
			fmt.Sprintf("DTSTART:%s\nRRULE:FREQ=DAILY;BYHOUR=12;BYMINUTE=0;BYSECOND=0", dtstart.Format("20060102T150405Z")),
			time.Hour, "UTC", dtstart),
	})
	e := NewBlackoutEvaluator(store)
	mustRefresh(t, e)

	at12 := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)
	at1255 := time.Date(2026, 4, 19, 12, 55, 0, 0, time.UTC)
	at13 := time.Date(2026, 4, 19, 13, 0, 0, 0, time.UTC)

	if active, _ := e.IsActive("t", at12); !active {
		t.Fatalf("12:00 should be active (inclusive start)")
	}
	if active, _ := e.IsActive("t", at1255); !active {
		t.Fatalf("12:55 should be active")
	}
	if active, _ := e.IsActive("t", at13); active {
		t.Fatalf("13:00 should NOT be active (exclusive end)")
	}
}

func TestBlackoutEvaluator_WeeklyRRULE(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	// Saturday 00:00 UTC, 48h = covers Sat + Sun.
	dtstart := time.Date(2026, 4, 4, 0, 0, 0, 0, time.UTC) // saturday
	store.set([]model.BlackoutWindow{
		mkWindow("g", model.BlackoutScopeGlobal, nil,
			fmt.Sprintf("DTSTART:%s\nRRULE:FREQ=WEEKLY;BYDAY=SA", dtstart.Format("20060102T150405Z")),
			48*time.Hour, "UTC", dtstart),
	})
	e := NewBlackoutEvaluator(store)
	mustRefresh(t, e)

	// Saturday mid-day → active.
	sat := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	sun := time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC)
	mon := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
	fri := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	for _, tc := range []struct {
		name string
		at   time.Time
		want bool
	}{
		{"sat", sat, true},
		{"sun", sun, true},
		{"mon", mon, false},
		{"fri", fri, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			active, _ := e.IsActive("t", tc.at)
			if active != tc.want {
				t.Fatalf("%s: want active=%v got %v", tc.name, tc.want, active)
			}
		})
	}
}

func TestBlackoutEvaluator_RefreshInvalidates(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	e := NewBlackoutEvaluator(store)
	mustRefresh(t, e)

	dtstart := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)
	if active, _ := e.IsActive("t", now); active {
		t.Fatalf("unexpected active on empty store")
	}

	// Populate after refresh — should not yet be visible.
	store.set([]model.BlackoutWindow{
		mkWindow("g", model.BlackoutScopeGlobal, nil,
			fmt.Sprintf("DTSTART:%s\nRRULE:FREQ=DAILY;BYHOUR=0", dtstart.Format("20060102T150405Z")),
			24*time.Hour, "UTC", dtstart),
	})

	// Immediately after populating, the cache is still fresh (populatedOK=true, <30s).
	if active, _ := e.IsActive("t", now); active {
		t.Fatalf("store change should not be visible without Refresh")
	}

	mustRefresh(t, e)
	if active, _ := e.IsActive("t", now); !active {
		t.Fatalf("after Refresh, new window should be visible")
	}
}

func TestBlackoutEvaluator_NextWindowEnd(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	dtstart := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)
	store.set([]model.BlackoutWindow{
		mkWindow("g", model.BlackoutScopeGlobal, nil,
			fmt.Sprintf("DTSTART:%s\nRRULE:FREQ=DAILY;BYHOUR=12;BYMINUTE=0;BYSECOND=0", dtstart.Format("20060102T150405Z")),
			time.Hour, "UTC", dtstart),
	})
	e := NewBlackoutEvaluator(store)
	mustRefresh(t, e)

	at := time.Date(2026, 4, 19, 12, 30, 0, 0, time.UTC)
	end := e.NextWindowEnd("t", at)
	if end == nil {
		t.Fatalf("expected end time")
	}
	want := time.Date(2026, 4, 19, 13, 0, 0, 0, time.UTC)
	if !end.Equal(want) {
		t.Fatalf("end = %s, want %s", end, want)
	}

	inactive := time.Date(2026, 4, 19, 14, 0, 0, 0, time.UTC)
	if e.NextWindowEnd("t", inactive) != nil {
		t.Fatalf("expected nil when inactive")
	}
}

func TestBlackoutEvaluator_DisabledSkipped(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	dtstart := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	w := mkWindow("g", model.BlackoutScopeGlobal, nil,
		fmt.Sprintf("DTSTART:%s\nRRULE:FREQ=DAILY;BYHOUR=0", dtstart.Format("20060102T150405Z")),
		24*time.Hour, "UTC", dtstart)
	w.Enabled = false
	store.set([]model.BlackoutWindow{w})
	e := NewBlackoutEvaluator(store)
	mustRefresh(t, e)

	now := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)
	if active, _ := e.IsActive("t", now); active {
		t.Fatalf("disabled window should not activate")
	}
}

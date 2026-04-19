package v1

import (
	"net/http"
	"testing"
	"time"
)

func TestUpcomingEmpty(t *testing.T) {
	store := newTestStore(t)
	srv := newTestAPI(t, defaultAPIDeps(store))

	resp, raw := doJSON(t, srv, http.MethodGet, "/api/v1/schedules/upcoming", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, raw)
	}
	var u UpcomingResponse
	decode(t, raw, &u)
	if u.Items == nil {
		t.Fatalf("items must be non-nil slice, got nil")
	}
	if len(u.Items) != 0 {
		t.Fatalf("expected 0 items on empty, got %d", len(u.Items))
	}
	if u.BlackoutsInHorizon == nil {
		t.Fatalf("blackouts must be non-nil slice, got nil")
	}
}

func TestUpcomingWithSchedule(t *testing.T) {
	store := newTestStore(t)
	srv := newTestAPI(t, defaultAPIDeps(store))
	targetID := seedTarget(t, store, "example.com")

	// A minutely schedule starting just before now — should produce
	// many firings inside the default 24h horizon.
	dtstart := time.Now().UTC().Add(-2 * time.Minute).Truncate(time.Second)
	_, raw := doJSON(t, srv, http.MethodPost, "/api/v1/schedules", CreateScheduleRequest{
		TargetID: targetID, Name: "every", Timezone: "UTC",
		RRule:   "FREQ=HOURLY",
		DTStart: dtstart,
	})
	var s ScheduleResponse
	decode(t, raw, &s)

	resp, raw := doJSON(t, srv, http.MethodGet, "/api/v1/schedules/upcoming?horizon=3h&limit=10", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, raw)
	}
	var u UpcomingResponse
	decode(t, raw, &u)
	if len(u.Items) == 0 {
		t.Fatalf("expected at least one firing in 3h horizon, got none (body=%s)", raw)
	}
	if u.Items[0].ScheduleID != s.ID {
		t.Fatalf("wrong schedule id: %+v", u.Items[0])
	}
}

func TestUpcomingSkipsPaused(t *testing.T) {
	store := newTestStore(t)
	srv := newTestAPI(t, defaultAPIDeps(store))
	targetID := seedTarget(t, store, "example.com")

	dtstart := time.Now().UTC().Add(-2 * time.Minute).Truncate(time.Second)
	_, raw := doJSON(t, srv, http.MethodPost, "/api/v1/schedules", CreateScheduleRequest{
		TargetID: targetID, Name: "x", Timezone: "UTC",
		RRule: "FREQ=HOURLY", DTStart: dtstart,
	})
	var s ScheduleResponse
	decode(t, raw, &s)

	doJSON(t, srv, http.MethodPost, "/api/v1/schedules/"+s.ID+"/pause", nil)

	_, raw = doJSON(t, srv, http.MethodGet, "/api/v1/schedules/upcoming?horizon=3h", nil)
	var u UpcomingResponse
	decode(t, raw, &u)
	if len(u.Items) != 0 {
		t.Fatalf("paused schedule should not appear; got %+v", u.Items)
	}
}

func TestUpcomingRejectsInvalidHorizon(t *testing.T) {
	store := newTestStore(t)
	srv := newTestAPI(t, defaultAPIDeps(store))

	resp, _ := doJSON(t, srv, http.MethodGet, "/api/v1/schedules/upcoming?horizon=-5m", nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("negative horizon should 400, got %d", resp.StatusCode)
	}
}

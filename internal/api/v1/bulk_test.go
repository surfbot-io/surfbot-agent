package v1

import (
	"net/http"
	"testing"
	"time"
)

func TestBulkPauseResume(t *testing.T) {
	store := newTestStore(t)
	srv := newTestAPI(t, defaultAPIDeps(store))
	targetID := seedTarget(t, store, "example.com")

	mk := func(name string, hour int) string {
		_, raw := doJSON(t, srv, http.MethodPost, "/api/v1/schedules", CreateScheduleRequest{
			TargetID: targetID, Name: name, Timezone: "UTC",
			RRule:   "FREQ=WEEKLY;BYHOUR=" + itoa(hour) + ";BYMINUTE=0;BYSECOND=0",
			DTStart: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		})
		var s ScheduleResponse
		decode(t, raw, &s)
		return s.ID
	}
	a := mk("a", 1)
	b := mk("b", 12)

	resp, raw := doJSON(t, srv, http.MethodPost, "/api/v1/schedules/bulk", BulkScheduleRequest{
		Operation:   BulkPause,
		ScheduleIDs: []string{a, b},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("bulk pause status=%d body=%s", resp.StatusCode, raw)
	}
	var out BulkScheduleResponse
	decode(t, raw, &out)
	if len(out.Succeeded) != 2 || len(out.Failed) != 0 {
		t.Fatalf("bulk pause outcome: %+v", out)
	}

	// Verify both paused.
	_, raw = doJSON(t, srv, http.MethodGet, "/api/v1/schedules/"+a, nil)
	var got ScheduleResponse
	decode(t, raw, &got)
	if got.Status != "paused" {
		t.Fatalf("a status=%q after bulk pause", got.Status)
	}

	// Resume one, leave the other paused.
	resp, raw = doJSON(t, srv, http.MethodPost, "/api/v1/schedules/bulk", BulkScheduleRequest{
		Operation:   BulkResume,
		ScheduleIDs: []string{a},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("bulk resume status=%d body=%s", resp.StatusCode, raw)
	}
}

func TestBulkDeleteMixedExistingAndMissing(t *testing.T) {
	store := newTestStore(t)
	srv := newTestAPI(t, defaultAPIDeps(store))
	targetID := seedTarget(t, store, "example.com")

	_, raw := doJSON(t, srv, http.MethodPost, "/api/v1/schedules", CreateScheduleRequest{
		TargetID: targetID, Name: "a", Timezone: "UTC",
		RRule: "FREQ=WEEKLY", DTStart: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	var s ScheduleResponse
	decode(t, raw, &s)

	resp, raw := doJSON(t, srv, http.MethodPost, "/api/v1/schedules/bulk", BulkScheduleRequest{
		Operation:   BulkDelete,
		ScheduleIDs: []string{s.ID, "does-not-exist"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("mixed-existing bulk should 200 with per-item failures, got %d body=%s", resp.StatusCode, raw)
	}
	var out BulkScheduleResponse
	decode(t, raw, &out)
	if len(out.Succeeded) != 1 || len(out.Failed) != 1 {
		t.Fatalf("outcome: %+v", out)
	}
	if out.Succeeded[0] != s.ID {
		t.Fatalf("wrong succeeded id: %+v", out)
	}
}

func TestBulkRejectsUnknownOperation(t *testing.T) {
	store := newTestStore(t)
	srv := newTestAPI(t, defaultAPIDeps(store))

	resp, _ := doJSON(t, srv, http.MethodPost, "/api/v1/schedules/bulk", BulkScheduleRequest{
		Operation:   "lolwat",
		ScheduleIDs: []string{"abc"},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown op should 400, got %d", resp.StatusCode)
	}
}

func TestBulkEmptyIDs(t *testing.T) {
	store := newTestStore(t)
	srv := newTestAPI(t, defaultAPIDeps(store))

	resp, _ := doJSON(t, srv, http.MethodPost, "/api/v1/schedules/bulk", BulkScheduleRequest{
		Operation: BulkPause,
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty ids should 400, got %d", resp.StatusCode)
	}
}

package v1

import (
	"net/http"
	"strconv"
	"testing"
	"time"
)

func itoa(n int) string { return strconv.Itoa(n) }

func TestSchedulesCreateAndList(t *testing.T) {
	store := newTestStore(t)
	srv := newTestAPI(t, defaultAPIDeps(store))
	targetID := seedTarget(t, store, "example.com")

	// Happy-path create.
	body := CreateScheduleRequest{
		TargetID: targetID,
		Name:     "nightly",
		RRule:    "FREQ=DAILY;BYHOUR=2",
		DTStart:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Timezone: "UTC",
	}
	resp, rawBody := doJSON(t, srv, http.MethodPost, "/api/v1/schedules", body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /schedules status=%d body=%s", resp.StatusCode, rawBody)
	}
	var created ScheduleResponse
	decode(t, rawBody, &created)
	if created.ID == "" || created.TargetID != targetID {
		t.Fatalf("created response missing fields: %+v", created)
	}
	if created.Status != "active" {
		t.Fatalf("new schedule status=%q, want active", created.Status)
	}

	// List.
	resp, rawBody = doJSON(t, srv, http.MethodGet, "/api/v1/schedules", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /schedules status=%d body=%s", resp.StatusCode, rawBody)
	}
	var page PaginatedResponse[ScheduleResponse]
	decode(t, rawBody, &page)
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != created.ID {
		t.Fatalf("unexpected list page: %+v", page)
	}
}

func TestSchedulesValidationMissingFields(t *testing.T) {
	store := newTestStore(t)
	srv := newTestAPI(t, defaultAPIDeps(store))

	resp, body := doJSON(t, srv, http.MethodPost, "/api/v1/schedules", CreateScheduleRequest{})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing fields should 400, got %d body=%s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != ProblemContentType {
		t.Fatalf("content-type=%q, want %q", ct, ProblemContentType)
	}
	var p ProblemResponse
	decode(t, body, &p)
	if len(p.FieldErrors) == 0 {
		t.Fatalf("expected field errors, got none: %+v", p)
	}
}

func TestSchedulesInvalidRRule(t *testing.T) {
	store := newTestStore(t)
	srv := newTestAPI(t, defaultAPIDeps(store))
	targetID := seedTarget(t, store, "example.com")

	req := CreateScheduleRequest{
		TargetID: targetID,
		Name:     "bad",
		RRule:    "FREQ=SECONDLY",
		DTStart:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Timezone: "UTC",
	}
	resp, body := doJSON(t, srv, http.MethodPost, "/api/v1/schedules", req)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("SECONDLY should 422, got %d body=%s", resp.StatusCode, body)
	}
	var p ProblemResponse
	decode(t, body, &p)
	if len(p.FieldErrors) == 0 || p.FieldErrors[0].Field != "rrule" {
		t.Fatalf("expected rrule field error, got %+v", p.FieldErrors)
	}
}

func TestSchedulesPauseResumeIdempotent(t *testing.T) {
	store := newTestStore(t)
	srv := newTestAPI(t, defaultAPIDeps(store))
	targetID := seedTarget(t, store, "example.com")

	resp, body := doJSON(t, srv, http.MethodPost, "/api/v1/schedules", CreateScheduleRequest{
		TargetID: targetID, Name: "n", RRule: "FREQ=DAILY", Timezone: "UTC",
		DTStart: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", resp.StatusCode, body)
	}
	var created ScheduleResponse
	decode(t, body, &created)

	// Pause.
	resp, body = doJSON(t, srv, http.MethodPost, "/api/v1/schedules/"+created.ID+"/pause", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pause status=%d body=%s", resp.StatusCode, body)
	}
	var paused ScheduleResponse
	decode(t, body, &paused)
	if paused.Status != "paused" {
		t.Fatalf("after pause status=%q", paused.Status)
	}
	// Pause again — idempotent.
	resp, body = doJSON(t, srv, http.MethodPost, "/api/v1/schedules/"+created.ID+"/pause", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pause twice status=%d body=%s", resp.StatusCode, body)
	}
	// Resume.
	resp, body = doJSON(t, srv, http.MethodPost, "/api/v1/schedules/"+created.ID+"/resume", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("resume status=%d body=%s", resp.StatusCode, body)
	}
	var resumed ScheduleResponse
	decode(t, body, &resumed)
	if resumed.Status != "active" {
		t.Fatalf("after resume status=%q", resumed.Status)
	}
}

func TestSchedulesUpdateAndDelete(t *testing.T) {
	store := newTestStore(t)
	srv := newTestAPI(t, defaultAPIDeps(store))
	targetID := seedTarget(t, store, "example.com")

	_, body := doJSON(t, srv, http.MethodPost, "/api/v1/schedules", CreateScheduleRequest{
		TargetID: targetID, Name: "n", RRule: "FREQ=DAILY", Timezone: "UTC",
		DTStart: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	var created ScheduleResponse
	decode(t, body, &created)

	newName := "updated"
	resp, body := doJSON(t, srv, http.MethodPut, "/api/v1/schedules/"+created.ID, UpdateScheduleRequest{
		Name: &newName,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT status=%d body=%s", resp.StatusCode, body)
	}
	var updated ScheduleResponse
	decode(t, body, &updated)
	if updated.Name != "updated" {
		t.Fatalf("name after update=%q", updated.Name)
	}

	// DELETE returns 204; subsequent GET 404.
	resp, body = doJSON(t, srv, http.MethodDelete, "/api/v1/schedules/"+created.ID, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE status=%d body=%s", resp.StatusCode, body)
	}
	resp, body = doJSON(t, srv, http.MethodGet, "/api/v1/schedules/"+created.ID, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("after delete GET status=%d body=%s", resp.StatusCode, body)
	}
}

func TestSchedulesListFilters(t *testing.T) {
	store := newTestStore(t)
	srv := newTestAPI(t, defaultAPIDeps(store))
	targetID := seedTarget(t, store, "example.com")
	other := seedTarget(t, store, "other.com")

	// Use distinct BYHOUR values so schedules on the same target don't
	// overlap within the 1h default estimated duration.
	mk := func(targetID, name string, hour int) string {
		_, body := doJSON(t, srv, http.MethodPost, "/api/v1/schedules", CreateScheduleRequest{
			TargetID: targetID, Name: name,
			RRule:    "FREQ=WEEKLY;BYHOUR=" + itoa(hour) + ";BYMINUTE=0;BYSECOND=0",
			Timezone: "UTC",
			DTStart:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		})
		var r ScheduleResponse
		decode(t, body, &r)
		return r.ID
	}
	mk(targetID, "a", 1)
	mk(targetID, "b", 12)
	mk(other, "c", 2)

	_, body := doJSON(t, srv, http.MethodGet, "/api/v1/schedules?target_id="+targetID, nil)
	var page PaginatedResponse[ScheduleResponse]
	decode(t, body, &page)
	if page.Total != 2 {
		t.Fatalf("target filter total=%d, want 2", page.Total)
	}

	// Pause one, then list with status=paused.
	doJSON(t, srv, http.MethodPost, "/api/v1/schedules/"+page.Items[0].ID+"/pause", nil)
	_, body = doJSON(t, srv, http.MethodGet, "/api/v1/schedules?status=paused", nil)
	var p2 PaginatedResponse[ScheduleResponse]
	decode(t, body, &p2)
	if p2.Total != 1 {
		t.Fatalf("status=paused total=%d, want 1", p2.Total)
	}
}

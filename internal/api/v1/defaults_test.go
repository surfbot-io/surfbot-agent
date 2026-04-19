package v1

import (
	"net/http"
	"testing"
)

func TestScheduleDefaultsGetSeededRow(t *testing.T) {
	store := newTestStore(t)
	srv := newTestAPI(t, defaultAPIDeps(store))

	resp, raw := doJSON(t, srv, http.MethodGet, "/api/v1/schedule-defaults", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, raw)
	}
	var d ScheduleDefaultsResponse
	decode(t, raw, &d)
	if d.DefaultRRule == "" || d.DefaultTimezone == "" {
		t.Fatalf("defaults are empty: %+v", d)
	}
	if d.MaxConcurrentScans < 1 {
		t.Fatalf("bad max_concurrent_scans: %+v", d)
	}
}

func TestScheduleDefaultsPut(t *testing.T) {
	store := newTestStore(t)
	srv := newTestAPI(t, defaultAPIDeps(store))

	body := UpdateScheduleDefaultsRequest{
		DefaultRRule:       "FREQ=WEEKLY;BYDAY=SU",
		DefaultTimezone:    "UTC",
		MaxConcurrentScans: 6,
		JitterSeconds:      30,
	}
	resp, raw := doJSON(t, srv, http.MethodPut, "/api/v1/schedule-defaults", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT status=%d body=%s", resp.StatusCode, raw)
	}

	_, raw = doJSON(t, srv, http.MethodGet, "/api/v1/schedule-defaults", nil)
	var d ScheduleDefaultsResponse
	decode(t, raw, &d)
	if d.DefaultRRule != body.DefaultRRule || d.MaxConcurrentScans != 6 || d.JitterSeconds != 30 {
		t.Fatalf("defaults not persisted: %+v", d)
	}
}

func TestScheduleDefaultsRejectsBadBounds(t *testing.T) {
	store := newTestStore(t)
	srv := newTestAPI(t, defaultAPIDeps(store))

	body := UpdateScheduleDefaultsRequest{
		DefaultRRule:       "FREQ=DAILY",
		DefaultTimezone:    "UTC",
		MaxConcurrentScans: 0,
	}
	resp, raw := doJSON(t, srv, http.MethodPut, "/api/v1/schedule-defaults", body)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("bad bounds should 422, got %d body=%s", resp.StatusCode, raw)
	}
}

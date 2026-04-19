package v1

import (
	"net/http"
	"testing"
	"time"

	"github.com/surfbot-io/surfbot-agent/internal/model"
)

func TestBlackoutsCreateGlobal(t *testing.T) {
	store := newTestStore(t)
	srv := newTestAPI(t, defaultAPIDeps(store))

	body := CreateBlackoutRequest{
		Scope:           model.BlackoutScopeGlobal,
		Name:            "weekends",
		RRule:           "FREQ=WEEKLY;BYDAY=SA,SU",
		DurationSeconds: 24 * 3600,
		Timezone:        "UTC",
	}
	resp, raw := doJSON(t, srv, http.MethodPost, "/api/v1/blackouts", body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status=%d body=%s", resp.StatusCode, raw)
	}
	var b BlackoutResponse
	decode(t, raw, &b)
	if b.ID == "" || b.Scope != model.BlackoutScopeGlobal {
		t.Fatalf("unexpected: %+v", b)
	}
}

func TestBlackoutsRejectsOversizedDuration(t *testing.T) {
	store := newTestStore(t)
	srv := newTestAPI(t, defaultAPIDeps(store))

	body := CreateBlackoutRequest{
		Scope:           model.BlackoutScopeGlobal,
		Name:            "too-long",
		RRule:           "FREQ=YEARLY",
		DurationSeconds: 8 * 24 * 3600,
	}
	resp, raw := doJSON(t, srv, http.MethodPost, "/api/v1/blackouts", body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", resp.StatusCode, raw)
	}
}

func TestBlackoutsActiveAtFilter(t *testing.T) {
	store := newTestStore(t)
	srv := newTestAPI(t, defaultAPIDeps(store))

	// Pad dtstart by 1s before `now` so the rrule's second-precision
	// truncation doesn't defeat activity at the chosen instant.
	now := time.Now().UTC().Truncate(time.Second)
	past := now.Add(-10 * time.Second)

	body := CreateBlackoutRequest{
		Scope:           model.BlackoutScopeGlobal,
		Name:            "always-on",
		RRule:           "FREQ=DAILY;DTSTART=" + past.Format("20060102T150405Z"),
		DurationSeconds: 48 * 3600,
		Timezone:        "UTC",
	}
	resp, raw := doJSON(t, srv, http.MethodPost, "/api/v1/blackouts", body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", resp.StatusCode, raw)
	}

	// Active at now — should appear.
	url := "/api/v1/blackouts?active_at=" + now.Format(time.RFC3339)
	resp, raw = doJSON(t, srv, http.MethodGet, url, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list status=%d body=%s", resp.StatusCode, raw)
	}
	var page PaginatedResponse[BlackoutResponse]
	decode(t, raw, &page)
	if page.Total != 1 {
		t.Fatalf("active_at filter expected 1 match, got %d (body=%s)", page.Total, raw)
	}
}

func TestBlackoutsTargetScopeRequiresTargetID(t *testing.T) {
	store := newTestStore(t)
	srv := newTestAPI(t, defaultAPIDeps(store))

	body := CreateBlackoutRequest{
		Scope:           model.BlackoutScopeTarget,
		Name:            "t",
		RRule:           "FREQ=DAILY",
		DurationSeconds: 3600,
	}
	resp, raw := doJSON(t, srv, http.MethodPost, "/api/v1/blackouts", body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", resp.StatusCode, raw)
	}
}

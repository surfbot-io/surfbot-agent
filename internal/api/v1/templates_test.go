package v1

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/surfbot-io/surfbot-agent/internal/model"
)

func TestTemplatesCreateAndGet(t *testing.T) {
	store := newTestStore(t)
	srv := newTestAPI(t, defaultAPIDeps(store))

	body := CreateTemplateRequest{
		Name:     "nightly",
		RRule:    "FREQ=DAILY",
		Timezone: "UTC",
	}
	resp, raw := doJSON(t, srv, http.MethodPost, "/api/v1/templates", body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST status=%d body=%s", resp.StatusCode, raw)
	}
	var created TemplateResponse
	decode(t, raw, &created)
	if created.ID == "" {
		t.Fatalf("missing id")
	}

	resp, raw = doJSON(t, srv, http.MethodGet, "/api/v1/templates/"+created.ID, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET status=%d body=%s", resp.StatusCode, raw)
	}
}

func TestTemplatesRejectUnknownToolKey(t *testing.T) {
	store := newTestStore(t)
	srv := newTestAPI(t, defaultAPIDeps(store))

	body := CreateTemplateRequest{
		Name:  "bad",
		RRule: "FREQ=DAILY",
		ToolConfig: model.ToolConfig{
			"amass": json.RawMessage(`{}`),
		},
	}
	resp, raw := doJSON(t, srv, http.MethodPost, "/api/v1/templates", body)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("unknown tool should 422, got %d body=%s", resp.StatusCode, raw)
	}
	var p ProblemResponse
	decode(t, raw, &p)
	if len(p.FieldErrors) == 0 || p.FieldErrors[0].Field != "tool_config.amass" {
		t.Fatalf("expected field error on tool_config.amass, got %+v", p.FieldErrors)
	}
}

func TestTemplatesRejectMalformedToolPayload(t *testing.T) {
	store := newTestStore(t)
	srv := newTestAPI(t, defaultAPIDeps(store))

	body := CreateTemplateRequest{
		Name:  "bad2",
		RRule: "FREQ=DAILY",
		ToolConfig: model.ToolConfig{
			"nuclei": json.RawMessage(`{"rate_limit": "not-an-int"}`),
		},
	}
	resp, raw := doJSON(t, srv, http.MethodPost, "/api/v1/templates", body)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("malformed should 422, got %d body=%s", resp.StatusCode, raw)
	}
}

func TestTemplatesDeleteRefusesWithDependents(t *testing.T) {
	store := newTestStore(t)
	srv := newTestAPI(t, defaultAPIDeps(store))
	targetID := seedTarget(t, store, "example.com")

	// Create template then a schedule that references it.
	_, raw := doJSON(t, srv, http.MethodPost, "/api/v1/templates", CreateTemplateRequest{
		Name: "tmpl", RRule: "FREQ=DAILY", Timezone: "UTC",
	})
	var tmpl TemplateResponse
	decode(t, raw, &tmpl)

	_, raw = doJSON(t, srv, http.MethodPost, "/api/v1/schedules", CreateScheduleRequest{
		TargetID: targetID, Name: "sched", RRule: "FREQ=DAILY", Timezone: "UTC",
		TemplateID: &tmpl.ID,
	})
	var sched ScheduleResponse
	decode(t, raw, &sched)

	resp, raw := doJSON(t, srv, http.MethodDelete, "/api/v1/templates/"+tmpl.ID, nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("delete with dependents should 409, got %d body=%s", resp.StatusCode, raw)
	}
	var p ProblemResponse
	decode(t, raw, &p)
	if len(p.FieldErrors) == 0 {
		t.Fatalf("expected dependent ids in field errors")
	}

	// Force delete cascades.
	resp, raw = doJSON(t, srv, http.MethodDelete, "/api/v1/templates/"+tmpl.ID+"?force=true", nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("force delete status=%d body=%s", resp.StatusCode, raw)
	}
	// Dependent schedule is gone.
	resp, _ = doJSON(t, srv, http.MethodGet, "/api/v1/schedules/"+sched.ID, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("dependent schedule still present after cascade delete: status=%d", resp.StatusCode)
	}
}

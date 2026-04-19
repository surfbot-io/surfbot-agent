package v1

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestProblemResponseShape(t *testing.T) {
	rec := httptest.NewRecorder()
	writeProblem(rec, http.StatusUnprocessableEntity, "/problems/validation",
		"Validation Failed", "rrule is invalid", []FieldError{
			{Field: "rrule", Message: "missing FREQ"},
		})

	if got := rec.Header().Get("Content-Type"); got != ProblemContentType {
		t.Fatalf("content-type = %q, want %q", got, ProblemContentType)
	}
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}
	var p ProblemResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if p.Type == "" || p.Title == "" || p.Status == 0 {
		t.Fatalf("required RFC 7807 fields missing: %+v", p)
	}
	if len(p.FieldErrors) != 1 || p.FieldErrors[0].Field != "rrule" {
		t.Fatalf("field errors not serialized: %+v", p.FieldErrors)
	}
}

func TestParsePaginationDefaults(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	p := ParsePagination(req, 0)
	if p.Limit != DefaultLimit || p.Offset != 0 {
		t.Fatalf("defaults = %+v, want limit=%d offset=0", p, DefaultLimit)
	}
}

func TestParsePaginationClamps(t *testing.T) {
	cases := []struct {
		q          string
		maxLimit   int
		wantLimit  int
		wantOffset int
	}{
		{"limit=10&offset=5", 0, 10, 5},
		{"limit=9999", 500, 500, 0},
		{"limit=-1", 0, DefaultLimit, 0},
		{"limit=abc&offset=xyz", 0, DefaultLimit, 0},
		{"offset=-5", 0, DefaultLimit, 0},
	}
	for _, tc := range cases {
		u, _ := url.Parse("/x?" + tc.q)
		req := httptest.NewRequest(http.MethodGet, u.String(), nil)
		p := ParsePagination(req, tc.maxLimit)
		if p.Limit != tc.wantLimit || p.Offset != tc.wantOffset {
			t.Fatalf("?%s -> %+v, want limit=%d offset=%d", tc.q, p, tc.wantLimit, tc.wantOffset)
		}
	}
}

func TestMethodNotAllowedSetsHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	methodNotAllowed(rec, "GET")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("Allow"); got != "GET" {
		t.Fatalf("Allow = %q", got)
	}
	if !strings.Contains(rec.Body.String(), "method-not-allowed") {
		t.Fatalf("body missing problem type: %s", rec.Body.String())
	}
}

func TestRegisterRoutesNoop(t *testing.T) {
	// R1 ships the scaffold; later commits add handlers.
	mux := http.NewServeMux()
	RegisterRoutes(mux, APIDeps{})
	// A path not yet registered should 404 via the default mux behavior.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/schedules", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unregistered path should 404, got %d", rec.Code)
	}
}

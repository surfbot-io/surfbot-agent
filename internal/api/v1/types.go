// Package v1 implements the versioned REST API for first-class schedules
// introduced by SPEC-SCHED1. Handlers live in this package and are
// registered via RegisterRoutes against an *http.ServeMux. All error
// responses use the RFC 7807 problem+json shape defined below.
package v1

import (
	"encoding/json"
	"net/http"
	"strconv"
)

// ProblemContentType is the RFC 7807 media type used for error responses.
const ProblemContentType = "application/problem+json"

// ProblemResponse is the RFC 7807 problem-details shape every error
// response uses. Type is a URI-reference that identifies the problem
// class; handlers use /problems/<slug> relative URIs. FieldErrors is a
// non-standard extension — callers that want structured per-field
// validation errors read it off the body.
type ProblemResponse struct {
	Type        string       `json:"type"`
	Title       string       `json:"title"`
	Status      int          `json:"status"`
	Detail      string       `json:"detail,omitempty"`
	FieldErrors []FieldError `json:"field_errors,omitempty"`
}

// FieldError names a single invalid field in a request body. Field uses
// dotted JSON paths (e.g. tool_config.nuclei.severity).
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// Pagination carries parsed ?limit=&offset= query parameters.
type Pagination struct {
	Limit  int
	Offset int
}

// Default pagination bounds. Handlers pick their own max via
// ParsePagination's maxLimit parameter.
const (
	DefaultLimit = 50
	MaxLimit     = 500
)

// PaginatedResponse is the shape list endpoints return. Items is always
// an allocated slice (never nil) so clients don't have to special-case
// empty pages.
type PaginatedResponse[T any] struct {
	Items  []T   `json:"items"`
	Total  int64 `json:"total"`
	Limit  int   `json:"limit"`
	Offset int   `json:"offset"`
}

// ParsePagination reads ?limit and ?offset from the request with sane
// defaults. An invalid limit falls back to DefaultLimit; an invalid
// offset falls back to 0. limit is clamped to [1, maxLimit]; pass 0 to
// use MaxLimit.
func ParsePagination(r *http.Request, maxLimit int) Pagination {
	if maxLimit <= 0 {
		maxLimit = MaxLimit
	}
	p := Pagination{Limit: DefaultLimit, Offset: 0}
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			if n > maxLimit {
				n = maxLimit
			}
			p.Limit = n
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			p.Offset = n
		}
	}
	return p
}

// writeProblem emits a problem+json error response. status and title are
// required; detail and fieldErrors are optional. problemType is the
// relative URI identifying the problem class.
func writeProblem(w http.ResponseWriter, status int, problemType, title, detail string, fieldErrors []FieldError) {
	p := ProblemResponse{
		Type:        problemType,
		Title:       title,
		Status:      status,
		Detail:      detail,
		FieldErrors: fieldErrors,
	}
	w.Header().Set("Content-Type", ProblemContentType)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(p)
}

// writeJSON emits a standard JSON response with the given status and
// body. Kept local to this package so handlers have a single entry
// point independent of the webui helper.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if body != nil {
		_ = json.NewEncoder(w).Encode(body)
	}
}

// decodeJSON reads and decodes the request body into v with a 1 MiB
// size cap. Returns a non-nil error on unreadable body or malformed
// JSON — callers translate to a 400 ProblemResponse.
func decodeJSON(r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// methodNotAllowed writes a 405 problem response with an Allow header.
func methodNotAllowed(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	writeProblem(w, http.StatusMethodNotAllowed, "/problems/method-not-allowed",
		"Method Not Allowed", "method "+allow+" required", nil)
}

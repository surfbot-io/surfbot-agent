package v1

import (
	"errors"
	"net/http"
	"strings"
	"time"

	extrrule "github.com/teambition/rrule-go"

	"github.com/surfbot-io/surfbot-agent/internal/model"
	"github.com/surfbot-io/surfbot-agent/internal/rrule"
	"github.com/surfbot-io/surfbot-agent/internal/storage"
)

// maxBlackoutDurationSeconds caps blackout durations at 7 days so a
// typo in a multi-day window doesn't permanently sideline scans. The
// documented intent is per-window durations — if a stretch longer than
// 7d is needed, encode it as recurring windows with a matching RRULE.
const maxBlackoutDurationSeconds = 7 * 24 * 60 * 60

// BlackoutResponse is the public shape of a blackout window.
type BlackoutResponse struct {
	ID              string              `json:"id"`
	Scope           model.BlackoutScope `json:"scope"`
	TargetID        *string             `json:"target_id,omitempty"`
	Name            string              `json:"name"`
	RRule           string              `json:"rrule"`
	DurationSeconds int                 `json:"duration_seconds"`
	Timezone        string              `json:"timezone"`
	Enabled         bool                `json:"enabled"`
	CreatedAt       time.Time           `json:"created_at"`
	UpdatedAt       time.Time           `json:"updated_at"`
}

// CreateBlackoutRequest is the POST body. TargetID is required iff
// scope == "target"; omitted otherwise. Scope defaults to "global".
type CreateBlackoutRequest struct {
	Scope           model.BlackoutScope `json:"scope"`
	TargetID        *string             `json:"target_id,omitempty"`
	Name            string              `json:"name"`
	RRule           string              `json:"rrule"`
	DurationSeconds int                 `json:"duration_seconds"`
	Timezone        string              `json:"timezone,omitempty"`
	Enabled         *bool               `json:"enabled,omitempty"`
}

// UpdateBlackoutRequest is the PUT body. Partial.
type UpdateBlackoutRequest struct {
	Scope           *model.BlackoutScope `json:"scope,omitempty"`
	TargetID        *string              `json:"target_id,omitempty"`
	ClearTarget     bool                 `json:"clear_target,omitempty"`
	Name            *string              `json:"name,omitempty"`
	RRule           *string              `json:"rrule,omitempty"`
	DurationSeconds *int                 `json:"duration_seconds,omitempty"`
	Timezone        *string              `json:"timezone,omitempty"`
	Enabled         *bool                `json:"enabled,omitempty"`
}

func (h *handlers) routeBlackouts(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.listBlackouts(w, r)
	case http.MethodPost:
		h.createBlackout(w, r)
	default:
		methodNotAllowed(w, "GET, POST")
	}
}

func (h *handlers) routeBlackoutByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/blackouts/")
	if id == "" || strings.Contains(id, "/") {
		writeProblem(w, http.StatusNotFound, "/problems/not-found",
			"Unknown blackout subresource", "", nil)
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.getBlackout(w, r, id)
	case http.MethodPut:
		h.updateBlackout(w, r, id)
	case http.MethodDelete:
		h.deleteBlackout(w, r, id)
	default:
		methodNotAllowed(w, "GET, PUT, DELETE")
	}
}

func (h *handlers) listBlackouts(w http.ResponseWriter, r *http.Request) {
	all, err := h.deps.BlackoutStore.List(r.Context())
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "/problems/store",
			"Listing blackouts failed", err.Error(), nil)
		return
	}

	if at := r.URL.Query().Get("active_at"); at != "" {
		instant, perr := time.Parse(time.RFC3339, at)
		if perr != nil {
			writeProblem(w, http.StatusBadRequest, "/problems/invalid-query",
				"Invalid active_at", "must be RFC3339", nil)
			return
		}
		filtered := make([]model.BlackoutWindow, 0, len(all))
		for _, b := range all {
			if !b.Enabled {
				continue
			}
			active, aerr := blackoutActiveAt(b, instant)
			if aerr != nil {
				continue
			}
			if active {
				filtered = append(filtered, b)
			}
		}
		all = filtered
	}

	total := int64(len(all))
	p := ParsePagination(r, MaxLimit)
	lo, hi := p.Offset, p.Offset+p.Limit
	if lo > len(all) {
		lo = len(all)
	}
	if hi > len(all) {
		hi = len(all)
	}
	page := all[lo:hi]
	out := make([]BlackoutResponse, 0, len(page))
	for _, b := range page {
		out = append(out, toBlackoutResponse(b))
	}
	writeJSON(w, http.StatusOK, PaginatedResponse[BlackoutResponse]{
		Items:  out,
		Total:  total,
		Limit:  p.Limit,
		Offset: p.Offset,
	})
}

func (h *handlers) getBlackout(w http.ResponseWriter, r *http.Request, id string) {
	b, err := h.deps.BlackoutStore.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeProblem(w, http.StatusNotFound, "/problems/not-found",
				"Blackout not found", "", nil)
			return
		}
		writeProblem(w, http.StatusInternalServerError, "/problems/store",
			"Loading blackout failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, toBlackoutResponse(*b))
}

func (h *handlers) createBlackout(w http.ResponseWriter, r *http.Request) {
	var req CreateBlackoutRequest
	if err := decodeJSON(r, &req); err != nil {
		writeProblem(w, http.StatusBadRequest, "/problems/invalid-json",
			"Invalid JSON body", err.Error(), nil)
		return
	}

	if req.Scope == "" {
		req.Scope = model.BlackoutScopeGlobal
	}
	if fe := validateBlackoutCreate(&req); len(fe) > 0 {
		writeProblem(w, http.StatusBadRequest, "/problems/validation",
			"Required fields missing", "", fe)
		return
	}
	if _, err := rrule.ValidateRRule(req.RRule); err != nil {
		writeProblem(w, http.StatusUnprocessableEntity, "/problems/validation",
			"Invalid RRULE", err.Error(),
			[]FieldError{{Field: "rrule", Message: err.Error()}})
		return
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	tz := req.Timezone
	if tz == "" {
		tz = "UTC"
	}
	b := model.BlackoutWindow{
		Scope:       req.Scope,
		TargetID:    req.TargetID,
		Name:        req.Name,
		RRule:       req.RRule,
		DurationSec: req.DurationSeconds,
		Timezone:    tz,
		Enabled:     enabled,
	}
	if err := h.deps.BlackoutStore.Create(r.Context(), &b); err != nil {
		writeProblem(w, http.StatusUnprocessableEntity, "/problems/validation",
			"Create failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusCreated, toBlackoutResponse(b))
}

func (h *handlers) updateBlackout(w http.ResponseWriter, r *http.Request, id string) {
	var req UpdateBlackoutRequest
	if err := decodeJSON(r, &req); err != nil {
		writeProblem(w, http.StatusBadRequest, "/problems/invalid-json",
			"Invalid JSON body", err.Error(), nil)
		return
	}
	cur, err := h.deps.BlackoutStore.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeProblem(w, http.StatusNotFound, "/problems/not-found",
				"Blackout not found", "", nil)
			return
		}
		writeProblem(w, http.StatusInternalServerError, "/problems/store",
			"Loading blackout failed", err.Error(), nil)
		return
	}

	if req.Scope != nil {
		cur.Scope = *req.Scope
	}
	if req.ClearTarget {
		cur.TargetID = nil
	} else if req.TargetID != nil {
		cur.TargetID = req.TargetID
	}
	if req.Name != nil {
		cur.Name = *req.Name
	}
	if req.RRule != nil {
		cur.RRule = *req.RRule
	}
	if req.DurationSeconds != nil {
		cur.DurationSec = *req.DurationSeconds
	}
	if req.Timezone != nil {
		cur.Timezone = *req.Timezone
	}
	if req.Enabled != nil {
		cur.Enabled = *req.Enabled
	}

	if cur.DurationSec <= 0 || cur.DurationSec > maxBlackoutDurationSeconds {
		writeProblem(w, http.StatusUnprocessableEntity, "/problems/validation",
			"Invalid duration", "", []FieldError{
				{Field: "duration_seconds", Message: "must be > 0 and <= 7 days"},
			})
		return
	}
	if _, err := rrule.ValidateRRule(cur.RRule); err != nil {
		writeProblem(w, http.StatusUnprocessableEntity, "/problems/validation",
			"Invalid RRULE", err.Error(),
			[]FieldError{{Field: "rrule", Message: err.Error()}})
		return
	}
	if err := h.deps.BlackoutStore.Update(r.Context(), cur); err != nil {
		writeProblem(w, http.StatusUnprocessableEntity, "/problems/validation",
			"Update failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, toBlackoutResponse(*cur))
}

func (h *handlers) deleteBlackout(w http.ResponseWriter, r *http.Request, id string) {
	if err := h.deps.BlackoutStore.Delete(r.Context(), id); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeProblem(w, http.StatusNotFound, "/problems/not-found",
				"Blackout not found", "", nil)
			return
		}
		writeProblem(w, http.StatusInternalServerError, "/problems/store",
			"Delete failed", err.Error(), nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func validateBlackoutCreate(req *CreateBlackoutRequest) []FieldError {
	var fe []FieldError
	if strings.TrimSpace(req.Name) == "" {
		fe = append(fe, FieldError{Field: "name", Message: "required"})
	}
	if strings.TrimSpace(req.RRule) == "" {
		fe = append(fe, FieldError{Field: "rrule", Message: "required"})
	}
	if req.DurationSeconds <= 0 || req.DurationSeconds > maxBlackoutDurationSeconds {
		fe = append(fe, FieldError{Field: "duration_seconds", Message: "must be > 0 and <= 7 days"})
	}
	switch req.Scope {
	case model.BlackoutScopeGlobal:
		if req.TargetID != nil && *req.TargetID != "" {
			fe = append(fe, FieldError{Field: "target_id", Message: "must be unset for global scope"})
		}
	case model.BlackoutScopeTarget:
		if req.TargetID == nil || *req.TargetID == "" {
			fe = append(fe, FieldError{Field: "target_id", Message: "required for target scope"})
		}
	default:
		fe = append(fe, FieldError{Field: "scope", Message: "must be 'global' or 'target'"})
	}
	return fe
}

// blackoutActiveAt reports whether any occurrence of `b` covers `at`.
// Deliberately self-contained (no intervalsched import for the active_at
// filter) so the API layer doesn't depend on the evaluator's implicit
// cache. Mirrors intervalsched.occurrenceCovers semantics.
func blackoutActiveAt(b model.BlackoutWindow, at time.Time) (bool, error) {
	loc, err := time.LoadLocation(b.Timezone)
	if err != nil {
		return false, err
	}
	opt, err := extrrule.StrToROption(b.RRule)
	if err != nil {
		return false, err
	}
	if opt.Dtstart.IsZero() {
		opt.Dtstart = b.CreatedAt.In(loc)
	} else {
		opt.Dtstart = opt.Dtstart.In(loc)
	}
	rule, err := extrrule.NewRRule(*opt)
	if err != nil {
		return false, err
	}
	dur := time.Duration(b.DurationSec) * time.Second
	atTZ := at.In(loc)
	lower := atTZ.Add(-7 * 24 * time.Hour)
	upper := atTZ.Add(time.Nanosecond)
	for _, occ := range rule.Between(lower, upper, true) {
		end := occ.Add(dur)
		if (atTZ.Equal(occ) || atTZ.After(occ)) && atTZ.Before(end) {
			return true, nil
		}
	}
	return false, nil
}

func toBlackoutResponse(b model.BlackoutWindow) BlackoutResponse {
	return BlackoutResponse{
		ID:              b.ID,
		Scope:           b.Scope,
		TargetID:        b.TargetID,
		Name:            b.Name,
		RRule:           b.RRule,
		DurationSeconds: b.DurationSec,
		Timezone:        b.Timezone,
		Enabled:         b.Enabled,
		CreatedAt:       b.CreatedAt,
		UpdatedAt:       b.UpdatedAt,
	}
}

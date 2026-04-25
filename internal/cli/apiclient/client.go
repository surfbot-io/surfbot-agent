package apiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultTimeout caps a single API request end-to-end. The CLI is
// interactive; a runaway request should fail fast rather than block
// the terminal.
const DefaultTimeout = 30 * time.Second

// Client is the surfbot CLI's API client. One instance is shared
// across all subcommands per invocation; tests create their own with
// httptest.NewServer.
//
// The client sets an Origin header (derived from baseURL as
// "scheme://host[:port]") on every request so that it satisfies the
// same-origin check enforced by the webui middleware for mutating
// methods. Callers pointing at a bare apiv1 mux (no webui middleware)
// are unaffected — the header is additive.
type Client struct {
	baseURL    string
	origin     string
	httpClient *http.Client
	userAgent  string
	authToken  string
}

// Option configures a Client at construction.
type Option func(*Client)

// WithHTTPClient swaps the underlying http.Client — useful for tests
// that want to inject a custom transport or override the timeout.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) {
		if h != nil {
			c.httpClient = h
		}
	}
}

// WithUserAgent overrides the default User-Agent string.
func WithUserAgent(ua string) Option {
	return func(c *Client) { c.userAgent = ua }
}

// WithAuthToken sets the bearer token injected into every /api/*
// request. The webui loopback server requires this; callers pointing
// at an unauthenticated daemon can omit it.
func WithAuthToken(token string) Option {
	return func(c *Client) { c.authToken = token }
}

// New returns a Client targeting baseURL (e.g. "http://127.0.0.1:8470").
// Trailing slashes are trimmed so callers don't have to worry about
// double-slashes in request paths. An Origin header is derived from
// baseURL and sent on every request to satisfy the webui same-origin
// check; if baseURL is empty or unparseable, the origin is left empty
// and requests fall through to the underlying HTTP round-trip (callers
// pointing at an unauthenticated daemon or a bare apiv1 mux see no
// behavior change).
func New(baseURL string, opts ...Option) *Client {
	trimmed := strings.TrimRight(baseURL, "/")
	c := &Client{
		baseURL:    trimmed,
		origin:     deriveOrigin(trimmed),
		httpClient: &http.Client{Timeout: DefaultTimeout},
		userAgent:  "surfbot-agent",
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// deriveOrigin reassembles the scheme+host of baseURL into the
// Origin-header shape ("scheme://host[:port]"). Returns "" for inputs
// that url.Parse rejects or that lack a scheme/host — callers treat an
// empty origin as "do not set the header".
func deriveOrigin(baseURL string) string {
	if baseURL == "" {
		return ""
	}
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// BaseURL returns the effective base URL — exposed so the CLI can
// surface it in error messages ("couldn't reach <url>").
func (c *Client) BaseURL() string { return c.baseURL }

// ---- generic request helpers ----

// do executes a request, parses 2xx into `out` (if non-nil), and
// turns non-2xx into an *APIError. body may be nil.
func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		buf := &bytes.Buffer{}
		if err := json.NewEncoder(buf).Encode(body); err != nil {
			return fmt.Errorf("encode request body: %w", err)
		}
		reader = buf
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json, application/problem+json")
	req.Header.Set("User-Agent", c.userAgent)
	if c.origin != "" {
		req.Header.Set("Origin", c.origin)
	}
	if c.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.authToken)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if resp.StatusCode == http.StatusNoContent || out == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			return nil
		}
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil && err != io.EOF {
			return fmt.Errorf("decode response: %w", err)
		}
		return nil
	}
	return parseProblem(resp)
}

// parseProblem reads a non-2xx response body and constructs an APIError
// from it. Tolerates non-JSON bodies — falls back to a generic message
// keyed on status code.
func parseProblem(resp *http.Response) error {
	body, _ := io.ReadAll(resp.Body)
	apiErr := &APIError{StatusCode: resp.StatusCode}
	if len(body) > 0 && json.Valid(body) {
		_ = json.Unmarshal(body, apiErr)
	}
	if apiErr.Title == "" {
		apiErr.Title = http.StatusText(resp.StatusCode)
		if apiErr.Title == "" {
			apiErr.Title = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
	}
	if apiErr.Status == 0 {
		apiErr.Status = resp.StatusCode
	}
	return apiErr
}

// buildQuery composes a URL with query parameters, skipping empty values.
func buildQuery(path string, params map[string]string) string {
	if len(params) == 0 {
		return path
	}
	v := url.Values{}
	for k, val := range params {
		if val == "" {
			continue
		}
		v.Set(k, val)
	}
	if len(v) == 0 {
		return path
	}
	return path + "?" + v.Encode()
}

// ---- Schedules ----

// ListSchedules returns a paginated list of schedules matching params.
// Zero-valued filter fields are omitted from the URL.
func (c *Client) ListSchedules(ctx context.Context, params ListSchedulesParams) (PaginatedResponse[Schedule], error) {
	q := map[string]string{
		"status":      params.Status,
		"target_id":   params.TargetID,
		"template_id": params.TemplateID,
	}
	if params.Limit > 0 {
		q["limit"] = strconv.Itoa(params.Limit)
	}
	if params.Offset > 0 {
		q["offset"] = strconv.Itoa(params.Offset)
	}
	var out PaginatedResponse[Schedule]
	err := c.do(ctx, http.MethodGet, buildQuery("/api/v1/schedules", q), nil, &out)
	return out, err
}

// GetSchedule returns a single schedule by ID.
func (c *Client) GetSchedule(ctx context.Context, id string) (Schedule, error) {
	var out Schedule
	err := c.do(ctx, http.MethodGet, "/api/v1/schedules/"+url.PathEscape(id), nil, &out)
	return out, err
}

// CreateSchedule posts a new schedule.
func (c *Client) CreateSchedule(ctx context.Context, req CreateScheduleRequest) (Schedule, error) {
	var out Schedule
	err := c.do(ctx, http.MethodPost, "/api/v1/schedules", req, &out)
	return out, err
}

// UpdateSchedule patches a schedule. Only non-nil fields on req are
// sent via the JSON marshaler's omitempty.
func (c *Client) UpdateSchedule(ctx context.Context, id string, req UpdateScheduleRequest) (Schedule, error) {
	var out Schedule
	err := c.do(ctx, http.MethodPut, "/api/v1/schedules/"+url.PathEscape(id), req, &out)
	return out, err
}

// DeleteSchedule issues a hard delete.
func (c *Client) DeleteSchedule(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/api/v1/schedules/"+url.PathEscape(id), nil, nil)
}

// PauseSchedule toggles enabled=false. Idempotent on the server.
func (c *Client) PauseSchedule(ctx context.Context, id string) (Schedule, error) {
	var out Schedule
	err := c.do(ctx, http.MethodPost, "/api/v1/schedules/"+url.PathEscape(id)+"/pause", nil, &out)
	return out, err
}

// ResumeSchedule toggles enabled=true. Idempotent on the server.
func (c *Client) ResumeSchedule(ctx context.Context, id string) (Schedule, error) {
	var out Schedule
	err := c.do(ctx, http.MethodPost, "/api/v1/schedules/"+url.PathEscape(id)+"/resume", nil, &out)
	return out, err
}

// UpcomingSchedules returns the next firings across all active
// schedules within the horizon, plus any blackouts in that horizon.
func (c *Client) UpcomingSchedules(ctx context.Context, params UpcomingParams) (UpcomingResponse, error) {
	q := map[string]string{"target_id": params.TargetID}
	if params.Horizon > 0 {
		q["horizon"] = params.Horizon.String()
	}
	if params.Limit > 0 {
		q["limit"] = strconv.Itoa(params.Limit)
	}
	var out UpcomingResponse
	err := c.do(ctx, http.MethodGet, buildQuery("/api/v1/schedules/upcoming", q), nil, &out)
	return out, err
}

// BulkSchedules runs a pause/resume/delete/clone over a set of IDs
// atomically on the server.
func (c *Client) BulkSchedules(ctx context.Context, req BulkScheduleRequest) (BulkScheduleResponse, error) {
	var out BulkScheduleResponse
	err := c.do(ctx, http.MethodPost, "/api/v1/schedules/bulk", req, &out)
	return out, err
}

// ---- Templates ----

// ListTemplates returns a paginated list of templates.
func (c *Client) ListTemplates(ctx context.Context, limit, offset int) (PaginatedResponse[Template], error) {
	q := map[string]string{}
	if limit > 0 {
		q["limit"] = strconv.Itoa(limit)
	}
	if offset > 0 {
		q["offset"] = strconv.Itoa(offset)
	}
	var out PaginatedResponse[Template]
	err := c.do(ctx, http.MethodGet, buildQuery("/api/v1/templates", q), nil, &out)
	return out, err
}

// GetTemplate returns a single template by ID.
func (c *Client) GetTemplate(ctx context.Context, id string) (Template, error) {
	var out Template
	err := c.do(ctx, http.MethodGet, "/api/v1/templates/"+url.PathEscape(id), nil, &out)
	return out, err
}

// CreateTemplate posts a new template.
func (c *Client) CreateTemplate(ctx context.Context, req CreateTemplateRequest) (Template, error) {
	var out Template
	err := c.do(ctx, http.MethodPost, "/api/v1/templates", req, &out)
	return out, err
}

// UpdateTemplate patches a template and triggers a server-side cascade
// recompute of next_run_at for every dependent schedule.
func (c *Client) UpdateTemplate(ctx context.Context, id string, req UpdateTemplateRequest) (Template, error) {
	var out Template
	err := c.do(ctx, http.MethodPut, "/api/v1/templates/"+url.PathEscape(id), req, &out)
	return out, err
}

// DeleteTemplate refuses with 409 when dependents exist unless force
// is true, in which case dependent schedules are deleted atomically.
func (c *Client) DeleteTemplate(ctx context.Context, id string, force bool) error {
	path := "/api/v1/templates/" + url.PathEscape(id)
	if force {
		path += "?force=true"
	}
	return c.do(ctx, http.MethodDelete, path, nil, nil)
}

// ---- Blackouts ----

// ListBlackouts returns a paginated list of blackouts.
// activeAt is an RFC3339 instant; "" skips the filter.
func (c *Client) ListBlackouts(ctx context.Context, activeAt string, limit, offset int) (PaginatedResponse[Blackout], error) {
	q := map[string]string{"active_at": activeAt}
	if limit > 0 {
		q["limit"] = strconv.Itoa(limit)
	}
	if offset > 0 {
		q["offset"] = strconv.Itoa(offset)
	}
	var out PaginatedResponse[Blackout]
	err := c.do(ctx, http.MethodGet, buildQuery("/api/v1/blackouts", q), nil, &out)
	return out, err
}

// GetBlackout returns a single blackout by ID.
func (c *Client) GetBlackout(ctx context.Context, id string) (Blackout, error) {
	var out Blackout
	err := c.do(ctx, http.MethodGet, "/api/v1/blackouts/"+url.PathEscape(id), nil, &out)
	return out, err
}

// CreateBlackout posts a new blackout window.
func (c *Client) CreateBlackout(ctx context.Context, req CreateBlackoutRequest) (Blackout, error) {
	var out Blackout
	err := c.do(ctx, http.MethodPost, "/api/v1/blackouts", req, &out)
	return out, err
}

// UpdateBlackout patches a blackout.
func (c *Client) UpdateBlackout(ctx context.Context, id string, req UpdateBlackoutRequest) (Blackout, error) {
	var out Blackout
	err := c.do(ctx, http.MethodPut, "/api/v1/blackouts/"+url.PathEscape(id), req, &out)
	return out, err
}

// DeleteBlackout removes a blackout.
func (c *Client) DeleteBlackout(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/api/v1/blackouts/"+url.PathEscape(id), nil, nil)
}

// ---- Defaults ----

// GetDefaults returns the singleton schedule_defaults row.
func (c *Client) GetDefaults(ctx context.Context) (ScheduleDefaults, error) {
	var out ScheduleDefaults
	err := c.do(ctx, http.MethodGet, "/api/v1/schedule-defaults", nil, &out)
	return out, err
}

// UpdateDefaults full-replaces the singleton row. Callers doing a
// partial update must GET first, merge, then pass the merged value.
func (c *Client) UpdateDefaults(ctx context.Context, req UpdateScheduleDefaultsRequest) (ScheduleDefaults, error) {
	var out ScheduleDefaults
	err := c.do(ctx, http.MethodPut, "/api/v1/schedule-defaults", req, &out)
	return out, err
}

// ---- Ad-hoc scans ----

// CreateAdHocScan dispatches a one-off scan. On success returns the
// ad_hoc_run_id (and, when the dispatcher returns synchronously, the
// scan_id). Typed error classes surfaced as APIError with
// status 409 (busy / in-blackout) or 503 (dispatcher unreachable).
func (c *Client) CreateAdHocScan(ctx context.Context, req CreateAdHocRequest) (CreateAdHocResponse, error) {
	var out CreateAdHocResponse
	err := c.do(ctx, http.MethodPost, "/api/v1/scans/ad-hoc", req, &out)
	return out, err
}

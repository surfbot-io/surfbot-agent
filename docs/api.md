# REST API reference — `/api/v1/*`

This is the endpoint-by-endpoint reference for the versioned surfbot
REST API shipped with SPEC-SCHED1 (agent-spec 3.0). For concepts behind
the resources, read [`docs/scheduling.md`](scheduling.md) first.

## Conventions

- **Base URL**: the agent binds to `127.0.0.1:8470` by default. Every
  endpoint below is rooted at `/api/v1/`.
- **Content-Type**: requests with a body use `application/json`.
  Responses are `application/json` or, on error, `application/problem+json`.
- **Authentication**: a loopback token is injected by the daemon into
  the SPA shell and must accompany every `/api/*` request as
  `Authorization: Bearer <token>` when the daemon was started with
  `--auth-token` (or equivalent). Token handling is unchanged from the
  webui subsystem; no new auth surface ships with 1.5.
- **Versioning**: `/api/v1/*` is the stable API. Breaking changes
  require a `/api/v2/*` sibling; additive changes stay within `v1`.
- **Pagination**: list endpoints accept `limit` (1–500, default 50)
  and `offset` (default 0). Responses wrap items in
  `{"items":[…],"total":N,"limit":N,"offset":N}`.

### Problem responses

Every error response is RFC 7807-flavored:

```json
{
  "type": "/problems/validation",
  "title": "Required fields missing",
  "status": 422,
  "detail": "target_id must be a UUID",
  "field_errors": [
    { "field": "target_id", "message": "required" }
  ]
}
```

Problem `type` values the API emits:

| Type | Meaning |
| --- | --- |
| `/problems/validation` | Request body failed validation (400 or 422 depending on phase). |
| `/problems/invalid-json` | Body did not decode as JSON (400). |
| `/problems/invalid-query` | Query param failed validation (400). |
| `/problems/missing-id` | Subresource URL lacked an id (400). |
| `/problems/not-found` | Resource does not exist (404). |
| `/problems/already-exists` | Unique-constraint violation on create (409). |
| `/problems/overlap` | Schedule's occurrences overlap an existing one (422). |
| `/problems/template-in-use` | Template delete refused, schedules still reference it (409). |
| `/problems/target-busy` | Target already has a scan running (409). |
| `/problems/in-blackout` | Target is inside an active blackout (409). |
| `/problems/dispatch-failed` | Scheduler returned an uncategorized error (500). |
| `/problems/dispatcher-unreachable` | Caller's process has no attached master ticker (503). |
| `/problems/bulk-failed` | Bulk endpoint encountered an unexpected error (500). |
| `/problems/store` | Underlying storage call failed (500). |
| `/problems/method-not-allowed` | HTTP method not supported on this path (405). |

### Deprecations

- `POST /api/daemon/trigger` was **removed** in SPEC-SCHED1.4a. Callers
  must use `POST /api/v1/scans/ad-hoc`.
- `GET /api/v1/schedule` (singular) is a sibling 410 Gone handler kept
  only to signal the migration — operators should use `/api/v1/schedules`.

## Schedules

### `GET /api/v1/schedules`

List schedules. Query: `status` (`active`/`paused`), `target_id`,
`template_id`, `limit`, `offset`.

```bash
curl -s 'http://127.0.0.1:8470/api/v1/schedules?status=active' \
  -H "Authorization: Bearer $TOKEN"
```

Returns `PaginatedResponse<ScheduleResponse>`.

### `GET /api/v1/schedules/{id}`

Fetch one. 200 with `ScheduleResponse`; 404 `/problems/not-found`.

### `POST /api/v1/schedules`

Create. Required: `target_id`, `name`, `rrule`, `dtstart`, `timezone`.
Optional: `template_id`, `tool_config`, `overrides`, `maintenance_window`,
`enabled`, `estimated_duration_seconds`.

```bash
curl -s -X POST http://127.0.0.1:8470/api/v1/schedules \
  -H 'Content-Type: application/json' \
  --data-binary '{
    "target_id": "TARGET_ID",
    "name": "nightly",
    "rrule": "FREQ=DAILY;BYHOUR=2",
    "dtstart": "2026-04-22T02:00:00Z",
    "timezone": "UTC"
  }'
```

| Status | Problem | When |
| --- | --- | --- |
| 400 | `/problems/invalid-json` | Body not JSON. |
| 400 | `/problems/validation` | Required field missing. |
| 422 | `/problems/validation` | RRULE invalid or target/template not found. |
| 422 | `/problems/overlap` | Would overlap an existing schedule on the same target. |
| 409 | `/problems/already-exists` | (target_id, name) already taken. |
| 201 | — | Created. |

### `PUT /api/v1/schedules/{id}`

Partial update. All fields optional. To clear optional fields, set
`clear_template` or `clear_maintenance_window` to `true`. Returns 200.

### `DELETE /api/v1/schedules/{id}`

Hard delete. 204 on success. 404 if absent.

### `POST /api/v1/schedules/{id}/pause` · `POST /api/v1/schedules/{id}/resume`

Idempotent. Return 200 with the updated `ScheduleResponse`.

### `GET /api/v1/schedules/upcoming`

Upcoming firings in a horizon.

Query: `horizon` (Go duration, default `24h`, max `720h`), `target_id`,
`limit`.

Returns:

```json
{
  "items": [
    {
      "schedule_id": "...",
      "target_id": "...",
      "template_id": null,
      "fires_at": "2026-04-22T02:00:00Z"
    }
  ],
  "horizon_end": "2026-04-23T02:00:00Z",
  "blackouts_in_horizon": [
    {
      "blackout_id": "...",
      "starts_at": "2026-04-22T09:00:00Z",
      "ends_at":   "2026-04-22T17:00:00Z"
    }
  ]
}
```

### `POST /api/v1/schedules/bulk`

Atomic-per-item bulk op. Body:

```json
{ "operation": "pause", "schedule_ids": ["id1", "id2"] }
```

`operation` ∈ `pause` / `resume` / `delete` / `clone`. `clone` also
requires `create_template` carrying name/rrule/dtstart/timezone for the
new schedules. Returns:

```json
{
  "operation": "pause",
  "succeeded": ["id1"],
  "failed": [{"schedule_id": "id2", "error": "not found"}]
}
```

See [`docs/examples/04-bulk-schedule-creation.md`](examples/04-bulk-schedule-creation.md).

## Templates

### `GET /api/v1/templates`

List templates. Paginated. Returns `PaginatedResponse<TemplateResponse>`.

### `GET /api/v1/templates/{id}`

Fetch one.

### `POST /api/v1/templates`

Create. Required: `name`, `rrule`. Optional: `description`, `timezone`
(defaults to `UTC`), `tool_config`, `maintenance_window`.

| Status | Problem | When |
| --- | --- | --- |
| 400 | `/problems/validation` | `name` or `rrule` missing. |
| 422 | `/problems/validation` | Invalid RRULE or unknown tool in tool_config. |
| 409 | `/problems/already-exists` | `name` already taken. |
| 201 | — | Created. |

### `PUT /api/v1/templates/{id}`

Partial update. 200 on success.

### `DELETE /api/v1/templates/{id}`

Hard delete. Query `?force=true` cascade-deletes dependent schedules
atomically.

| Status | Problem | When |
| --- | --- | --- |
| 409 | `/problems/template-in-use` | Schedules still reference it; pass `?force=true` or delete them first. |
| 404 | `/problems/not-found` | Absent. |
| 204 | — | Deleted. |

## Blackouts

### `GET /api/v1/blackouts`

List. Query: `active_at` (RFC 3339 instant, filters to blackouts whose
window contains it), `limit`, `offset`.

### `GET /api/v1/blackouts/{id}` · `POST` · `PUT` · `DELETE`

Standard CRUD. POST body:

```json
{
  "scope": "target",
  "target_id": "TARGET_ID",
  "name": "weekly-maint",
  "rrule": "FREQ=WEEKLY;BYDAY=SA;BYHOUR=2",
  "duration_seconds": 7200,
  "timezone": "UTC",
  "enabled": true
}
```

`scope` ∈ `global` / `target`. `target_id` is required iff `scope="target"`.
`duration_seconds` ≤ 7 days (604800).

PUT supports `clear_target` to drop the target-id (and, if you also
set `scope="global"`, convert a scoped blackout into a global one).

## Schedule defaults

### `GET /api/v1/schedule-defaults`

Returns the singleton. On a fresh daemon with no row, the server
returns server defaults (`default_rrule: "FREQ=DAILY;BYHOUR=2"`,
`max_concurrent_scans: 4`, etc.) with a zero `updated_at`.

### `PUT /api/v1/schedule-defaults`

Full-replace update. Partial fields are not supported — the Web UI
merges current state + form values before calling this. A successful
PUT cascades: every schedule whose effective config depends on the
defaults has its `next_run_at` recomputed.

## Ad-hoc scans

### `POST /api/v1/scans/ad-hoc`

Fire a one-off scan. Body:

```json
{
  "target_id": "TARGET_ID",
  "template_id": "TEMPLATE_ID",
  "tool_config_override": {
    "nuclei": {"severity": ["critical"]}
  },
  "reason": "re-check after infra change",
  "requested_by": "alice@example.com"
}
```

Absent `tool_config_override` → server auto-populates from the
template's tool_config (when `template_id` is set) merged with the
defaults. Send `{}` to run with zero overrides.

Responses:

| Status | Problem | When |
| --- | --- | --- |
| 202 | — | Dispatched. Body: `{ad_hoc_run_id, scan_id?}`. |
| 400 | `/problems/validation` | `target_id` missing. |
| 422 | `/problems/validation` | Target not found, or invalid tool params in override. |
| 409 | `/problems/target-busy` | Another scan already running on this target. |
| 409 | `/problems/in-blackout` | Target is inside an active blackout. |
| 503 | `/problems/dispatcher-unreachable` | This process doesn't have the master ticker. |

See [`docs/examples/03-adhoc-subfinder-httpx-chain.md`](examples/03-adhoc-subfinder-httpx-chain.md)
for a worked example.

## Tool schemas

### `GET /api/v1/schemas/tools`

Returns the list of tool names that have a shipped JSON Schema:

```json
{ "tools": ["dnsx", "httpx", "naabu", "nuclei", "subfinder"] }
```

### `GET /api/v1/schemas/tools/{tool}`

Returns the raw JSON Schema (`application/json`; Draft 2020-12). 404
with `/problems/not-found` on an unknown tool.

```bash
curl -s http://127.0.0.1:8470/api/v1/schemas/tools/nuclei | jq .title
# "Nuclei tool params"
```

Schemas are hand-written mirrors of the `NucleiParams` / `NaabuParams`
/ `HttpxParams` / `SubfinderParams` / `DnsxParams` structs in
`internal/model/tool_params.go`. They're kept in sync by the round-trip
test at [`internal/model/tool_schema_test.go`](../internal/model/tool_schema_test.go).

## Meta

This API is part of agent-spec 3.0. Use `surfbot agent-spec --version`
for the canonical version string at runtime. See
[`docs/agent-spec.md`](agent-spec.md) for the changelog and migration
guide.

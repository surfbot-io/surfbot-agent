# Scheduling — operator concept guide

This page explains how surfbot's scheduling subsystem fits together after
the SPEC-SCHED1 rewrite (agent-spec 3.0). If you're coming from 2.x,
read the [migration guide](agent-spec.md#migration-from-20) first.

## Overview

Scheduling is built from four first-class resources, each with its own
REST endpoint and CLI verb:

1. **Templates** — reusable tool-config + RRULE bundles, referenced by
   many schedules.
2. **Schedules** — per-target recurrence definitions. They can carry
   their own full config or inherit most of it from a template.
3. **Blackout windows** — periods when no scan may run, enforced at
   dispatch time and mid-scan.
4. **Schedule defaults** — a singleton row supplying fallback values
   for any schedule field not set at the schedule or template layer.

A scan, when it runs, is the product of a **cascade**: the effective
config is resolved from schedule → template → defaults, with schedule-
level `overrides` pinning specific fields. Ad-hoc runs bypass the
schedule layer but still respect blackouts and go through the same
dispatch path.

## Templates

A template is a named, reusable configuration. It carries:

- **`name`** (unique) and optional **`description`**.
- **`rrule`** + **`timezone`** — the default recurrence for every
  schedule that points at it.
- **`tool_config`** — the per-tool params that actually drive the scan.
  Keys must be names in `RegisteredToolParams`
  (`nuclei`/`naabu`/`httpx`/`subfinder`/`dnsx`); values are the
  corresponding typed structs. See [`docs/schemas/tools/`](schemas/tools/)
  for the JSON Schemas.
- **`maintenance_window`** — optional embedded blackout-like window
  that applies only to this template's children.
- **`is_system`** — templates that ship with the binary. Not editable
  or deletable via the API; the Web UI hides the Edit / Delete buttons.

Editing a template cascades: every schedule that references it recomputes
its effective config on the next tick. Schedules that override
`rrule` / `timezone` / `maintenance_window` at the schedule layer keep
their override; all other fields follow the template.

Deleting a template is hard. By default the API refuses with
`409 /problems/template-in-use` if any schedule still references it.
Pass `?force=true` (the UI does this after a second confirm) to cascade-
delete the dependent schedules in the same transaction.

## Schedules

A schedule binds a target to a recurrence:

- **`target_id`** — the target this schedule fires against. Immutable
  after creation.
- **`template_id`** — optional pointer to a template.
- **`name`** — unique per target.
- **`rrule`** + **`dtstart`** + **`timezone`** — the recurrence. RRULE
  is validated against `internal/rrule`; invalid RRULEs get
  `422 /problems/validation` with the invalid field echoed.
- **`tool_config`** — optional per-schedule overrides that replace the
  template's (or defaults') values field-by-field.
- **`overrides`** — explicit list of field names that should use the
  schedule's value even when the template provides one. The
  `POST /schedules` handler auto-adds `rrule` / `timezone` /
  `maintenance_window` to this list when the caller supplied them
  explicitly — a UX guard against accidental inheritance.
- **`enabled`** — `true` for active, `false` for paused. The master
  ticker skips paused schedules; they never fire until resumed.
- **`estimated_duration_seconds`** — used only at create/update time
  for the [overlap check](#overlap-check). NOT persisted.
- **`next_run_at` / `last_run_at` / `last_run_status` / `last_scan_id`**
  — observability fields the scheduler writes back after every tick.

### Status

`status` is derived, not stored: `active` when `enabled=true`,
`paused` otherwise. Use `POST /schedules/{id}/pause` and
`/resume` (both idempotent) to flip it.

### Deletion

Hard delete — there is no soft-delete column. Once gone, history in
`scan_runs` keeps the foreign key value but the schedule row is gone.

### Overlap check

When you create or update a schedule, the handler expands every other
schedule pointing at the same target across a 7-day horizon and checks
that no pair of occurrences within the horizon sit within
`estimated_duration_seconds` of each other. Failing overlaps return
`422 /problems/overlap` with the conflicting schedule IDs echoed.
Without `estimated_duration_seconds`, the server uses the 3600s
default.

## Blackout windows

A blackout is a recurring "do not scan" interval. Its shape:

- **`scope`** — `"global"` (affects every target) or `"target"`
  (affects only the referenced target).
- **`target_id`** — required when `scope="target"`, omitted otherwise.
- **`name`** — display-only.
- **`rrule`** + **`timezone`** — recurrence.
- **`duration_seconds`** — how long each occurrence lasts, capped at
  7 days. Longer periods should be expressed as multiple blackouts.
- **`enabled`** — kill switch; disabled blackouts are ignored.

### Filtering active blackouts

`GET /api/v1/blackouts?active_at=<RFC3339>` returns only blackouts
whose window contains the given instant. Omit the param to see every
row regardless of current state.

### Pause-in-flight

When a blackout activates while a scan is running against an affected
target, the scheduler cancels the job with `ErrBlackoutPause`. The scan
row transitions to `canceled` with a corresponding reason on the
`scan_runs` audit trail. The master ticker does not auto-resume — the
next scheduled tick (after the blackout ends) re-dispatches normally.

Ad-hoc runs dispatched during a blackout return
`409 /problems/in-blackout` immediately; they never start.

## Schedule defaults

A singleton row supplies fallback values for any field the schedule
and its template both leave unset:

- `default_template_id` — optional default template for schedules
  created without one specified.
- `default_rrule` / `default_timezone` / `default_tool_config` /
  `default_maintenance_window`.
- `max_concurrent_scans` — worker-pool width.
- `run_on_start` — whether overdue schedules fire immediately at daemon
  boot.
- `jitter_seconds` — randomized offset added to each tick so coordinated
  fleets don't hammer infrastructure.

PUT is a full-replace — partial updates are not supported. The Web UI
merges the current state with the edited fields before calling PUT,
so editing `jitter_seconds` alone in the UI doesn't silently reset
`default_tool_config`.

## Ad-hoc runs

Ad-hoc scans are one-offs not tied to a schedule's RRULE expansion.
They live at `POST /api/v1/scans/ad-hoc`:

- **`target_id`** (required), **`template_id`** (optional),
  **`tool_config_override`** (optional), **`reason`** (optional),
  **`requested_by`** (optional; defaults to `"api:ad-hoc"`).
- If `tool_config_override` is absent, the server auto-populates it
  from the template (when `template_id` is set) + defaults. Send an
  empty object to explicitly run with zero overrides.
- Returns `202` with `ad_hoc_run_id` and (when the scheduler is
  reachable) an immediate `scan_id`.
- Errors the operator will see often: `409 /problems/target-busy`
  (another scan already running on this target), `409 /problems/in-blackout`,
  `503 /problems/dispatcher-unreachable` (talking to a process that
  isn't `surfbot daemon run`).

The previous `POST /api/daemon/trigger` endpoint was removed with
SPEC-SCHED1.4a. Integrators should migrate to `/api/v1/scans/ad-hoc`.

## Cascade resolver

When the master ticker fires a schedule, it builds the scan's effective
config by walking:

```
schedule.<field>                          ← if in schedule.overrides, or schedule set it
  └── template.<field>                    ← if the template has it
       └── schedule_defaults.<field>       ← final fallback
            └── compile-time default      ← if defaults row is absent
```

For `tool_config`, the merge is per-tool-key: if the schedule's
`tool_config["nuclei"]` is set, it fully replaces the template's
`nuclei` params. Other tool keys continue to inherit from the template.

## Common patterns

Worked examples with copy-pasteable curl and CLI commands:

- [`01-daily-critical-nuclei.md`](examples/01-daily-critical-nuclei.md) —
  create a template + daily schedule.
- [`02-weekly-naabu-with-business-blackout.md`](examples/02-weekly-naabu-with-business-blackout.md) —
  combine a weekly schedule with a global business-hours blackout.
- [`03-adhoc-subfinder-httpx-chain.md`](examples/03-adhoc-subfinder-httpx-chain.md) —
  one-off dispatch with per-tool overrides.
- [`04-bulk-schedule-creation.md`](examples/04-bulk-schedule-creation.md) —
  bulk pause/resume/delete/clone via the bulk endpoint.

## Troubleshooting

### "My schedule isn't firing"

Check, in order:

1. `surfbot schedule show <id>` — is `status` active? A paused schedule
   never fires.
2. Is `next_run_at` in the past? The master ticker only acts on
   schedules whose `next_run_at <= now` and also validates against
   blackouts. If it's in the future, wait; if in the past, the daemon
   may not be running.
3. Is a blackout active? `GET /api/v1/blackouts?active_at=<now>` tells
   you.
4. `surfbot daemon status` — is the master ticker running? It lives
   inside `surfbot daemon run`.

### "I got 409 TARGET_BUSY"

Another scan is already running on this target. Wait for it to finish
(or cancel it explicitly via the API) and retry.

### "I got 503 dispatcher-unreachable"

You're talking to a process that doesn't have the master ticker
attached. The UI web server, a read-only API process, or a CLI client
will all return 503 on ad-hoc dispatch. Point your request at the
process running `surfbot daemon run`.

### "Why was my scan canceled halfway?"

Check `scan_runs.reason` — if it's `blackout_pause`, a blackout window
activated mid-scan (the pause-in-flight behavior). The next scheduled
tick after the blackout ends re-dispatches normally.

### "403 `missing origin` from the CLI"

The `surfbot ui` web server enforces a same-origin check on every
mutating request (`POST`/`PUT`/`DELETE`/`PATCH`) — cross-site callers
without an `Origin` header pointing back at the loopback UI are
rejected with `403 missing origin`. Builds from **SCHED1-HOTFIX-P0 and
later** derive the `Origin` header automatically inside the CLI's API
client, so the stock `surfbot schedule|template|blackout|defaults|scan
adhoc` commands just work. If you see this 403, upgrade your `surfbot`
binary; older builds predate the fix and will keep getting rejected
even against localhost.

### "Edit to a template didn't take effect"

Template edits cascade asynchronously: the server triggers
`RecomputeNextRunForTemplate` inline, but the new effective config only
lands on the next tick. If you want to force it, pause+resume the
affected schedule — that re-computes `next_run_at` immediately.

## Further reading

- [`docs/api.md`](api.md) — endpoint reference with curl examples.
- [`docs/agent-spec.md`](agent-spec.md) — v3.0 changelog + migration.
- [`docs/schemas/tools/`](schemas/tools/) — JSON Schemas for every tool.

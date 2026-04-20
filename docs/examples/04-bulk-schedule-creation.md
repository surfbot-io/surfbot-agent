# Example 04 — Bulk schedule operations

**What this does.** Uses `POST /api/v1/schedules/bulk` to pause, resume,
or delete many schedules in one transaction — the operation the Web UI
bulk-actions bar calls under the hood. Then shows the clone variant for
fan-out.

## Pause many schedules at once

Bulk-pause every schedule listed in the request:

```json
{
  "operation": "pause",
  "schedule_ids": [
    "9f4d2c93-a1b8-4e88-9d56-2a0e4bfa1e55",
    "0e5e31b2-cc2a-4b21-9fa6-3f1bdc9e2d42"
  ]
}
```

```bash
curl -s -X POST http://127.0.0.1:8470/api/v1/schedules/bulk \
  -H 'Content-Type: application/json' \
  --data-binary @- <<'EOF'
{
  "operation": "pause",
  "schedule_ids": [
    "9f4d2c93-a1b8-4e88-9d56-2a0e4bfa1e55",
    "0e5e31b2-cc2a-4b21-9fa6-3f1bdc9e2d42"
  ]
}
EOF
```

Response:

```json
{
  "operation": "pause",
  "succeeded": ["9f4d2c93-a1b8-4e88-9d56-2a0e4bfa1e55"],
  "failed": [
    {
      "schedule_id": "0e5e31b2-cc2a-4b21-9fa6-3f1bdc9e2d42",
      "error": "not found"
    }
  ]
}
```

Per-item failures do NOT abort the transaction — the handler succeeds
for every schedule that existed and reports the rest in `failed`.

## Resume or delete

Identical shape, different `operation`:

```json
{ "operation": "resume", "schedule_ids": ["..."] }
```

```json
{ "operation": "delete", "schedule_ids": ["..."] }
```

Deletion is hard: rows go and don't come back. The Web UI wraps this
operation in a confirmation modal; CLI and API callers are responsible
for their own guardrails.

## Clone with a template override

Clone multiple existing schedules onto a new name/rrule/dtstart trio —
each clone inherits the source's `target_id` and `tool_config`:

```json
{
  "operation": "clone",
  "schedule_ids": [
    "9f4d2c93-a1b8-4e88-9d56-2a0e4bfa1e55"
  ],
  "create_template": {
    "name": "nightly-clone",
    "rrule": "FREQ=DAILY;BYHOUR=23;BYMINUTE=30",
    "dtstart": "2026-04-22T23:30:00Z",
    "timezone": "UTC"
  }
}
```

```bash
curl -s -X POST http://127.0.0.1:8470/api/v1/schedules/bulk \
  -H 'Content-Type: application/json' \
  --data-binary @- <<'EOF'
{
  "operation": "clone",
  "schedule_ids": [
    "9f4d2c93-a1b8-4e88-9d56-2a0e4bfa1e55"
  ],
  "create_template": {
    "name": "nightly-clone",
    "rrule": "FREQ=DAILY;BYHOUR=23;BYMINUTE=30",
    "dtstart": "2026-04-22T23:30:00Z",
    "timezone": "UTC"
  }
}
EOF
```

## CLI equivalent

Per-schedule CLI counterparts exist (`surfbot schedule pause <id>`,
`surfbot schedule resume <id>`, `surfbot schedule delete <id>`). The
bulk endpoint is the API-only fast path the UI uses when the operator
selects multiple rows.

## Expected result

- 200 with the per-item `succeeded` / `failed` arrays.
- Every succeeded schedule's `next_run_at` is recomputed synchronously
  after the state change.
- Clone writes new rows with fresh UUIDs.

## See also

- [`docs/api.md`](../api.md) — full endpoint reference
- [`docs/scheduling.md`](../scheduling.md) — hard-delete semantics

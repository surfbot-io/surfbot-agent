# Example 01 — Daily critical-only Nuclei scan

**What this does.** Creates a reusable "critical-only" nuclei template,
then attaches it to a daily 02:00 UTC schedule on a single target. Only
critical and high severity findings are surfaced.

## The template

```json
{
  "name": "critical-only",
  "description": "Nightly sweep for critical/high severity CVEs.",
  "rrule": "FREQ=DAILY;BYHOUR=2;BYMINUTE=0",
  "timezone": "UTC",
  "tool_config": {
    "nuclei": {
      "severity": ["critical", "high"],
      "rate_limit": 150,
      "timeout": 5000000000
    }
  }
}
```

Create it:

```bash
curl -s -X POST http://127.0.0.1:8470/api/v1/templates \
  -H 'Content-Type: application/json' \
  --data-binary @- <<'EOF'
{
  "name": "critical-only",
  "description": "Nightly sweep for critical/high severity CVEs.",
  "rrule": "FREQ=DAILY;BYHOUR=2;BYMINUTE=0",
  "timezone": "UTC",
  "tool_config": {
    "nuclei": {
      "severity": ["critical", "high"],
      "rate_limit": 150,
      "timeout": 5000000000
    }
  }
}
EOF
```

Capture the returned `id` — that's your `TEMPLATE_ID`.

## The schedule

Attach the template to a target (replace `TARGET_ID` with an id from
`GET /api/v1/targets`):

```json
{
  "target_id": "TARGET_ID",
  "template_id": "TEMPLATE_ID",
  "name": "example.com nightly critical",
  "rrule": "FREQ=DAILY;BYHOUR=2;BYMINUTE=0",
  "dtstart": "2026-04-21T02:00:00Z",
  "timezone": "UTC"
}
```

```bash
curl -s -X POST http://127.0.0.1:8470/api/v1/schedules \
  -H 'Content-Type: application/json' \
  --data-binary @- <<'EOF'
{
  "target_id": "TARGET_ID",
  "template_id": "TEMPLATE_ID",
  "name": "example.com nightly critical",
  "rrule": "FREQ=DAILY;BYHOUR=2;BYMINUTE=0",
  "dtstart": "2026-04-21T02:00:00Z",
  "timezone": "UTC"
}
EOF
```

## Expected result

`GET /api/v1/schedules?target_id=TARGET_ID` now returns the new schedule
with `next_run_at` set to 02:00 UTC tomorrow. At the scheduled tick the
master ticker enqueues a scan that runs nuclei with the severity filter
pinned to critical+high. The resulting findings — if any — land under
`GET /api/v1/findings?target_id=TARGET_ID`.

## See also

- [`docs/scheduling.md`](../scheduling.md) — template / schedule concepts
- [`docs/api.md`](../api.md) — full endpoint reference
- [`docs/schemas/tools/nuclei.json`](../schemas/tools/nuclei.json) — schema for the nuclei block

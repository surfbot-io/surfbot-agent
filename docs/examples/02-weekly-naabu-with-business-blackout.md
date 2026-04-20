# Example 02 — Weekly naabu with business-hours blackout

**What this does.** Runs a naabu port scan every Sunday at 03:00 UTC
against a single target, and installs a global blackout so no scan (of
any target) fires during business hours Monday–Friday. Useful when you
want dawn-of-week coverage but refuse to risk production traffic during
the workday.

## The template

```json
{
  "name": "weekly-naabu",
  "description": "Weekly port-scan sweep using the top-1000 port preset.",
  "rrule": "FREQ=WEEKLY;BYDAY=SU;BYHOUR=3;BYMINUTE=0",
  "timezone": "UTC",
  "tool_config": {
    "naabu": {
      "ports": "top1000",
      "rate": 50,
      "scan_type": "connect",
      "banner_grab": true
    }
  }
}
```

```bash
curl -s -X POST http://127.0.0.1:8470/api/v1/templates \
  -H 'Content-Type: application/json' \
  --data-binary @- <<'EOF'
{
  "name": "weekly-naabu",
  "description": "Weekly port-scan sweep using the top-1000 port preset.",
  "rrule": "FREQ=WEEKLY;BYDAY=SU;BYHOUR=3;BYMINUTE=0",
  "timezone": "UTC",
  "tool_config": {
    "naabu": {
      "ports": "top1000",
      "rate": 50,
      "scan_type": "connect",
      "banner_grab": true
    }
  }
}
EOF
```

## The schedule

```json
{
  "target_id": "TARGET_ID",
  "template_id": "TEMPLATE_ID",
  "name": "example.com weekly naabu",
  "rrule": "FREQ=WEEKLY;BYDAY=SU;BYHOUR=3;BYMINUTE=0",
  "dtstart": "2026-04-26T03:00:00Z",
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
  "name": "example.com weekly naabu",
  "rrule": "FREQ=WEEKLY;BYDAY=SU;BYHOUR=3;BYMINUTE=0",
  "dtstart": "2026-04-26T03:00:00Z",
  "timezone": "UTC"
}
EOF
```

## The global business-hours blackout

```json
{
  "scope": "global",
  "name": "business-hours",
  "rrule": "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR;BYHOUR=9;BYMINUTE=0",
  "duration_seconds": 28800,
  "timezone": "UTC",
  "enabled": true
}
```

```bash
curl -s -X POST http://127.0.0.1:8470/api/v1/blackouts \
  -H 'Content-Type: application/json' \
  --data-binary @- <<'EOF'
{
  "scope": "global",
  "name": "business-hours",
  "rrule": "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR;BYHOUR=9;BYMINUTE=0",
  "duration_seconds": 28800,
  "timezone": "UTC",
  "enabled": true
}
EOF
```

## Expected result

- Weekly naabu fires every Sunday 03:00 UTC — outside every blackout
  occurrence by construction.
- Any ad-hoc scan attempted against any target Mon–Fri 09:00–17:00 UTC
  returns `409 /problems/in-blackout`.
- In-flight scans that straddle 09:00 UTC on a weekday are canceled
  with `ErrBlackoutPause` (see [`docs/scheduling.md`](../scheduling.md#pause-in-flight)).

## See also

- [`docs/scheduling.md`](../scheduling.md) — blackout scope + pause-in-flight semantics
- [`docs/schemas/tools/naabu.json`](../schemas/tools/naabu.json)

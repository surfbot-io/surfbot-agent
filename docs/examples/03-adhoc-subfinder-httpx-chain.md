# Example 03 — Ad-hoc subfinder + httpx chain

**What this does.** Fires a one-off ad-hoc scan that combines subfinder
(passive subdomain enumeration) and httpx (live-endpoint probing) with
non-default knobs, then watches the run through completion. Useful when
you've just onboarded a target and want immediate discovery coverage
without waiting for a scheduled run.

## API form — `POST /api/v1/scans/ad-hoc`

Request body:

```json
{
  "target_id": "TARGET_ID",
  "reason": "initial onboarding sweep",
  "tool_config_override": {
    "subfinder": {
      "all_sources": true,
      "recursive": true
    },
    "httpx": {
      "threads": 80,
      "probes": ["http", "https"],
      "follow_redirects": true,
      "timeout": 15000000000
    }
  }
}
```

```bash
curl -s -X POST http://127.0.0.1:8470/api/v1/scans/ad-hoc \
  -H 'Content-Type: application/json' \
  --data-binary @- <<'EOF'
{
  "target_id": "TARGET_ID",
  "reason": "initial onboarding sweep",
  "tool_config_override": {
    "subfinder": {
      "all_sources": true,
      "recursive": true
    },
    "httpx": {
      "threads": 80,
      "probes": ["http", "https"],
      "follow_redirects": true,
      "timeout": 15000000000
    }
  }
}
EOF
```

The 202 response carries `ad_hoc_run_id` and, when the scheduler is
reachable, an immediate `scan_id`.

## CLI form

```bash
surfbot scan ad-hoc \
  --target TARGET_ID \
  --reason 'initial onboarding sweep' \
  --override '{"subfinder":{"all_sources":true,"recursive":true},"httpx":{"threads":80,"follow_redirects":true,"timeout":15000000000}}'
```

## Expected result

- Immediate 202 with an `ad_hoc_run_id`.
- Within seconds, `GET /api/v1/scans/{scan_id}` shows `status: "running"`
  transitioning to `completed`.
- Discovered subdomains land as `subdomain` assets; live endpoints land
  as `url` assets; any httpx-surfaced findings (tech fingerprints, weak
  TLS, missing security headers) land in `/api/v1/findings`.
- The `reason` is persisted on the ad_hoc_scan_runs row for the audit
  trail.

## Error cases to expect

| Status | Problem type | Cause |
| --- | --- | --- |
| 409 | `/problems/target-busy` | Another scan is already running on this target. Retry after it finishes. |
| 409 | `/problems/in-blackout` | Target (or global) blackout is active. Retry later. |
| 503 | `/problems/dispatcher-unreachable` | The process you're talking to isn't the one running the master ticker. Point the API call at `surfbot daemon run`. |

## See also

- [`docs/scheduling.md`](../scheduling.md) — ad-hoc vs scheduled runs
- [`docs/schemas/tools/subfinder.json`](../schemas/tools/subfinder.json)
- [`docs/schemas/tools/httpx.json`](../schemas/tools/httpx.json)

# `surfbot agent-spec` — LLM interface

`agent-spec` emits a self-describing JSON (or markdown) contract of every
Surfbot CLI command. Give the JSON output to a cold LLM and it has
everything it needs to drive Surfbot atomically: subcommands, flags,
positional args, input/output types, composition rules, and canonical
recipes.

## Quick start

```sh
surfbot agent-spec --format json > surfbot.spec.json
```

Then in your LLM prompt:

> You are operating Surfbot via a shell. Use only the commands and
> schemas described in the attached spec. Chain atomic commands via the
> declared pipe rules.

Attach `surfbot.spec.json`. Done.

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--format json\|md` | `json` | Output format. JSON is the canonical contract; md is for humans. |
| `--command <name>` | "" | Return spec for a single command (dotted path, e.g. `findings.list`). |
| `--schema-only` | false | Emit only types + per-command input/output schema refs. Smaller payload for prompt injection. |
| `--version` | false | Print only the spec version and agent version. |

Exit codes: `0` success, `2` unknown `--command`, `1` any other error.

## Document shape

See `internal/cli/agent_spec.go` for the canonical Go types. Top-level:

```
{
  spec_version, agent_version, generated_at, binary, description,
  global_flags: [...],
  commands:     [{name, path, summary, category, flags, input, output, examples, ...}],
  types:        {TypeName: JSONSchemaFragment, ...},
  composition:  {pipes: [...], recipes: [...]}
}
```

### Command categories

- `atomic-detection` — a direct wrapper over a detection tool (discover, resolve, portscan, probe, assess)
- `scan` — the orchestrated recipe over all atomic tools
- `target`, `findings`, `assets`, `score`, `status` — state management
- `daemon` — OS service subcommands
- `meta` — everything else (`tools`, `version`, `agent-spec`, `fix`, `connectors`)

### Composition

`composition.pipes` declares which commands can be piped into which:
a consumer's `input.type` is compatible with a producer's `output.type`.
`composition.recipes` declares canonical named compositions — today only
`scan`, which mirrors the live `surfbot scan` pipeline.

## Scope guarantees

Surfbot will not persist assets whose hostname lies outside the declared
target scope. When a target hostname resolves to a shared IP (CDN, cloud
PaaS, shared hosting), Surfbot sends the target hostname as the HTTP
`Host` header so the server returns the intended vhost. If the server
responds with a different vhost — detected by comparing the final URL
hostname and TLS certificate SANs against the expected hostname — the
response is dropped silently and logged to stderr with
`reason=vhost_mismatch`.

**Input formats for `probe`:**

- `hostname|ip:port/tcp` — scoped probe. Request goes to the IP, `Host`
  header is set to the hostname. Response is dropped if the effective
  host does not match.
- `ip:port/tcp` — IP-pure probe. No `Host` override, no scope check.
  Whatever the server returns is persisted under the IP.
- `hostname` — DNS-resolved probe, self-scoped. Off-site redirects
  trigger the drop.

The `scan` pipeline produces `hostname|ip:port/tcp` automatically by
pairing resolved IPs with their originating hostnames.

**Observability:** each drop emits one stderr line in key=value form:

```
reason=vhost_mismatch expected_host=<h> observed_host=<h> ip=<ip> port=<p> status=<code>
```

The total is accumulated on the run's `ToolRun.Config["vhost_mismatch_drops"]`.

**Known limitation — plaintext HTTP over IP.** When the probe is plaintext
HTTP dialed directly by IP and the server does not issue an off-site
redirect, there is no cryptographic evidence either way as to whether the
server honored the `Host` header or silently served its default vhost.
Surfbot trusts the `Host` header in this case and keeps the response. A
redirect to a different hostname still trips the drop (rule 1 above). For
strict attribution of plaintext probes on shared IPs, use HTTPS targets
where the certificate CN/SANs provide the proof.

## Stability

`spec_version` is semver:
- **major** — breaking changes (removed commands, renamed fields)
- **minor** — additive (new commands, new optional fields)
- **patch** — doc/description changes only

Current: **3.1.0**.

## Changelog

### 3.1.0 — built-in scan templates (SPEC-SCHED2.3)

Additive. The Spec envelope grows a top-level `builtin_templates`
array describing the three templates the agent seeds into
`scan_templates` on first boot (`Default`, `Fast`, `Deep`). Each entry
carries `name`, `description`, `recommended_for`, `rrule`, `timezone`,
and `tool_config` — the same shape an LLM consumer would otherwise
have to retrieve via `GET /api/v1/templates`. Useful for cold-start
orchestration: an LLM can call `--template Default` immediately
without first listing.

No removals or renames; pre-3.1 consumers that ignore unknown fields
keep working unchanged.

### 3.0.0 — first-class schedules (SPEC-SCHED1)

Breaking rewrite of the scheduling subsystem. Pre-3.0, recurrence was
driven by a single `schedule.config.json` file read at daemon boot;
3.0 promotes scheduling into four first-class resources (templates,
schedules, blackout windows, schedule defaults) with their own REST
endpoints and CLI verbs.

**What's new**

- **Four resources**, each with full CRUD at `/api/v1/*`:
  `schedules`, `templates`, `blackouts`, `schedule-defaults`. See
  [`docs/scheduling.md`](scheduling.md) for the conceptual model and
  [`docs/api.md`](api.md) for the endpoint reference.
- **Typed tool params.** `ToolConfig` is no longer an opaque
  `map[string]interface{}`. Each tool name (`nuclei`, `naabu`, `httpx`,
  `subfinder`, `dnsx`) maps to a validated Go struct in
  `internal/model/tool_params.go`. Unknown tool keys are rejected at
  create/update time. JSON Schemas for each are shipped under
  [`docs/schemas/tools/`](schemas/tools/) and served at
  `/api/v1/schemas/tools/{tool}`.
- **Canonical ad-hoc dispatch** at `POST /api/v1/scans/ad-hoc`, with
  `tool_config_override` auto-population from the template + defaults
  when absent.
- **Blackout windows** with `scope` (`global` or `target`) + optional
  `target_id`, **pause-in-flight** semantics (in-flight scans canceled
  with `ErrBlackoutPause` when a blackout activates mid-scan), and a
  7-day max duration.
- **Schedule defaults singleton** with cascade resolution:
  `schedule → template → defaults → compile-time`.
- **Bulk endpoint** at `POST /api/v1/schedules/bulk` for atomic-per-
  item pause/resume/delete/clone across many schedules at once.
- **Web UI overhaul** — `#/schedules`, `#/templates`, `#/blackouts`,
  `#/settings/defaults`. Every resource has create/edit/delete;
  schedules support pause/resume + bulk ops; target-detail pages show
  schedules for the target and wire the ad-hoc modal. Upcoming
  firings live in the per-schedule detail page (a standalone Timeline
  page existed in 1.4c and was removed in UI v2 PR12).

**What's removed**

- `POST /api/daemon/trigger` is gone. Callers must use
  `POST /api/v1/scans/ad-hoc`. The trigger handler, tests, dashboard
  "Scan now" button, and JS caller were all deleted in SPEC-SCHED1.4a
  and the button was restored against the new endpoint in 1.4c.
- `GET /api/v1/schedule` (singular) returns `410 Gone` with a pointer
  to `/api/v1/schedules` (plural).
- The legacy `/settings/schedule` SPA page is gone — operators use
  `#/schedules` and `#/settings/defaults` instead.
- `schedule.config.json` is no longer read at boot. On first 3.0 boot
  against a 2.x data dir, the SCHED1.1 migration auto-imports the old
  config into the new tables.

### Migration from 2.0

**Data migration.** SPEC-SCHED1.1 installs a one-shot migration that
runs on daemon first boot with 3.0: any pre-existing
`schedule.config.json` is translated into a system template + one
schedule per configured target, and the file is archived. The
migration is idempotent — a second boot with the file already absent
is a no-op.

**CLI callers.** Replace:

- `surfbot scan trigger ...` → `surfbot scan ad-hoc ...`
- `surfbot schedule show` / `--set ...` (pre-3.0 single-config
  management) → new `surfbot schedule list / show / create / update /
  delete / pause / resume` verbs. Run `surfbot schedule --help` for the
  full surface. The old CLI commands are gone in 1.3b; they will not
  reappear.

**HTTP integrators.** Replace:

- `POST /api/daemon/trigger` with body `{"profile":"full"}` →
  `POST /api/v1/scans/ad-hoc` with `{"target_id": "..."}`. Profile-
  based dispatch is no longer the API shape; target-anchored is.
- `GET /api/v1/schedule` → `GET /api/v1/schedules` (plural) + filter
  by `target_id` or `template_id` as needed.
- Raw `tool_config` blobs → typed params per the schemas at
  [`docs/schemas/tools/`](schemas/tools/). Submitting unknown keys now
  returns `422 /problems/validation`.

**See also:**

- [`docs/scheduling.md`](scheduling.md) — full concept guide.
- [`docs/api.md`](api.md) — endpoint reference.
- [`docs/examples/`](examples/) — four copy-pasteable recipes.

### 2.0.0 — scan aggregates redesign (SPEC-QA3 / SUR-244)

The `ScanStats` type is removed. `Scan` now exposes three semantically
distinct aggregates, each populated from database queries at scan
completion (no in-memory accumulators, no drift from `list` subcommands):

- **`target_state`** — what the target looks like at scan completion.
  Source: queries against `assets` and `findings` filtered by `target_id`.
  Answers *"what currently exists?"*. Includes `assets_by_type`,
  `assets_total`, `ports_by_status`, `findings_open`, `findings_open_total`,
  `findings_by_status`, and `remediations` (empty placeholder until
  remediation tooling lands).
- **`delta`** — what *this* scan changed relative to prior state. Source:
  the `asset_changes` table (scan_id-scoped) plus finding status transitions.
  Answers *"what did this scan discover or resolve?"*. Includes
  `new_assets`, `disappeared_assets`, `modified_assets`, `new_findings`,
  `resolved_findings`, `returned_findings`, and `is_baseline`.
- **`work`** — telemetry of the execution itself. Source: `tool_runs` and
  scan timing. Includes `duration_ms`, `tools_run`, `tools_failed`,
  `tools_skipped`, `phases_run`, and `raw_emissions` (pre-dedup tool output
  count, useful for debugging tool noise).

**Field mapping (1.x → 2.0):**

| 1.x (`stats.*`) | 2.0 (|
|---|---|
| `subdomains_found` | `target_state.assets_by_type.subdomain` |
| `ips_resolved` | `target_state.assets_by_type.ipv4` + `.ipv6` |
| `open_ports` | `target_state.ports_by_status.open` |
| *(new)* | `target_state.ports_by_status.filtered` |
| `http_probed` | `target_state.assets_by_type.url` |
| `tech_detected` | `target_state.assets_by_type.technology` |
| `ports_scanned` | *removed (was never populated)* |
| `findings_total` | `target_state.findings_open_total` *(strictly DB-derived, no longer inflated by pre-dedup emissions)* |
| `findings_<severity>` | `target_state.findings_open.<severity>` |

**Open-ended stat buckets.** `assets_by_type`, `new_assets`, `ports_by_status`,
and siblings are JSON objects keyed by enum strings. For `AssetType` the
vocabulary is **open** — a new detection tool may introduce a new key
without a spec version bump. Consumers must tolerate unknown keys. For
`Severity`, `FindingStatus`, and `RemediationStatus` the vocabulary is
closed (universal concepts, not tool-specific).

**Finding.scan_id semantics changed.** It now tracks the LATEST scan that
observed the finding (updated on every upsert). The immutable originating
scan moved to a new field `first_seen_scan_id`. This makes
`COUNT(*) WHERE scan_id = X AND status = 'open'` a meaningful query for
"findings observed by scan X" — which was broken under 1.x semantics.

**Assets type CHECK removed.** The `assets.type` column no longer has a
SQL CHECK constraint. A new detection tool that emits an asset type
outside the built-in vocabulary no longer requires a schema migration —
`AssetType` is validated in Go. The `Finding` severity and `Asset` status
enums remain closed at the SQL level.

### 1.2.0 — portscan status metadata (SUR-243/SUR-248)

Portscan assets gain `status` (open|filtered) and `banner_preview` metadata.

### 1.1.0 — enriched probe input (SUR-242)

`probe` accepts `hostname|ip:port/tcp` alongside bare `ip:port/tcp`.

## Design notes

- Generated at runtime from the live Cobra tree. Never ship a
  hand-maintained JSON file — it will drift.
- Type schemas are hand-coded in `internal/cli/agent_spec_schemas.go`
  (pinned to `internal/model` structs via tests). If a model struct
  changes, update the schema deliberately.
- `agent-spec` is in `skipDBCommands` — it never opens the SQLite store.
  Safe to run with `--db /nonexistent/path`.
- Invariant tests in `internal/cli/agent_spec_test.go` enforce
  completeness (every Cobra command in the spec), detection coverage,
  pipe consistency, and recipe executability against the live tree.

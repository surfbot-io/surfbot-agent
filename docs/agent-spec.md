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

Current: **2.0.0**.

## Changelog

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

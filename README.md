# Surfbot Agent

Local security scanner with pluggable detection and remediation. Open source under MIT license.

## Quick Start

```bash
surfbot ui
```

That opens the dashboard at `http://127.0.0.1:8470` and starts the
scheduler in the same process. Add a target, pick one of the built-in
templates (`Default`, `Fast`, or `Deep` — seeded automatically on
first boot), create a schedule, walk away — scans fire on the
schedule until you Ctrl+C. See [`docs/scheduling.md`](docs/scheduling.md#built-in-templates)
for what each template runs.

If you want to run surfbot as a long-lived OS service instead (no
browser-facing UI on the same host), see [Run as a service](#run-as-a-service)
below.

## Build from Source

```bash
git clone https://github.com/surfbot-io/surfbot-agent.git
cd surfbot-agent
make build
./bin/surfbot version
```

## Commands

Reference for the CLI surface. Run `surfbot <command> --help` for flags
and examples on each.

**Scanning**

```
surfbot scan [target]        Run a security scan against a target
surfbot scan adhoc           Dispatch an ad-hoc scan via the daemon API
surfbot discover             Discover subdomains for a target via passive sources
surfbot resolve              Resolve domains to IP addresses (DNS)
surfbot portscan             Scan hosts for open TCP ports
surfbot probe                Probe host:port pairs for live HTTP services
surfbot assess               Run vulnerability assessment with nuclei templates
```

**Scheduling**

```
surfbot schedule {create|list|show|update|delete|pause|resume}
                             Manage first-class scan schedules
surfbot blackout             Manage blackout windows (suppress scans)
surfbot defaults             View and update schedule defaults
```

**Data and remediation**

```
surfbot target {add|list|remove}
                             Manage monitored targets
surfbot findings             List discovered vulnerabilities
surfbot assets               List discovered assets
surfbot fix <id>             Apply remediation for a finding
surfbot score                Show security score
surfbot status               Show agent status
```

**Operations**

```
surfbot daemon {install|uninstall|start|stop|restart|status|logs}
                             Install and control the background service
surfbot tools                Manage detection tools (enable/disable)
surfbot connectors           Manage MCP connectors
surfbot agent-spec           Emit machine-readable contract for LLM orchestration
surfbot version              Print version info
```

## Run as a service

Surfbot can run as a long-lived background service that triggers scheduled
scans automatically. The same `surfbot` binary registers itself with the
local service manager (systemd on Linux, launchd on macOS, the Service
Control Manager on Windows).

### Linux (systemd)

```bash
sudo surfbot daemon install
sudo surfbot daemon start
surfbot daemon status
surfbot daemon logs -f
sudo surfbot daemon stop
sudo surfbot daemon uninstall
```

### macOS (launchd)

By default `daemon install` registers a per-user LaunchAgent
(`~/Library/LaunchAgents/surfbot.plist`) — no `sudo` required. The
agent runs as your user so it can read your `~/Library/Application
Support/surfbot/` config and database. Pass `--system` to install a
system-wide LaunchDaemon instead (requires `sudo`, runs as root).

```bash
surfbot daemon install
surfbot daemon start
surfbot daemon status
surfbot daemon logs -f
surfbot daemon stop
```

Manual release verification for macOS lives in
[`docs/daemon-macos-qa.md`](docs/daemon-macos-qa.md).

### Windows (Service Control Manager)

Run from an Administrator PowerShell:

```powershell
surfbot.exe daemon install
surfbot.exe daemon start
surfbot.exe daemon status
surfbot.exe daemon logs
surfbot.exe daemon stop
```

### Status output

`surfbot daemon status` reports the running state, PID, version, uptime,
and the next scheduled scan. Use `--json` for machine-readable output.
Exit codes: `0` running, `3` stopped, `4` unknown / not installed.

## Scheduling

Surfbot uses **first-class schedules** — RRULE-based, target-anchored,
template-driven. Manage them through the UI dashboard or the CLI:

```bash
# Built-in templates are seeded on first boot — no setup required.
surfbot template list

# Create a schedule from a template (daily at 02:00 UTC):
surfbot schedule create \
  --target <target-id> \
  --template Default \
  --name "daily" \
  --rrule "FREQ=DAILY;BYHOUR=2"

# List, pause, resume, edit, delete:
surfbot schedule list
surfbot schedule pause   <id>
surfbot schedule resume  <id>
surfbot schedule update  <id>
surfbot schedule delete  <id>
```

Schedule data, blackouts, and template configs live in the SQLite
database (`~/.surfbot/surfbot.db` by default), survive restarts, and
are visible from both the UI and the CLI. See
[`docs/scheduling.md`](docs/scheduling.md) for the full operator guide
covering blackouts, the cascade resolver, pause-in-flight, and the
maintenance window.

If the process dies mid-scan (panic, OOM, `kill -9`, power loss),
the next start automatically marks orphaned `scans` rows as `failed`
with `error="orphaned on scheduler restart"`. No manual cleanup. See
[`docs/scheduling.md` → Crash recovery](docs/scheduling.md#crash-recovery).

## Embedded UI

`surfbot ui` runs a localhost-only web dashboard at `http://127.0.0.1:8470`
backed by the same SQLite database and config files as the CLI. By
default it also runs the scheduler in-process, so schedules created
through the dashboard fire and "Run scan now" buttons work without a
separate daemon. Pass `--no-scheduler` to opt out (useful when you've
already installed `surfbot daemon` as a system service and want the UI
process to stay read-only).

### Security model

The UI binds to `127.0.0.1` only, but loopback alone is not a sufficient
boundary on a multi-user host or against DNS-rebinding attacks from
malicious web pages the user might visit. The server applies four
defenses in depth:

1. **Bearer token on every `/api/*` request.** On first launch
   `surfbot ui` generates a 32-byte hex token and stores it in
   `<state_dir>/ui.token` (mode `0600`, user-mode state dir, never
   touched by the daemon). The token is reused across restarts. The
   served `index.html` carries it in a `<meta name="surfbot-token">`
   tag; the SPA reads it once and forwards `Authorization: Bearer …`
   on every fetch. Other local users cannot read the token file and
   therefore cannot reach the API.
2. **Origin / Referer check on mutating requests.** `POST`, `PUT`,
   `PATCH`, and `DELETE` are rejected with `403` unless the request
   carries an `Origin` (or, failing that, `Referer`) that points back
   at `http://127.0.0.1:8470` or `http://localhost:8470`. This blocks
   CSRF from any other origin even if the attacker guesses the token.
3. **Host header allowlist.** Requests whose `Host` header is not one
   of the loopback aliases are rejected with `421 Misdirected Request`.
   This is the anti-DNS-rebinding gate: even after a rebind the
   attacker page still sends its own hostname, so the kernel-accepted
   request never reaches a handler.
4. **Strict response headers.** Every response carries
   `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`,
   `Referrer-Policy: no-referrer`, and a CSP that allows only same-
   origin scripts/styles, forbids inline scripts and styles, and sets
   `frame-ancestors 'none'`, `base-uri 'none'`, and
   `form-action 'none'`. JSON responses under `/api/` additionally
   carry `Cache-Control: no-store`.

### Agent card

The dashboard's top card mirrors `surfbot daemon status`. It polls
`GET /api/daemon/status` every 8 seconds and renders one of four states:

1. **Running, window closed** — green dot, version, uptime, last/next
   full and quick scan times, and the configured maintenance window.
2. **Running, window open** — same, but the "Window" line shows `open`.
3. **Running, scheduler disabled** — header is green; scheduler block
   collapses to "Scheduler disabled".
4. **Stopped or not installed** — red/amber dot with a one-click
   copy-to-clipboard button. The exact command shown is supplied by the
   server in the `install_hint` block (see the response shape below) so
   the UI never has to guess between `install` / `start` or whether to
   prefix `sudo`.

The endpoint never shells out to systemctl/launchctl. Liveness is
inferred from the freshness of `daemon.state.json`'s `written_at` field;
the daemon is reported as stopped when the heartbeat is older than
`3 × daemon.state_heartbeat` (90 s with default config).

Sensitive substrings (`api_key=`, `Authorization: Bearer …`, long
opaque blobs) in the scheduler's `last_error` fields are redacted server
side before being returned.

#### `/api/daemon/status` response shape

The endpoint always returns 200; failure modes are expressed in the
body. `HEAD` is also accepted for liveness probes (no body, same status
code).

```json
{
  "installed": true,
  "running": true,
  "pid": 4317,
  "version": "0.4.0",
  "started_at": "2026-04-08T08:12:03Z",
  "uptime_seconds": 7421,
  "scheduler": {
    "enabled": true,
    "last_full":  { "at": "2026-04-07T03:00:11Z", "status": "ok" },
    "last_quick": { "at": "2026-04-08T09:30:02Z", "status": "ok" },
    "next_full":  "2026-04-08T03:00:00Z",
    "next_quick": "2026-04-08T10:30:00Z",
    "window": {
      "enabled":   true,
      "start":     "02:00",
      "end":       "06:00",
      "timezone":  "Europe/Madrid",
      "open_now":  false,
      "next_open": "2026-04-09T02:00:00+02:00"
    }
  }
}
```

When the daemon is not running, `running` is `false`, the `scheduler`
block is omitted, and an `install_hint` block is attached:

```json
{
  "installed": false,
  "running": false,
  "install_hint": {
    "install_command": "sudo surfbot daemon install",
    "start_command":   "sudo surfbot daemon start",
    "docs_url":        "https://github.com/surfbot-io/surfbot-agent#run-as-a-service",
    "requires_admin":  true
  }
}
```

`install_hint.install_command` is empty when the daemon is installed but
stopped — only `start_command` is meaningful in that case. The strings
are derived server-side from `runtime.GOOS`: Linux gets `sudo` prefixes
and `requires_admin: true`; macOS gets unprefixed user-mode commands and
`requires_admin: false`; Windows gets unprefixed commands but
`requires_admin: true` because the binary cannot self-elevate from the
CLI (the UI surfaces a "run from an elevated terminal" hint instead of
inventing a `sudo` equivalent).

### Ad-hoc scans

Trigger a one-off scan against a target you've already added:

- **From the UI**: open the target page or the ad-hoc modal in the sidebar, click "Run scan now".
- **From the CLI**: `surfbot scan adhoc --target <id>`
- **Direct API**: `POST /api/v1/scans/ad-hoc` (RFC 7807 problem+json on errors).

The dispatch is synchronous against the in-process scheduler — the API
returns once the scan is queued and a `scan_id` is allocated. Findings
are persisted to the same database the dashboard reads.

### API reference

Full REST endpoint reference with curl examples and Problem-response
tables: [`docs/api.md`](docs/api.md). All endpoints sit behind the same
loopback token and CSRF / Host / header defenses described in the
[Security model](#security-model) section.

## LLM integration — `surfbot agent-spec`

`surfbot agent-spec` emits a machine-readable contract of every command,
flag, input/output schema, and composition rule. Pipe it into any LLM to
give it a complete picture of Surfbot with zero prior knowledge:

```sh
surfbot agent-spec --format json > surfbot.spec.json
```

Current agent-spec version: **3.1.0** (see [docs/agent-spec.md](docs/agent-spec.md)
for the changelog).

## Documentation

- [`docs/scheduling.md`](docs/scheduling.md) — operator concept guide
  for first-class scheduling (templates, schedules, blackouts, defaults,
  cascade resolver, pause-in-flight, crash recovery, built-in templates).
- [`docs/api.md`](docs/api.md) — REST endpoint reference with curl
  examples and Problem-response tables.
- [`docs/agent-spec.md`](docs/agent-spec.md) — agent-spec document
  shape, stability guarantees, and changelog.
- [`docs/examples/`](docs/examples/) — copy-pasteable recipes
  (daily nuclei, weekly naabu + blackout, ad-hoc chain, bulk ops).
- [`docs/schemas/tools/`](docs/schemas/tools/) — JSON Schemas for
  every tool's Params struct, also served at
  `/api/v1/schemas/tools/{tool}`.

## License

MIT - see [LICENSE](LICENSE).

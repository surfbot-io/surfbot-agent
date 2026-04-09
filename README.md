# Surfbot Agent

Local security scanner with pluggable detection and remediation. Open source under MIT license.

> **Early development.** The agent is being built. See [ADR-001](../surfbot-strategy/ADR-001-surfbot-agent-architecture.md) for the architecture vision.

## Quick Start

(coming soon)

## Build from Source

```bash
git clone https://github.com/surfbot-io/surfbot-agent.git
cd surfbot-agent
make build
./bin/surfbot version
```

## Commands

```
surfbot scan [target]        Run detection pipeline
surfbot target add/list/rm   Manage monitored targets
surfbot findings             List discovered vulnerabilities
surfbot assets               List discovered assets
surfbot fix <id>             Apply remediation
surfbot score                Security score
surfbot status               Agent status
surfbot tools                Manage detection tools
surfbot version              Version info
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
(`~/Library/LaunchAgents/io.surfbot.plist`) — no `sudo` required. The
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

### Scheduling

The daemon runs full scans and lightweight quick checks on independent
cadences, defined under `daemon.scheduler` in `~/.surfbot/config.yaml`:

```yaml
daemon:
  scheduler:
    enabled: true               # turn the scheduler off without uninstalling
    full_scan_interval: 24h     # full pipeline (default: 24h)
    quick_check_interval: 1h    # quick checks only (default: 1h)
    jitter: 5m                  # +/- jitter applied to each tick (default: 5m)
    run_on_start: false         # fire one full scan immediately on daemon start
    quick_check_tools:          # tool whitelist for quick checks
      - httpx
      - nuclei
    maintenance_window:         # optional: suppress new scans during this window
      enabled: false
      start: "02:00"            # HH:MM, may cross midnight (e.g. 22:00 → 06:00)
      end:   "04:00"
      timezone: "Europe/Madrid" # IANA tz; DST-correct
```

Rules:

- Intervals must be ≥ `1m`. If `quick_check_interval ≥ full_scan_interval`,
  quick checks are disabled and a warning is logged.
- `jitter` is capped at `min(interval) / 10` and only ever added (never
  subtracted), so a fresh start is never pushed into the past.
- Inside the maintenance window, new scans are deferred to the next close
  time. In-flight scans are not killed.
- A failed scan still advances the cursor — the scheduler never enters a
  tight retry loop on a permanent failure. The error is recorded in
  `last_full_status` / `last_quick_status` and surfaced by
  `surfbot daemon status`.
- Cursors persist across restarts in `schedule.state.json`, alongside
  `daemon.state.json`.

## Embedded UI

`surfbot ui` runs a localhost-only web dashboard at `http://127.0.0.1:8470`
backed by the same SQLite database and config files as the CLI.

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

### Scan now

The Agent card has a profile selector (full / quick) and a **Scan now**
button. Clicking it `POST`s to `/api/daemon/trigger`, which writes a
`trigger.json` flag file into the daemon's state directory. The
scheduler picks the file up on its next idle poll (≤ 2 s), claims it via
atomic rename, runs the requested profile, and writes the result back to
`schedule.state.json`. The next status poll then surfaces the updated
`last_full_at` / `last_quick_at`.

**Triggers bypass the maintenance window.** This is intentional — a
manual click is an explicit user override of the configured silence
period. The button's tooltip and this paragraph are the only places this
behavior is documented; do not change it without updating both.

A second click while a trigger is in flight returns `409 Conflict`. The
button itself is also disabled for one poll cycle after firing.

### Endpoints

| Method     | Path                  | Notes                                              |
| ---------- | --------------------- | -------------------------------------------------- |
| GET / HEAD | `/api/daemon/status`  | always 200; status in body (HEAD has empty body)   |
| POST       | `/api/daemon/trigger` | body: `{"profile":"full"\|"quick"}`; 202 / 409     |

Both endpoints sit behind the same loopback token and CSRF / Host /
header defenses described in the [Security model](#security-model)
section. `/api/daemon/trigger` is a mutating route, so it additionally
requires a same-origin `Origin` (or `Referer`) header.

These live outside `/api/v1/` because they describe the daemon process,
not versioned domain data.

## LLM integration — `surfbot agent-spec`

`surfbot agent-spec` emits a machine-readable contract of every command,
flag, input/output schema, and composition rule. Pipe it into any LLM to
give it a complete picture of Surfbot with zero prior knowledge:

```sh
surfbot agent-spec --format json > surfbot.spec.json
```

See [docs/agent-spec.md](docs/agent-spec.md) for the document shape and
stability guarantees.

## Architecture

See [ADR-001](../surfbot-strategy/ADR-001-surfbot-agent-architecture.md).

## License

MIT - see [LICENSE](LICENSE).

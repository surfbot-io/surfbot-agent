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

## Architecture

See [ADR-001](../surfbot-strategy/ADR-001-surfbot-agent-architecture.md).

## License

MIT - see [LICENSE](LICENSE).

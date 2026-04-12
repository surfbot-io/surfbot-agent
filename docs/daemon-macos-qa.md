# macOS Daemon QA Checklist

Manual pre-release verification for `surfbot daemon` on macOS. Use this
before shipping changes that touch `internal/daemon/` or
`internal/cli/daemon.go`.

CI cannot install launchd services on GitHub-hosted runners, so this check
runs on a developer laptop.

## Targets

- Apple Silicon (`arm64`): run the full checklist for every relevant release.
- Intel (`x86_64`): run install/start/stop/uninstall coverage at least once
  per minor release.

## Pre-reqs

- Clean macOS user account with no prior `surfbot` launchd install.
- Local build, for example: `GOOS=darwin GOARCH=arm64 go build -o surfbot ./cmd/surfbot`
- Terminal with `sudo` access for system-mode checks.

## 1. User-mode install (`LaunchAgent`, default)

- [ ] `./surfbot daemon install` exits `0`.
- [ ] Output includes `service file: ~/Library/LaunchAgents/surfbot.plist`.
- [ ] `~/Library/LaunchAgents/surfbot.plist` exists and is owned by the current user.
- [ ] Second `./surfbot daemon install` is idempotent: prints `already installed`, exits `0`.
- [ ] `./surfbot daemon start` exits `0`.
- [ ] `launchctl list surfbot` shows a `PID`.
- [ ] `./surfbot daemon status` prints `running`, the current PID, version, and a non-zero `next scan`.
- [ ] `./surfbot daemon status --json` returns valid JSON consistent with the human output.
- [ ] Exit code of `./surfbot daemon status` is `0` while running.

## 2. Logs and rotation

- [ ] Log file exists at `~/Library/Logs/surfbot/daemon.log`.
- [ ] `./surfbot daemon logs` prints the last 200 lines from the current log file.
- [ ] `./surfbot daemon logs -f` tails live output. On an idle daemon there may be no heartbeat log entries; trigger activity by restarting the service or waiting for scheduler output if enabled.
- [ ] Force rotation by writing about 12 MB of filler and confirm rotated files appear as `daemon-*.log.gz` alongside `daemon.log`.
- [ ] `./surfbot daemon logs --since 1m` returns only recent entries.
- [ ] No log line contains raw API tokens or secrets.

Notes:

- Live `daemon.log` is created by lumberjack with mode `0600`.
- Rotated files use timestamped names, not `daemon.log.1`.

## 3. Stop and restart

- [ ] `./surfbot daemon stop` returns within 20 s, exit `0`.
- [ ] `launchctl list surfbot` shows no `PID` or reports the job absent.
- [ ] `pgrep -fl "nuclei|httpx|naabu|subfinder"` returns empty.
- [ ] `./surfbot daemon status` exits `3` and prints `stopped`.
- [ ] `./surfbot daemon restart` starts the service again and the PID changes.

## 4. Crash recovery

- [ ] With the daemon running, `sudo kill -9 <pid>`.
- [ ] launchd `KeepAlive` respawns the service within about 10 s.
- [ ] `~/Library/Application Support/surfbot/daemon.state.json` reflects the new PID after the next heartbeat.
- [ ] No stale `*.tmp` files remain in the state directory.

## 5. Uninstall

- [ ] `./surfbot daemon stop && ./surfbot daemon uninstall` exits `0`.
- [ ] `~/Library/LaunchAgents/surfbot.plist` is removed.
- [ ] `launchctl list surfbot` no longer shows the job.
- [ ] Logs and state files are preserved.

## 6. System-mode install (`LaunchDaemon`, `--system`)

- [ ] `sudo ./surfbot daemon install --system` exits `0`.
- [ ] Output includes `service file: /Library/LaunchDaemons/surfbot.plist`.
- [ ] `/Library/LaunchDaemons/surfbot.plist` exists and is owned by `root`.
- [ ] `./surfbot daemon install --system` without `sudo` fails with a clear permission error.
- [ ] `sudo ./surfbot daemon start --system` starts the service as `root`.
- [ ] State and config live under `/Library/Application Support/surfbot/`.
- [ ] Logs live under `/Library/Logs/surfbot/`.
- [ ] Directories are created `0750`.
- [ ] `daemon.state.json` and `daemon.log` are created `0600` by the current implementation.
- [ ] Repeat stop/uninstall checks with `sudo`.

## 7. Path sanity

User mode should resolve to:

- [ ] Config: `~/Library/Application Support/surfbot/config.yaml`
- [ ] State: `~/Library/Application Support/surfbot/daemon.state.json`
- [ ] Logs: `~/Library/Logs/surfbot/daemon.log`
- [ ] Service file: `~/Library/LaunchAgents/surfbot.plist`

System mode should resolve to:

- [ ] Config: `/Library/Application Support/surfbot/config.yaml`
- [ ] State: `/Library/Application Support/surfbot/daemon.state.json`
- [ ] Logs: `/Library/Logs/surfbot/daemon.log`
- [ ] Service file: `/Library/LaunchDaemons/surfbot.plist`

## 8. Architecture coverage

- [ ] Entire checklist executed on Apple Silicon (`uname -m` = `arm64`).
- [ ] Install/start/stop/uninstall executed on Intel (`uname -m` = `x86_64`) at least once per minor release.

## Sign-off

| Field | Value |
|-------|-------|
| Release tag | |
| Tester | |
| Date | |
| arm64 host | |
| amd64 host | |
| Issues found | |

File failures as release blockers before cutting the tag.

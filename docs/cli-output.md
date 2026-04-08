# CLI Output Conventions

This document is the source of truth for surfbot's human-readable CLI output.
Lock file: the v1 theme is frozen by `TestThemeUnchanged` in
`internal/cli/output_test.go`. Any change to the color palette requires a
roadmap entry and a deliberate fingerprint update.

## Theme (frozen v1)

| Role       | Color                   | Use                                          |
|------------|-------------------------|----------------------------------------------|
| Critical   | red + bold              | severity `CRITICAL`, fatal errors            |
| High       | red                     | severity `HIGH`                              |
| Medium     | yellow                  | severity `MEDIUM`, general warnings          |
| Low        | blue                    | severity `LOW`                               |
| Info       | faint                   | severity `INFO`, muted details               |
| Success    | Signal Green `#00E599`  | `[+]` markers, brand moments, score good    |
| Warning    | yellow                  | `[!]` markers                                |
| Error      | red + bold              | `[✗]` markers                                |
| Progress   | cyan                    | `[*]` markers                                |
| Muted      | faint                   | hints, timestamps, dividers                  |
| Bold       | bold                    | table headers, section titles                |
| Header     | bold + underline        | section headers                              |

Signal Green (`color.RGB(0, 229, 153)`) is the Surfbot brand accent and
**must not** be reused for general info — only `Success` / brand moments.

## Rules

1. **Every user-facing command constructs a `Printer`.** No raw `fmt.Print*`
   calls in `internal/cli/*.go` outside `output.go` and `root.go`'s
   `Execute()` fallback.
2. **Stdout for results, stderr for errors.** Use
   `NewPrinter(cmd.OutOrStdout())` for normal output and
   `NewPrinter(cmd.ErrOrStderr())` for errors/warnings that shouldn't
   pollute a piped `stdout`.
3. **JSON mode produces zero ANSI bytes.** When `--json` (global) or
   `-o json` (subcommand) is set, skip the Printer entirely and write raw
   JSON via `json.NewEncoder(cmd.OutOrStdout())`. The invariant is
   asserted by `TestJSONHasNoANSI`.
4. **`--no-color` and `NO_COLOR` are equivalent.** Both are wired in
   `root.go`'s `PersistentPreRunE` and set `color.NoColor = true`. Asserted
   by `TestNoColorEnv` and `TestNoColorFlag`.
5. **Severity labels are always uppercase, 8-wide, colored via
   `p.Severity(sev)`.** Never hand-roll severity coloring.
6. **Empty states always include an actionable hint** via
   `p.EmptyState(msg, hint)` or `p.ActionHint(...)`. Never print "No
   results" alone.
7. **Tables use `p.NewTable()`** (tabwriter with padding=3). Never
   instantiate `tabwriter` directly.
8. **Progress markers**: `[*]` for progress (`p.Progress`), `[+]` for
   success (`p.Success`), `[!]` for warn (`p.Warn`), `[✗]` for error
   (`p.Errorf`), `[i]` for info (`p.Info`).

## Printer API

Defined in `internal/cli/output.go`:

- `Progress(format, args...)` — cyan `[*]` line
- `Success(format, args...)` — signal-green `[+]` line
- `Warn(format, args...)` — yellow `[!]` line
- `Errorf(format, args...)` — red+bold `[✗]` line
- `Info(format, args...)` — plain `[i]` line
- `Keyf(key, format, args...)` — `key: value` with dim key
- `Bullet(format, args...)` — `  • item` line
- `Severity(sev)` — colored, padded severity label (string)
- `SeverityCount(sev, count)` — `CRITICAL  3` tally line
- `SectionHeader(text)` — bold+underline section header
- `Muted(format, args...)` — dim text
- `Divider(width)` — horizontal rule
- `NewTable()` — tabwriter with consistent padding
- `EmptyState(msg, hint)` — "no results" + hint
- `ActionHint(format, args...)` — `→ next: ...` line
- `Elapsed(d)` — muted `"2m34s"` formatting for durations
- `ScoreBar(score)` — colored `0–100` bar + risk band

## Adding a new command — checklist

1. Construct `p := NewPrinter(cmd.OutOrStdout())` in `RunE`.
2. For errors/warnings, use `NewPrinter(cmd.ErrOrStderr())`.
3. If the command supports `--json`, guard it: write raw JSON via
   `json.NewEncoder`, never through the Printer.
4. Replace any ad-hoc table with `p.NewTable()`.
5. Add an `EmptyState(msg, hint)` for the zero-result path, with an
   actionable follow-up command in the hint.
6. Use `p.Severity(sev)` for any severity rendering.
7. Add a test in `conformance_test.go` if the command takes `--json` so
   the ANSI-free invariant covers it.
8. Run `go test ./internal/cli/ -race` and `golangci-lint run ./...`.

## JSON mode

When `--json` is set, commands must:
- Skip constructing a stdout `Printer`.
- Write the payload with `json.NewEncoder(cmd.OutOrStdout())`.
- Emit errors to stderr only (Cobra's default error handling is fine).
- Never print banners, progress markers, or empty-state hints.

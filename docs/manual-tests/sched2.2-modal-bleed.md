# Manual smoke test — SPEC-SCHED2.2 (schedule edit-modal state bleed)

**Spec:** `surfbot-strategy/specs/SPEC-SCHED2.2-modal-bleed.md`
**Linear:** [SUR-256](https://linear.app/surfbot/issue/SUR-256)

The bug: opening the edit modal for schedule A, closing it, then opening
the edit modal for schedule B used to show A's field values. Saving B
silently overwrote it with A's RRULE / target / template / etc.

The fix is a JS-only change in `internal/webui/static/js/pages/schedules.js`
(`hydrateScheduleForm` + `onClose` cleanup). There is no Go change and no
new automated test — this doc is the smoke check.

## Setup

```sh
make build
./surfbot ui &
# or: ./surfbot daemon  + open the UI port in a browser
```

Create two targets and two schedules with distinct values so the bleed
is visually obvious:

```sh
./surfbot target add foo.test.local
./surfbot target add bar.test.local
```

In the UI (Schedules → New schedule), create:

- **A**: name `alpha`, target = `foo.test.local`, RRULE = `FREQ=DAILY`,
  DTSTART = any future minute, timezone `UTC`.
- **B**: name `beta`, target = `bar.test.local`, RRULE = `FREQ=WEEKLY`,
  DTSTART = any future minute, timezone `UTC`.

## Repro of the bug (pre-fix; for historical reference)

1. Schedules → click row A → **Edit**.
2. Verify form shows `name=alpha`, `rrule=FREQ=DAILY`.
3. Close the modal — try all three dismiss paths: Esc, backdrop click,
   Cancel button. The bug reproduced under all three.
4. Schedules → click row B → **Edit**.
5. **Bug:** form shows `name=alpha`, `rrule=FREQ=DAILY` (bleed from A).
6. Click **Save**. The B row in the list now has A's RRULE and name.

## Verification (post-fix)

Repeat steps 1–5. At step 5, every field reflects B's server state:
`name=beta`, `rrule=FREQ=WEEKLY`, target = `bar.test.local`.

Run all three dismiss paths (Esc, backdrop, Cancel) between A and B and
confirm none of them leak state.

Bonus checks:

- A → Edit → close → **New schedule**: every field is blank except
  `Timezone`, which defaults to `UTC`.
- New schedule → fill in fields → close → **New schedule** again:
  blank again (no state from the previous create attempt).
- Edit B → change `name` to `betaprime` → Save → reopen Edit on B:
  shows `betaprime` (server is the source of truth, not the previous
  modal session).

## What's deferred

- **Visual diff indicator + reset control** (R3 of the original Linear
  ticket — defensive UX, not a bug fix). Tracked separately under UX
  hardening.
- **Audit of `templates.js` / `blackouts.js` modals** for the same
  pattern. They use the same `value=` attribute mechanism and are
  candidates for the same fix if the bug reproduces there. Not in
  scope for this ticket.
- **JS unit-test infrastructure** (jsdom / Vitest / Playwright). No
  framework exists in the repo today; adding one is its own ticket.

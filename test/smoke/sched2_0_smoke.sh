#!/usr/bin/env bash
# SPEC-SCHED2.0 smoke test — automated validation of SUR-259 / SUR-254.
#
# Usage:
#   ./test/smoke/sched2_0_smoke.sh
#
# Optional env:
#   SURFBOT_BIN  Path to surfbot binary (default: ./bin/surfbot, builds if missing)
#   SMOKE_OUT    Output dir for evidence (default: /tmp/sched2_0_smoke_<ts>)
#   SMOKE_PORT   HTTP port for ui (default: 8479, avoids prod 8470)
#
# What it validates (3 escenarios from SUR-259):
#   1. `surfbot ui` boots scheduler in-process; a future schedule fires
#      automatically (without a separate `surfbot daemon`).
#   2. Ad-hoc "Run scan now" via API responds synchronously, scan row created.
#   3. Second `surfbot daemon run` while ui is alive exits non-zero with a
#      lock contention message mentioning the holder.
#
# The script does NOT verify findings persistence — that requires the full
# scanner toolchain (nmap, nuclei, etc.). What we verify is the SCHED2.0
# mechanic: dispatch happens, lock works, run-now is sync.

set -uo pipefail

# ---------- setup ----------
TS=$(date +%Y%m%d_%H%M%S)
OUT="${SMOKE_OUT:-/tmp/sched2_0_smoke_$TS}"
PORT="${SMOKE_PORT:-8479}"
mkdir -p "$OUT"

DB="$OUT/surfbot.db"
LOG_UI="$OUT/ui.log"
LOG_DAEMON="$OUT/daemon-contention.log"
RESULTS="$OUT/results.json"

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$REPO_ROOT"

if [ -z "${SURFBOT_BIN:-}" ]; then
  SURFBOT_BIN="$REPO_ROOT/bin/surfbot"
fi

# Build if needed or stale
NEED_BUILD=false
if [ ! -x "$SURFBOT_BIN" ]; then
  NEED_BUILD=true
elif [ -n "$(find $REPO_ROOT/cmd $REPO_ROOT/internal -newer "$SURFBOT_BIN" -type f -name '*.go' 2>/dev/null | head -1)" ]; then
  NEED_BUILD=true
fi
if [ "$NEED_BUILD" = "true" ]; then
  echo "[build] surfbot binary missing or stale, building..."
  mkdir -p "$REPO_ROOT/bin"
  go build -o "$SURFBOT_BIN" ./cmd/surfbot
fi

echo "[setup]"
echo "  OUT     = $OUT"
echo "  BIN     = $SURFBOT_BIN"
echo "  DB      = $DB"
echo "  PORT    = $PORT"
echo "  GIT     = $(git rev-parse --short HEAD) ($(git branch --show-current))"
echo

# Helpers
fail_count=0
pass() { echo "  PASS: $*"; }
fail() { echo "  FAIL: $*"; fail_count=$((fail_count + 1)); }

# JSON-safe quoting helper
jq_str() { python3 -c "import json,sys; print(json.dumps(sys.argv[1]))" "$1" 2>/dev/null || echo "\"$(echo "$1" | sed 's/"/\\"/g; s/$/\\n/' | tr -d '\n')\""; }

# ---------- boot UI ----------
echo "[boot] starting surfbot ui..."
"$SURFBOT_BIN" --db "$DB" ui --port "$PORT" --bind 127.0.0.1 --no-open >"$LOG_UI" 2>&1 &
UI_PID=$!
echo "  UI_PID=$UI_PID"

cleanup() {
  set +e
  if [ -n "${UI_PID:-}" ] && kill -0 "$UI_PID" 2>/dev/null; then
    echo "[cleanup] stopping UI (pid $UI_PID)..."
    kill "$UI_PID" 2>/dev/null
    sleep 2
    kill -9 "$UI_PID" 2>/dev/null
    wait "$UI_PID" 2>/dev/null
  fi
}
trap cleanup EXIT

# Wait up to 15s for the scheduler-started log line
boot_ok=false
for i in $(seq 1 30); do
  if grep -qE "scheduler started in-process|listening on" "$LOG_UI" 2>/dev/null; then
    boot_ok=true
    break
  fi
  sleep 0.5
done

scheduler_in_process=false
if grep -qE "scheduler started in-process" "$LOG_UI" 2>/dev/null; then
  scheduler_in_process=true
  pass "scheduler started in-process (log line present)"
else
  fail "scheduler did not log 'scheduler started in-process' within 15s"
  echo "    --- ui.log tail ---"
  tail -20 "$LOG_UI" | sed 's/^/    /'
fi

if ! kill -0 "$UI_PID" 2>/dev/null; then
  fail "UI process died during boot"
  cat "$LOG_UI"
  exit 1
fi

# ---------- ESCENARIO 1: schedule futuro dispara solo ----------
echo
echo "[ESCENARIO 1] schedule futuro dispara solo"

TARGET_VAL="smoke-$TS.test.local"
TARGET_OUT="$OUT/target.txt"
"$SURFBOT_BIN" --db "$DB" target add "$TARGET_VAL" --scope external --type domain >"$TARGET_OUT" 2>&1 || true
TARGET_ID=$(grep -oE '[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}' "$TARGET_OUT" | head -1)
if [ -z "$TARGET_ID" ]; then
  TARGET_ID=$(sqlite3 "$DB" "SELECT id FROM targets WHERE value='$TARGET_VAL';" 2>/dev/null || echo "")
fi
echo "  target_id=$TARGET_ID"

if [ -z "$TARGET_ID" ]; then
  fail "could not create or find target"
else
  pass "target created"
fi

# Schedule with FREQ=MINUTELY — first occurrence is the next minute boundary
SCHED_OUT="$OUT/schedule.txt"
"$SURFBOT_BIN" --db "$DB" schedule create \
  --target "$TARGET_ID" \
  --name "smoke-sched-$TS" \
  --rrule "FREQ=MINUTELY" \
  --tzid UTC \
  >"$SCHED_OUT" 2>&1 || true
SCHED_ID=$(grep -oE '[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}' "$SCHED_OUT" | head -1)
echo "  schedule_id=$SCHED_ID"
if [ -z "$SCHED_ID" ]; then
  fail "could not create schedule — see $SCHED_OUT"
  cat "$SCHED_OUT" | sed 's/^/    /'
else
  pass "schedule created"
fi

# Wait up to 90s for first dispatch
echo "  waiting up to 90s for dispatch (FREQ=MINUTELY hits next wall-clock minute)..."
scan_count=0
dispatch_observed=false
for i in $(seq 1 18); do
  sleep 5
  scan_count=$(sqlite3 "$DB" "SELECT COUNT(*) FROM scans WHERE target_id='$TARGET_ID';" 2>/dev/null || echo 0)
  if [ "${scan_count:-0}" -gt 0 ]; then
    dispatch_observed=true
    break
  fi
done
if [ "$dispatch_observed" = "true" ]; then
  pass "scan dispatched (count=$scan_count)"
else
  fail "no scan dispatched after 90s"
fi

# Capture log lines about dispatch
DISPATCH_LOGS=$(grep -iE "(dispatch|scan.*started|scheduler.*tick)" "$LOG_UI" 2>/dev/null | tail -5 || echo "")

# ---------- ESCENARIO 2: run-now sync ----------
echo
echo "[ESCENARIO 2] run-now sincrónico"

ADHOC_OUT="$OUT/adhoc.txt"
START_NS=$(date +%s%N)
"$SURFBOT_BIN" --db "$DB" scan adhoc \
  --target "$TARGET_ID" \
  --daemon-url "http://127.0.0.1:$PORT" \
  --reason "smoke-sched2.0-$TS" \
  >"$ADHOC_OUT" 2>&1 &
ADHOC_PID=$!
# Cap at 60s
adhoc_done=false
for i in $(seq 1 60); do
  if ! kill -0 "$ADHOC_PID" 2>/dev/null; then
    adhoc_done=true
    break
  fi
  sleep 1
done
END_NS=$(date +%s%N)
ADHOC_MS=$(( (END_NS - START_NS) / 1000000 ))

if [ "$adhoc_done" = "false" ]; then
  kill "$ADHOC_PID" 2>/dev/null
  fail "scan adhoc did not return within 60s (queue limbo?)"
fi

wait "$ADHOC_PID" 2>/dev/null
ADHOC_RC=$?

if [ "$adhoc_done" = "true" ] && [ "$ADHOC_MS" -lt 60000 ]; then
  pass "adhoc returned in ${ADHOC_MS}ms (rc=$ADHOC_RC)"
fi

# Verify a new scan row was created (compared to scenario 1)
sleep 2
total_scans=$(sqlite3 "$DB" "SELECT COUNT(*) FROM scans WHERE target_id='$TARGET_ID';" 2>/dev/null || echo 0)
if [ "$total_scans" -gt "$scan_count" ]; then
  pass "new scan row from adhoc (now $total_scans, was $scan_count)"
else
  fail "no new scan row after adhoc (still $total_scans)"
fi

# Capture transition trace for the adhoc scan
SCAN_TRACE=$(sqlite3 "$DB" "SELECT id, status, started_at, finished_at FROM scans WHERE target_id='$TARGET_ID' ORDER BY rowid DESC LIMIT 3;" 2>/dev/null || echo "")

# ---------- ESCENARIO 3: lock contention ----------
echo
echo "[ESCENARIO 3] daemon + ui simultáneos no double-dispatch"

# Try `surfbot daemon run` against the same DB while UI is alive
"$SURFBOT_BIN" --db "$DB" daemon run >"$LOG_DAEMON" 2>&1 &
DAEMON_PID=$!
# It should fail fast (lock held); give it 10s
daemon_done=false
for i in $(seq 1 10); do
  if ! kill -0 "$DAEMON_PID" 2>/dev/null; then
    daemon_done=true
    break
  fi
  sleep 1
done

if [ "$daemon_done" = "false" ]; then
  kill "$DAEMON_PID" 2>/dev/null
  fail "daemon run did not exit within 10s — lock contention not detected (process may have stolen the lock)"
  DAEMON_EXIT=124
else
  wait "$DAEMON_PID" 2>/dev/null
  DAEMON_EXIT=$?
fi

if [ "$DAEMON_EXIT" -ne 0 ]; then
  pass "daemon run exited non-zero (rc=$DAEMON_EXIT) as expected"
else
  fail "daemon run exited 0 — lock did not block second process"
fi

LOCK_MSG=$(grep -iE "(scheduler already running|lock|pid)" "$LOG_DAEMON" 2>/dev/null | head -3 || echo "")
if [ -n "$LOCK_MSG" ]; then
  pass "daemon log mentions lock contention: $(echo "$LOCK_MSG" | head -1)"
else
  fail "daemon log does not mention lock — error message unclear"
fi

# Verify UI process still alive
if kill -0 "$UI_PID" 2>/dev/null; then
  pass "UI process still healthy after contention attempt"
else
  fail "UI process died during contention test"
fi

# ---------- write results ----------
echo
echo "[results]"
{
  echo "{"
  echo "  \"ts\": \"$TS\","
  echo "  \"git_sha\": \"$(git rev-parse --short HEAD)\","
  echo "  \"git_branch\": \"$(git branch --show-current)\","
  echo "  \"out_dir\": \"$OUT\","
  echo "  \"port\": $PORT,"
  echo "  \"failures\": $fail_count,"
  echo "  \"scenario_1\": {"
  echo "    \"scheduler_started_in_process\": $scheduler_in_process,"
  echo "    \"dispatch_observed\": $dispatch_observed,"
  echo "    \"scan_count\": $scan_count,"
  echo "    \"target_id\": \"$TARGET_ID\","
  echo "    \"schedule_id\": \"$SCHED_ID\""
  echo "  },"
  echo "  \"scenario_2\": {"
  echo "    \"adhoc_response_ms\": $ADHOC_MS,"
  echo "    \"adhoc_rc\": $ADHOC_RC,"
  echo "    \"adhoc_done_within_60s\": $adhoc_done,"
  echo "    \"total_scans_after\": $total_scans"
  echo "  },"
  echo "  \"scenario_3\": {"
  echo "    \"daemon_exit_code\": $DAEMON_EXIT,"
  echo "    \"daemon_exit_nonzero\": $([ $DAEMON_EXIT -ne 0 ] && echo true || echo false),"
  echo "    \"lock_message_present\": $([ -n "$LOCK_MSG" ] && echo true || echo false),"
  echo "    \"ui_still_alive\": $(kill -0 $UI_PID 2>/dev/null && echo true || echo false)"
  echo "  }"
  echo "}"
} > "$RESULTS"

cat "$RESULTS"
echo
echo "[evidence]"
echo "  $OUT/ui.log                ($(wc -l < "$LOG_UI") lines)"
echo "  $OUT/daemon-contention.log ($(wc -l < "$LOG_DAEMON") lines)"
echo "  $OUT/surfbot.db            ($(stat -c%s "$DB" 2>/dev/null || stat -f%z "$DB" 2>/dev/null) bytes)"
echo "  $OUT/results.json"
echo
echo "[scan trace]"
echo "$SCAN_TRACE"
echo
echo "[dispatch logs]"
echo "$DISPATCH_LOGS"
echo
if [ "$fail_count" -eq 0 ]; then
  echo "OVERALL: PASS"
  exit 0
else
  echo "OVERALL: FAIL ($fail_count failure(s))"
  exit 1
fi

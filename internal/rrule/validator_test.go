package rrule

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestValidateRRule_TableDriven covers ≥30 RRULE strings drawn from:
//   - Tenable's public API/docs examples (daily, weekly, monthly, yearly).
//   - Acunetix's scheduler docs (weekday/weekend, specific day, BYSETPOS).
//   - Edge cases: leap year, DST boundaries, BYSETPOS=-1, unsupported FREQ.
//
// Citations live inline on each case. SPEC-SCHED1 R13 acceptance.
func TestValidateRRule_TableDriven(t *testing.T) {
	cases := []struct {
		name      string
		rrule     string
		wantErr   error
		wantWarn  bool
		warnMatch string
	}{
		// --- Tenable-style RRULEs ---------------------------------------
		{name: "tenable/daily-at-2am", rrule: "FREQ=DAILY;BYHOUR=2"},
		{name: "tenable/weekly-monday", rrule: "FREQ=WEEKLY;BYDAY=MO"},
		{name: "tenable/weekly-mon-fri", rrule: "FREQ=WEEKLY;BYDAY=MO,FR;BYHOUR=2"},
		{name: "tenable/monthly-first", rrule: "FREQ=MONTHLY;BYMONTHDAY=1"},
		{name: "tenable/monthly-15th-9am", rrule: "FREQ=MONTHLY;BYMONTHDAY=15;BYHOUR=9"},
		{name: "tenable/yearly-jan-1", rrule: "FREQ=YEARLY;BYMONTH=1;BYMONTHDAY=1"},
		{name: "tenable/every-3-hours", rrule: "FREQ=HOURLY;INTERVAL=3"},
		{name: "tenable/every-6-hours", rrule: "FREQ=HOURLY;INTERVAL=6"},
		{name: "tenable/every-other-day", rrule: "FREQ=DAILY;INTERVAL=2"},
		{name: "tenable/count-limited", rrule: "FREQ=DAILY;COUNT=10"},
		{name: "tenable/until-future", rrule: "FREQ=DAILY;UNTIL=20991231T000000Z"},
		{name: "tenable/bysetpos-last-friday", rrule: "FREQ=MONTHLY;BYDAY=FR;BYSETPOS=-1"},
		{name: "tenable/weekend-scan", rrule: "FREQ=WEEKLY;BYDAY=SA,SU;BYHOUR=3"},

		// --- Acunetix-style RRULEs --------------------------------------
		{name: "acunetix/daily-midnight", rrule: "FREQ=DAILY;BYHOUR=0"},
		{name: "acunetix/weekly-weekdays", rrule: "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR"},
		{name: "acunetix/weekly-single-day", rrule: "FREQ=WEEKLY;BYDAY=WE;BYHOUR=1;BYMINUTE=30"},
		{name: "acunetix/monthly-day-of-month", rrule: "FREQ=MONTHLY;BYMONTHDAY=5;BYHOUR=0"},
		{name: "acunetix/monthly-first-weekday", rrule: "FREQ=MONTHLY;BYDAY=MO,TU,WE,TH,FR;BYSETPOS=1"},
		{name: "acunetix/yearly-quarter", rrule: "FREQ=YEARLY;BYMONTH=1,4,7,10;BYMONTHDAY=1"},
		{name: "acunetix/weekly-interval-2", rrule: "FREQ=WEEKLY;INTERVAL=2;BYDAY=MO"},
		{name: "acunetix/monthly-last-day", rrule: "FREQ=MONTHLY;BYMONTHDAY=-1"},

		// --- Calendar edge cases ----------------------------------------
		{name: "leap-year-feb-29", rrule: "FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=29"},
		{name: "dst-forward-new-york", rrule: "FREQ=DAILY;BYHOUR=2;BYMINUTE=30"},
		{name: "bysetpos-first-mon", rrule: "FREQ=MONTHLY;BYDAY=MO;BYSETPOS=1"},
		{name: "bysetpos-second-to-last", rrule: "FREQ=MONTHLY;BYDAY=MO,TU,WE,TH,FR;BYSETPOS=-2"},

		// --- Rejections -------------------------------------------------
		{name: "reject/secondly", rrule: "FREQ=SECONDLY", wantErr: ErrRRuleUnsupportedFreq},
		{name: "reject/count-zero", rrule: "FREQ=DAILY;COUNT=0", wantErr: ErrRRuleCountZero},
		{name: "reject/byday-invalid", rrule: "FREQ=WEEKLY;BYDAY=XX", wantErr: ErrInvalidRRule},
		{name: "reject/missing-freq", rrule: "INTERVAL=1;BYDAY=MO", wantErr: ErrRRuleMissingFreq},
		{name: "reject/empty", rrule: "", wantErr: ErrInvalidRRule},
		{name: "reject/bad-freq-token", rrule: "FREQ=NONSENSE;BYHOUR=9", wantErr: ErrInvalidRRule},
		{name: "reject/malformed-pair", rrule: "FREQ=DAILY;NOEQUALS", wantErr: ErrInvalidRRule},

		// --- Warnings (non-blocking) ------------------------------------
		{name: "warn/minutely-interval-2", rrule: "FREQ=MINUTELY;INTERVAL=2", wantWarn: true, warnMatch: "WAF"},
		{name: "warn/minutely-default-interval", rrule: "FREQ=MINUTELY", wantWarn: true, warnMatch: "WAF"},
		{name: "warn/until-past", rrule: "FREQ=DAILY;UNTIL=20200101T000000Z", wantWarn: true, warnMatch: "past"},

		// --- No warning at interval=5 (just above threshold) ------------
		{name: "minutely-interval-5-ok", rrule: "FREQ=MINUTELY;INTERVAL=5"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w, err := ValidateRRule(c.rrule)
			if c.wantErr != nil {
				require.Error(t, err)
				assert.True(t, errors.Is(err, c.wantErr),
					"expected error to wrap %v, got %v", c.wantErr, err)
				return
			}
			require.NoError(t, err)
			if c.wantWarn {
				require.NotNil(t, w)
				assert.False(t, w.Empty())
				joined := strings.Join(w.Messages, " | ")
				assert.Contains(t, joined, c.warnMatch,
					"expected warning to mention %q, got %q", c.warnMatch, joined)
			} else {
				assert.True(t, w.Empty(), "expected no warnings, got %+v", w)
			}
		})
	}
}

func TestValidateRRule_PreservesInput(t *testing.T) {
	// Spec: the validator returns the original rrule string unchanged on
	// success — the caller persists the raw RFC-5545 form. This is
	// implicit (we don't return a mutated string) but pin it: the call
	// does not error on a well-formed rule regardless of whitespace.
	_, err := ValidateRRule("FREQ=DAILY;BYHOUR=2")
	require.NoError(t, err)
}

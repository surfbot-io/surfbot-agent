// Package rrule validates RFC-5545 RRULE strings for use by the schedule
// storage layer and the CLI. It is its own package so internal/storage
// can call the validator without creating an import cycle with
// internal/cli.
package rrule

import (
	"errors"
	"fmt"
	"strings"
	"time"

	extrrule "github.com/teambition/rrule-go"
)

// ErrInvalidRRule wraps every failure returned by ValidateRRule. Callers
// that want to distinguish semantic causes should errors.Is against the
// sentinels below.
var (
	ErrInvalidRRule         = errors.New("invalid rrule")
	ErrRRuleUnsupportedFreq = errors.New("unsupported FREQ")
	ErrRRuleCountZero       = errors.New("COUNT=0")
	ErrRRuleMissingFreq     = errors.New("missing FREQ")
)

// Warnings is a non-blocking result returned alongside a valid RRULE.
// Callers persist the raw rrule string regardless of warnings; the
// warnings are surfaced in the UI / CLI for operator awareness.
type Warnings struct {
	Messages []string
}

// Empty reports whether the warning set is empty.
func (w *Warnings) Empty() bool {
	return w == nil || len(w.Messages) == 0
}

// ValidateRRule parses an RFC-5545 RRULE string and enforces
// SPEC-SCHED1 R13 acceptance rules:
//
//   - Reject: SECONDLY, COUNT=0, missing FREQ, syntactically invalid.
//   - Warn:   MINUTELY with INTERVAL<5; UNTIL in the past.
//
// On success the returned Warnings value may be nil (no warnings) or
// non-nil with one or more messages. The caller persists the raw
// rfcString unchanged — we never rewrite it.
func ValidateRRule(rfcString string) (*Warnings, error) {
	if strings.TrimSpace(rfcString) == "" {
		return nil, fmt.Errorf("%w: empty string", ErrInvalidRRule)
	}

	// Check FREQ presence before parsing so we can surface the precise
	// ErrRRuleMissingFreq sentinel rather than wrapping the library's
	// generic "RRULE property FREQ is required" inside ErrInvalidRRule.
	if !strings.Contains(strings.ToUpper(rfcString), "FREQ=") {
		return nil, fmt.Errorf("%w", ErrRRuleMissingFreq)
	}

	opt, err := extrrule.StrToROption(rfcString)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidRRule, err)
	}

	if opt.Freq == extrrule.SECONDLY {
		return nil, fmt.Errorf("%w: SECONDLY is not supported (security noise)", ErrRRuleUnsupportedFreq)
	}

	// extrrule.StrToROption parses COUNT as int; COUNT=0 is meaningless
	// (schedule can never fire) and the library does not reject it.
	// We also reject a literal "COUNT=0" token to be explicit for
	// operators — opt.Count can be 0 when the token is absent, so we
	// check the string.
	if containsToken(rfcString, "COUNT=0") {
		return nil, fmt.Errorf("%w", ErrRRuleCountZero)
	}

	// Confirm the rule is constructible — this surfaces invalid BYDAY
	// tokens like "XX" that StrToROption may accept but NewRRule
	// rejects.
	if _, err := extrrule.NewRRule(*opt); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidRRule, err)
	}

	var w *Warnings
	addWarning := func(msg string) {
		if w == nil {
			w = &Warnings{}
		}
		w.Messages = append(w.Messages, msg)
	}

	if opt.Freq == extrrule.MINUTELY {
		interval := opt.Interval
		if interval == 0 {
			interval = 1
		}
		if interval < 5 {
			addWarning("sub-5-minute cadence will likely trigger WAFs")
		}
	}

	if !opt.Until.IsZero() && opt.Until.Before(time.Now()) {
		addWarning("UNTIL is in the past; schedule will never fire")
	}

	return w, nil
}

// containsToken does a case-insensitive scan for `token` as a ;-delimited
// element of the RRULE body. It avoids matching substrings inside, e.g.,
// "COUNT=0" appearing as a value in a later key/value pair.
func containsToken(rfc, token string) bool {
	upper := strings.ToUpper(rfc)
	token = strings.ToUpper(token)
	for _, part := range strings.Split(upper, ";") {
		if strings.TrimSpace(part) == token {
			return true
		}
	}
	return false
}

package detection

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBreakerDoesNotTripBeforeWindowFills(t *testing.T) {
	b := newBreaker(20)
	// 49 timeouts do not fire the breaker; we need a full window before
	// the ratio is meaningful.
	for i := 0; i < 49; i++ {
		event, _, _, _ := b.recordResult(false, true)
		assert.Equal(t, "", event)
	}
	assert.Equal(t, 20, b.inflightCap())
	assert.Equal(t, 0, b.halvingsCount())
}

func TestBreakerHalvesOnTimeoutFlood(t *testing.T) {
	b := newBreaker(20)
	// Fill the window with 80% timeouts — breaker halves on the 50th entry.
	var haved bool
	for i := 0; i < 50; i++ {
		timedOut := i < 40 // 40 timeouts + 10 successes = 80% ratio
		event, _, _, _ := b.recordResult(!timedOut, timedOut)
		if event == "halved" {
			haved = true
		}
	}
	assert.True(t, haved, "expected at least one halving on timeout flood")
	assert.Equal(t, 10, b.inflightCap(), "20 → 10 after first halving")
	assert.GreaterOrEqual(t, b.halvingsCount(), 1)
}

func TestBreakerHalvingFloorsAt5(t *testing.T) {
	b := newBreaker(20)
	// Fill with 100% timeouts repeatedly — each window should halve until
	// we hit the floor of 5.
	for round := 0; round < 10; round++ {
		for i := 0; i < 50; i++ {
			b.recordResult(false, true)
		}
	}
	assert.Equal(t, 5, b.inflightCap(), "floor at 5 per SPEC-QA2 R3")
}

func TestBreakerRestoresOnCooperativeTraffic(t *testing.T) {
	b := newBreaker(20)
	// First: flood with timeouts to trigger halving.
	for i := 0; i < 50; i++ {
		b.recordResult(false, true)
	}
	assert.Less(t, b.inflightCap(), 20)

	// Now: fill with all-success to get ratio below 25%.
	for i := 0; i < 50; i++ {
		b.recordResult(true, false)
	}
	assert.Equal(t, 20, b.inflightCap(), "restored to original after recovery")
}

func TestBreakerDoesNotHalveBelowFiveThreshold(t *testing.T) {
	b := newBreaker(20)
	// Drive cap to 5 via repeated halvings.
	for round := 0; round < 5; round++ {
		for i := 0; i < 50; i++ {
			b.recordResult(false, true)
		}
	}
	assert.Equal(t, 5, b.inflightCap())
	halvingsBefore := b.halvingsCount()

	// More timeouts at cap=5 should NOT increment halvings further — we've
	// bottomed out.
	for i := 0; i < 100; i++ {
		b.recordResult(false, true)
	}
	assert.Equal(t, 5, b.inflightCap())
	assert.Equal(t, halvingsBefore, b.halvingsCount(), "no extra halvings once at floor")
}

func TestBreakerRSTDoesNotCountAsTimeout(t *testing.T) {
	b := newBreaker(20)
	// 50 RST-style completed failures (success=false, timedOut=false) are
	// not a rate-limit signal — they're just closed ports. The breaker
	// must not react.
	for i := 0; i < 50; i++ {
		event, _, _, _ := b.recordResult(false, false)
		assert.Equal(t, "", event, "RST should never trigger halving")
	}
	assert.Equal(t, 20, b.inflightCap())
	assert.Equal(t, 0, b.halvingsCount())
}

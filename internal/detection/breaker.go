package detection

import "sync"

// breakerWindow is the ring-buffer size used for the timeout-ratio calculation.
// 50 is a reasonable tradeoff for top-100 scans — one full window per IP
// roughly — and small enough that the breaker reacts within a few seconds.
// Kept unexported; not user-tunable.
const breakerWindow = 50

// breakerEntry records the outcome of one completed connection attempt
// (after any retry). A "success" is a connection that completed the 3-way
// handshake; "timedOut" distinguishes a stall (rate-limit) from a clean
// RST/refused, which is not a breaker signal.
type breakerEntry struct {
	success  bool
	timedOut bool
}

// breaker halves the effective concurrency when the timeout ratio over the
// last breakerWindow completed attempts crosses 50%, and restores it when
// the ratio drops below 25%. The adjustment is expressed as inflightCap,
// which callers consult before consuming a semaphore token.
type breaker struct {
	mu       sync.Mutex
	ring     [breakerWindow]breakerEntry
	filled   int
	pos      int
	original int
	cap      int
	halvings int
}

// newBreaker returns a breaker with the given baseline concurrency.
func newBreaker(original int) *breaker {
	if original < 1 {
		original = 1
	}
	return &breaker{original: original, cap: original}
}

// recordResult appends one attempt outcome and adjusts the cap per the rules
// in SPEC-QA2 R3. Returns the event that fired, if any: "halved", "restored",
// or "" for no state change. The old and new caps are returned alongside so
// callers can log them.
func (b *breaker) recordResult(success, timedOut bool) (event string, oldCap, newCap int, timeoutRatio float64) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.ring[b.pos] = breakerEntry{success: success, timedOut: timedOut}
	b.pos = (b.pos + 1) % breakerWindow
	if b.filled < breakerWindow {
		b.filled++
	}

	// Wait for a full window before reacting. A 50-entry window on a top-100
	// scan fills within the first second or two; making decisions on partial
	// data would cause spurious halvings on the first few ports.
	if b.filled < breakerWindow {
		return "", b.cap, b.cap, 0
	}

	timeouts := 0
	for i := 0; i < b.filled; i++ {
		if b.ring[i].timedOut {
			timeouts++
		}
	}
	ratio := float64(timeouts) / float64(b.filled)

	oldCap = b.cap
	switch {
	case ratio > 0.5 && b.cap > 5:
		newCap = b.cap / 2
		if newCap < 5 {
			newCap = 5
		}
		if newCap != b.cap {
			b.cap = newCap
			b.halvings++
			return "halved", oldCap, newCap, ratio
		}
	case ratio < 0.25 && b.cap < b.original:
		newCap = b.original
		b.cap = newCap
		return "restored", oldCap, newCap, ratio
	}
	return "", b.cap, b.cap, ratio
}

// inflightCap is the current effective concurrency.
func (b *breaker) inflightCap() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.cap
}

// halvings is the count of times the breaker has reduced concurrency over the
// life of this scan. Exposed on ToolRun.Config for post-run telemetry.
func (b *breaker) halvingsCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.halvings
}

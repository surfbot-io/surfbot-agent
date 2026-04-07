package intervalsched

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// TimeOfDay is a wall-clock HH:MM value, independent of any date or
// timezone. The scheduler combines it with a *time.Location to test
// whether a moment falls inside a maintenance window.
type TimeOfDay struct {
	Hour   int
	Minute int
}

// ParseTimeOfDay parses an "HH:MM" string into a TimeOfDay.
func ParseTimeOfDay(s string) (TimeOfDay, error) {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) != 2 {
		return TimeOfDay{}, fmt.Errorf("invalid time-of-day %q: want HH:MM", s)
	}
	h, err := strconv.Atoi(parts[0])
	if err != nil || h < 0 || h > 23 {
		return TimeOfDay{}, fmt.Errorf("invalid hour in %q", s)
	}
	m, err := strconv.Atoi(parts[1])
	if err != nil || m < 0 || m > 59 {
		return TimeOfDay{}, fmt.Errorf("invalid minute in %q", s)
	}
	return TimeOfDay{Hour: h, Minute: m}, nil
}

// String renders the TimeOfDay back to "HH:MM".
func (t TimeOfDay) String() string {
	return fmt.Sprintf("%02d:%02d", t.Hour, t.Minute)
}

// MaintenanceWindow represents a recurring daily window during which the
// scheduler must not start new scans. In-flight scans are not killed.
//
// A window may cross midnight (Start=22:00, End=06:00) — Contains and
// NextOpen handle that case explicitly.
type MaintenanceWindow struct {
	Enabled bool
	Start   TimeOfDay
	End     TimeOfDay
	Loc     *time.Location
}

// Contains reports whether t (in Loc) falls inside the window.
// For a window crossing midnight, "inside" means t >= Start OR t < End.
// A disabled window contains nothing.
func (w MaintenanceWindow) Contains(t time.Time) bool {
	if !w.Enabled {
		return false
	}
	loc := w.Loc
	if loc == nil {
		loc = time.Local
	}
	local := t.In(loc)
	mins := local.Hour()*60 + local.Minute()
	startMins := w.Start.Hour*60 + w.Start.Minute
	endMins := w.End.Hour*60 + w.End.Minute
	if startMins == endMins {
		return false
	}
	if startMins < endMins {
		return mins >= startMins && mins < endMins
	}
	// Crosses midnight: window is [start, 24:00) ∪ [00:00, end).
	return mins >= startMins || mins < endMins
}

// NextOpen returns the earliest moment at or after `after` when the window
// is closed. If `after` is already outside the window it returns `after`
// unchanged. The result is computed via time.Date arithmetic so DST
// transitions are handled correctly.
func (w MaintenanceWindow) NextOpen(after time.Time) time.Time {
	if !w.Enabled || !w.Contains(after) {
		return after
	}
	loc := w.Loc
	if loc == nil {
		loc = time.Local
	}
	local := after.In(loc)
	endToday := time.Date(local.Year(), local.Month(), local.Day(),
		w.End.Hour, w.End.Minute, 0, 0, loc)

	startMins := w.Start.Hour*60 + w.Start.Minute
	endMins := w.End.Hour*60 + w.End.Minute

	if startMins < endMins {
		// Same-day window: end is later today.
		return endToday
	}
	// Crosses midnight. If we're in the post-midnight tail (mins < end),
	// the close time is endToday; otherwise it's tomorrow's end.
	mins := local.Hour()*60 + local.Minute()
	if mins < endMins {
		return endToday
	}
	tomorrowEnd := time.Date(local.Year(), local.Month(), local.Day()+1,
		w.End.Hour, w.End.Minute, 0, 0, loc)
	return tomorrowEnd
}

// String renders the window for human-friendly status output.
func (w MaintenanceWindow) String() string {
	if !w.Enabled {
		return "disabled"
	}
	loc := "local"
	if w.Loc != nil {
		loc = w.Loc.String()
	}
	return fmt.Sprintf("%s–%s %s", w.Start, w.End, loc)
}

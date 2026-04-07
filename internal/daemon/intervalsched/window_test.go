package intervalsched

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	require.NoError(t, err)
	return loc
}

func TestParseTimeOfDay(t *testing.T) {
	cases := []struct {
		in      string
		want    TimeOfDay
		wantErr bool
	}{
		{"00:00", TimeOfDay{0, 0}, false},
		{"23:59", TimeOfDay{23, 59}, false},
		{"09:30", TimeOfDay{9, 30}, false},
		{"24:00", TimeOfDay{}, true},
		{"12:60", TimeOfDay{}, true},
		{"nope", TimeOfDay{}, true},
		{"", TimeOfDay{}, true},
	}
	for _, c := range cases {
		got, err := ParseTimeOfDay(c.in)
		if c.wantErr {
			require.Error(t, err, "input %q", c.in)
			continue
		}
		require.NoError(t, err)
		require.Equal(t, c.want, got)
	}
}

func TestWindow_Contains_SameDay(t *testing.T) {
	loc := mustLoc(t, "UTC")
	w := MaintenanceWindow{Enabled: true, Start: TimeOfDay{2, 0}, End: TimeOfDay{4, 0}, Loc: loc}

	require.False(t, w.Contains(time.Date(2026, 1, 1, 1, 59, 0, 0, loc)))
	require.True(t, w.Contains(time.Date(2026, 1, 1, 2, 0, 0, 0, loc)))
	require.True(t, w.Contains(time.Date(2026, 1, 1, 3, 30, 0, 0, loc)))
	require.False(t, w.Contains(time.Date(2026, 1, 1, 4, 0, 0, 0, loc))) // exclusive end
	require.False(t, w.Contains(time.Date(2026, 1, 1, 5, 0, 0, 0, loc)))
}

func TestWindow_Contains_MidnightCross(t *testing.T) {
	loc := mustLoc(t, "UTC")
	w := MaintenanceWindow{Enabled: true, Start: TimeOfDay{22, 0}, End: TimeOfDay{6, 0}, Loc: loc}

	require.True(t, w.Contains(time.Date(2026, 1, 1, 23, 0, 0, 0, loc)))
	require.True(t, w.Contains(time.Date(2026, 1, 1, 0, 0, 0, 0, loc)))
	require.True(t, w.Contains(time.Date(2026, 1, 1, 5, 59, 0, 0, loc)))
	require.False(t, w.Contains(time.Date(2026, 1, 1, 6, 0, 0, 0, loc)))
	require.False(t, w.Contains(time.Date(2026, 1, 1, 12, 0, 0, 0, loc)))
	require.False(t, w.Contains(time.Date(2026, 1, 1, 21, 59, 0, 0, loc)))
}

func TestWindow_Disabled(t *testing.T) {
	w := MaintenanceWindow{Enabled: false}
	require.False(t, w.Contains(time.Now()))
	now := time.Now()
	require.Equal(t, now, w.NextOpen(now))
}

func TestWindow_NextOpen_SameDay(t *testing.T) {
	loc := mustLoc(t, "UTC")
	w := MaintenanceWindow{Enabled: true, Start: TimeOfDay{2, 0}, End: TimeOfDay{4, 0}, Loc: loc}
	in := time.Date(2026, 1, 1, 3, 0, 0, 0, loc)
	require.Equal(t, time.Date(2026, 1, 1, 4, 0, 0, 0, loc), w.NextOpen(in))

	out := time.Date(2026, 1, 1, 5, 0, 0, 0, loc)
	require.Equal(t, out, w.NextOpen(out))
}

func TestWindow_NextOpen_MidnightCross(t *testing.T) {
	loc := mustLoc(t, "UTC")
	w := MaintenanceWindow{Enabled: true, Start: TimeOfDay{22, 0}, End: TimeOfDay{6, 0}, Loc: loc}

	// Before midnight inside window → end is tomorrow 06:00.
	in := time.Date(2026, 1, 1, 23, 0, 0, 0, loc)
	require.Equal(t, time.Date(2026, 1, 2, 6, 0, 0, 0, loc), w.NextOpen(in))

	// After midnight inside window → end is today 06:00.
	in2 := time.Date(2026, 1, 2, 1, 0, 0, 0, loc)
	require.Equal(t, time.Date(2026, 1, 2, 6, 0, 0, 0, loc), w.NextOpen(in2))
}

// TestWindow_DST_Madrid_Spring covers the spring-forward jump on
// 2026-03-29 in Europe/Madrid where 02:00 → 03:00. A window 02:30–03:30
// straddles a non-existent local hour; the close instant must still be a
// valid wall clock the next day, computed via time.Date arithmetic.
func TestWindow_DST_Madrid_Spring(t *testing.T) {
	loc := mustLoc(t, "Europe/Madrid")
	w := MaintenanceWindow{Enabled: true, Start: TimeOfDay{2, 30}, End: TimeOfDay{3, 30}, Loc: loc}

	// 2026-03-29 03:15 local — DST has just kicked in. Whether the
	// scheduler considers this "inside" depends on the wall-clock minutes,
	// which is exactly what users configure against. We assert the
	// behavior is consistent: 03:15 wall-clock is inside [02:30, 03:30).
	inside := time.Date(2026, 3, 29, 3, 15, 0, 0, loc)
	require.True(t, w.Contains(inside))
	end := w.NextOpen(inside)
	require.Equal(t, 2026, end.Year())
	require.Equal(t, time.March, end.Month())
	require.Equal(t, 29, end.Day())
	require.Equal(t, 3, end.Hour())
	require.Equal(t, 30, end.Minute())
}

// TestWindow_DST_Madrid_Fall covers the fall-back on 2026-10-25 in
// Europe/Madrid where 03:00 → 02:00. The 02:00–02:30 wall-clock window
// occurs twice; we assert Contains/NextOpen do not panic and return a
// sensible close time.
func TestWindow_DST_Madrid_Fall(t *testing.T) {
	loc := mustLoc(t, "Europe/Madrid")
	w := MaintenanceWindow{Enabled: true, Start: TimeOfDay{2, 0}, End: TimeOfDay{2, 30}, Loc: loc}

	in := time.Date(2026, 10, 25, 2, 15, 0, 0, loc)
	require.True(t, w.Contains(in))
	end := w.NextOpen(in)
	require.Equal(t, 2, end.Hour())
	require.Equal(t, 30, end.Minute())
}

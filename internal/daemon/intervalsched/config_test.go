package intervalsched

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestConfigValidate_RejectsTooShortFull(t *testing.T) {
	c := Config{FullInterval: 30 * time.Second}
	_, err := c.Validate()
	require.Error(t, err)
}

func TestConfigValidate_RejectsTooShortQuick(t *testing.T) {
	c := Config{FullInterval: time.Hour, QuickInterval: 10 * time.Second}
	_, err := c.Validate()
	require.Error(t, err)
}

func TestConfigValidate_QuickGEFull_WarnsAndDisables(t *testing.T) {
	c := Config{FullInterval: time.Hour, QuickInterval: 2 * time.Hour}
	warn, err := c.Validate()
	require.NoError(t, err)
	require.NotEmpty(t, warn)
	require.Equal(t, time.Duration(0), c.QuickInterval)
}

func TestConfigValidate_JitterCappedAtTenPercent(t *testing.T) {
	c := Config{FullInterval: time.Hour, QuickInterval: 10 * time.Minute, Jitter: time.Hour}
	_, err := c.Validate()
	require.NoError(t, err)
	require.Equal(t, time.Minute, c.Jitter, "jitter capped to min(interval)/10")
}

func TestConfigValidate_WindowStartEqualsEnd(t *testing.T) {
	c := Config{
		FullInterval: time.Hour,
		Window: MaintenanceWindow{
			Enabled: true,
			Start:   TimeOfDay{1, 0},
			End:     TimeOfDay{1, 0},
		},
	}
	_, err := c.Validate()
	require.Error(t, err)
}

package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScheduleCmd_PrintsDeprecation(t *testing.T) {
	for _, name := range []string{"schedule", "schedule show", "schedule set foo bar"} {
		t.Run(name, func(t *testing.T) {
			rootCmd.SetArgs(strings.Fields(name))
			var out bytes.Buffer
			rootCmd.SetOut(&out)
			rootCmd.SetErr(&out)
			require.NoError(t, rootCmd.Execute())
			assert.Contains(t, out.String(), "first-class schedules")
			assert.Contains(t, out.String(), "agent-spec 3.0")
		})
	}
}

package cli

import (
	"github.com/spf13/cobra"
)

// scheduleDeprecationNotice is the message every `surfbot schedule`
// invocation prints before exiting 0. The legacy scheduler config is
// gone in SCHED1.2b; the new CLI surface lands in SCHED1.3.
const scheduleDeprecationNotice = `schedule management has moved to first-class schedules in agent-spec 3.0.
this command will be replaced in the next release.
see agent-spec for details.
`

var scheduleCmd = &cobra.Command{
	Use:   "schedule",
	Short: "(deprecated) View and configure scan schedule",
	Long:  scheduleDeprecationNotice,
	Run: func(cmd *cobra.Command, _ []string) {
		_, _ = cmd.OutOrStdout().Write([]byte(scheduleDeprecationNotice))
	},
}

var scheduleShowCmd = &cobra.Command{
	Use:   "show",
	Short: "(deprecated)",
	Run: func(cmd *cobra.Command, _ []string) {
		_, _ = cmd.OutOrStdout().Write([]byte(scheduleDeprecationNotice))
	},
}

var scheduleSetCmd = &cobra.Command{
	Use:   "set [key] [value]",
	Short: "(deprecated)",
	Args:  cobra.ArbitraryArgs,
	Run: func(cmd *cobra.Command, _ []string) {
		_, _ = cmd.OutOrStdout().Write([]byte(scheduleDeprecationNotice))
	},
}

func init() {
	scheduleCmd.AddCommand(scheduleShowCmd)
	scheduleCmd.AddCommand(scheduleSetCmd)
	rootCmd.AddCommand(scheduleCmd)
}

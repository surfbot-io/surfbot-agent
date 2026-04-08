package cli

import (
	"github.com/spf13/cobra"
)

var scoreCmd = &cobra.Command{
	Use:   "score [target]",
	Short: "Show security score",
	Args:  cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		p := NewPrinter(cmd.OutOrStdout())
		p.EmptyState("No score yet.", "Run a scan first: 'surfbot scan <domain>'.")
	},
}

func init() {
	rootCmd.AddCommand(scoreCmd)
}

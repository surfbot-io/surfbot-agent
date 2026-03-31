package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var scoreCmd = &cobra.Command{
	Use:   "score [target]",
	Short: "Show security score",
	Args:  cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("score: not yet implemented")
	},
}

func init() {
	rootCmd.AddCommand(scoreCmd)
}

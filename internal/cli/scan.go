package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var scanCmd = &cobra.Command{
	Use:   "scan [target]",
	Short: "Run detection pipeline against target",
	Args:  cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("scan: not yet implemented")
	},
}

func init() {
	scanCmd.Flags().Bool("full", false, "Run full scan")
	scanCmd.Flags().Bool("quick", false, "Run quick scan")
	scanCmd.Flags().StringSlice("tools", nil, "Comma-separated list of tools to run")
	scanCmd.Flags().StringP("output", "o", "", "Output file path")
	rootCmd.AddCommand(scanCmd)
}

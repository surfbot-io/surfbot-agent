package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var fixCmd = &cobra.Command{
	Use:   "fix <finding-id>",
	Short: "Apply remediation for a finding",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("fix: not yet implemented")
	},
}

func init() {
	fixCmd.Flags().Bool("dry-run", false, "Show what would be changed without applying")
	fixCmd.Flags().Bool("force", false, "Skip confirmation")
	fixCmd.Flags().String("tool", "", "Remediation tool to use")
	rootCmd.AddCommand(fixCmd)
}

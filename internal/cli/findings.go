package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var findingsCmd = &cobra.Command{
	Use:   "findings",
	Short: "List discovered vulnerabilities",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("findings: not yet implemented")
	},
}

func init() {
	findingsCmd.Flags().String("severity", "", "Filter by severity: critical|high|medium|low|info")
	findingsCmd.Flags().String("tool", "", "Filter by source tool")
	findingsCmd.Flags().String("status", "", "Filter by status: open|resolved|acknowledged|false_positive|ignored")
	findingsCmd.Flags().Bool("diff", false, "Show changes since last scan")
	findingsCmd.Flags().Int("limit", 0, "Max number of results")
	rootCmd.AddCommand(findingsCmd)
}

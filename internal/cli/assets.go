package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var assetsCmd = &cobra.Command{
	Use:   "assets",
	Short: "List discovered assets",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("assets: not yet implemented")
	},
}

func init() {
	assetsCmd.Flags().String("type", "", "Filter by type: subdomain|ip|port|url|technology|service")
	assetsCmd.Flags().Bool("new", false, "Show only new assets")
	assetsCmd.Flags().Bool("disappeared", false, "Show only disappeared assets")
	assetsCmd.Flags().Bool("diff", false, "Show changes since last scan")
	assetsCmd.Flags().Int("limit", 0, "Max number of results")
	rootCmd.AddCommand(assetsCmd)
}

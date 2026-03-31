package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var connectorsCmd = &cobra.Command{
	Use:   "connectors",
	Short: "Manage MCP connectors",
}

var connectorsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List configured connectors",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("connectors list: not yet implemented")
	},
}

var connectorsAddCmd = &cobra.Command{
	Use:   "add <name>",
	Short: "Add a connector",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("connectors add: not yet implemented")
	},
}

var connectorsRemoveCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Remove a connector",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("connectors remove: not yet implemented")
	},
}

func init() {
	connectorsCmd.AddCommand(connectorsListCmd, connectorsAddCmd, connectorsRemoveCmd)
	rootCmd.AddCommand(connectorsCmd)
}

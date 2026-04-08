package cli

import (
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
		p := NewPrinter(cmd.OutOrStdout())
		p.Warn("not yet implemented — see roadmap L6 (MCP connectors).")
	},
}

var connectorsAddCmd = &cobra.Command{
	Use:   "add <name>",
	Short: "Add a connector",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		p := NewPrinter(cmd.OutOrStdout())
		p.Warn("not yet implemented — see roadmap L6 (MCP connectors).")
	},
}

var connectorsRemoveCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Remove a connector",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		p := NewPrinter(cmd.OutOrStdout())
		p.Warn("not yet implemented — see roadmap L6 (MCP connectors).")
	},
}

func init() {
	connectorsCmd.AddCommand(connectorsListCmd, connectorsAddCmd, connectorsRemoveCmd)
	rootCmd.AddCommand(connectorsCmd)
}

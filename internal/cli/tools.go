package cli

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/surfbot-io/surfbot-agent/internal/detection"
)

var registry = detection.NewRegistry()

var toolsCmd = &cobra.Command{
	Use:   "tools",
	Short: "Manage detection/remediation tools",
}

var toolsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List available tools",
	Run: func(cmd *cobra.Command, args []string) {
		tools := registry.Tools()
		available := len(registry.AvailableTools())

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "TOOL\tPHASE\tAVAILABLE\tTYPE")
		for _, t := range tools {
			avail := "yes"
			if !t.Available() {
				avail = "no"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", t.Name(), t.Phase(), avail, t.Kind())
		}
		w.Flush()
		fmt.Printf("\n%d/%d tools available\n", available, len(tools))
	},
}

var toolsEnableCmd = &cobra.Command{
	Use:   "enable <name>",
	Short: "Enable a tool",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("tools enable: not yet implemented")
	},
}

var toolsDisableCmd = &cobra.Command{
	Use:   "disable <name>",
	Short: "Disable a tool",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("tools disable: not yet implemented")
	},
}

func init() {
	toolsCmd.AddCommand(toolsListCmd, toolsEnableCmd, toolsDisableCmd)
	rootCmd.AddCommand(toolsCmd)
}

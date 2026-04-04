package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/surfbot-io/surfbot-agent/internal/detection"
)

var registry = detection.NewRegistry()

var toolsCmd = &cobra.Command{
	Use:   "tools",
	Short: "Manage detection/remediation tools",
}

// ToolInfo is the JSON representation of a detection tool's metadata.
type ToolInfo struct {
	Name        string   `json:"name"`
	Command     string   `json:"command"`
	Phase       string   `json:"phase"`
	Kind        string   `json:"kind"`
	Available   bool     `json:"available"`
	Description string   `json:"description"`
	InputType   string   `json:"input_type"`
	OutputTypes []string `json:"output_types"`
}

var toolsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List available tools",
	RunE: func(cmd *cobra.Command, args []string) error {
		tools := registry.Tools()
		available := len(registry.AvailableTools())

		outputFlag, _ := cmd.Flags().GetString("output")

		var infos []ToolInfo
		for _, t := range tools {
			infos = append(infos, ToolInfo{
				Name:        t.Name(),
				Command:     t.Command(),
				Phase:       t.Phase(),
				Kind:        string(t.Kind()),
				Available:   t.Available(),
				Description: t.Description(),
				InputType:   t.InputType(),
				OutputTypes: t.OutputTypes(),
			})
		}

		if jsonOut || outputFlag == "json" {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(map[string]interface{}{"tools": infos})
		}

		out := cmd.OutOrStdout()
		p := NewPrinter(out)

		if len(infos) == 0 {
			p.EmptyState("No detection tools registered.",
				"This is unexpected — please report a bug.")
			return nil
		}

		w := p.NewTable()
		p.Theme.Bold.Fprintln(w, "NAME\tCOMMAND\tPHASE\tINPUT\tOUTPUT\tSTATUS")
		p.Divider(70)
		for _, t := range infos {
			var status string
			if t.Available {
				status = p.Theme.Success.Sprint("✓ available")
			} else {
				status = p.Theme.Error.Sprint("✗ unavailable")
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
				t.Name, t.Command, t.Phase, t.InputType,
				strings.Join(t.OutputTypes, ","), status)
		}
		w.Flush()
		fmt.Fprintf(out, "\n%d/%d tools available\n", available, len(tools))
		return nil
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
	toolsListCmd.Flags().StringP("output", "o", "", "Output format: json or text (default text)")
	toolsCmd.AddCommand(toolsListCmd, toolsEnableCmd, toolsDisableCmd)
	rootCmd.AddCommand(toolsCmd)

	// Auto-register atomic tool commands from the detection registry
	for _, tool := range registry.Tools() {
		rootCmd.AddCommand(buildToolCommand(tool))
	}
}

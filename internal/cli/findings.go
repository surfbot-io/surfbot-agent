package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/surfbot-io/surfbot-agent/internal/model"
	"github.com/surfbot-io/surfbot-agent/internal/storage"
)

var findingsCmd = &cobra.Command{
	Use:   "findings",
	Short: "Manage discovered vulnerabilities",
}

var findingsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List discovered vulnerabilities",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()

		sev, _ := cmd.Flags().GetString("severity")
		tool, _ := cmd.Flags().GetString("tool")
		status, _ := cmd.Flags().GetString("status")
		scanID, _ := cmd.Flags().GetString("scan")
		limit, _ := cmd.Flags().GetInt("limit")
		if limit <= 0 {
			limit = 50
		}

		opts := storage.FindingListOptions{
			Severity:   model.Severity(sev),
			SourceTool: tool,
			Status:     model.FindingStatus(status),
			ScanID:     scanID,
			Limit:      limit,
		}

		findings, err := store.ListFindings(ctx, opts)
		if err != nil {
			return fmt.Errorf("listing findings: %w", err)
		}

		if len(findings) == 0 {
			fmt.Println("No findings found.")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tSEVERITY\tTITLE\tTOOL\tSTATUS")
		for _, f := range findings {
			short := f.ID
			if len(short) > 8 {
				short = short[:8]
			}
			title := f.Title
			if len(title) > 60 {
				title = title[:57] + "..."
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
				short,
				strings.ToUpper(string(f.Severity)),
				title,
				f.SourceTool,
				f.Status,
			)
		}
		w.Flush()
		fmt.Printf("\nShowing %d findings. Use --limit to see more.\n", len(findings))
		fmt.Println("Use `surfbot findings show <id>` for full details.")

		return nil
	},
}

var findingsShowCmd = &cobra.Command{
	Use:   "show <id>",
	Short: "Show full details of a finding",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		id := args[0]

		// Search by prefix if short ID given
		findings, err := store.ListFindings(ctx, storage.FindingListOptions{Limit: 500})
		if err != nil {
			return fmt.Errorf("listing findings: %w", err)
		}

		var found *model.Finding
		for i := range findings {
			if findings[i].ID == id || strings.HasPrefix(findings[i].ID, id) {
				found = &findings[i]
				break
			}
		}
		if found == nil {
			return fmt.Errorf("finding not found: %s", id)
		}

		fmt.Printf("Finding: %s\n", found.ID)
		fmt.Printf("Severity: %s\n", strings.ToUpper(string(found.Severity)))
		fmt.Printf("Title: %s\n", found.Title)
		fmt.Printf("Template: %s (%s)\n", found.TemplateID, found.TemplateName)
		fmt.Printf("Tool: %s\n", found.SourceTool)
		fmt.Printf("Status: %s\n", found.Status)
		fmt.Printf("Confidence: %.0f%%\n", found.Confidence)
		if found.CVE != "" {
			fmt.Printf("CVE: %s\n", found.CVE)
		}
		if found.CVSS > 0 {
			fmt.Printf("CVSS: %.1f\n", found.CVSS)
		}
		if found.Description != "" {
			fmt.Printf("\nDescription:\n  %s\n", found.Description)
		}
		if found.Evidence != "" {
			fmt.Printf("\nEvidence:\n  %s\n", found.Evidence)
		}
		if found.Remediation != "" {
			fmt.Printf("\nRemediation:\n  %s\n", found.Remediation)
		}
		if len(found.References) > 0 {
			fmt.Println("\nReferences:")
			for _, ref := range found.References {
				fmt.Printf("  - %s\n", ref)
			}
		}
		fmt.Printf("\nFirst seen: %s\n", found.FirstSeen.Format("2006-01-02 15:04:05"))
		fmt.Printf("Last seen:  %s\n", found.LastSeen.Format("2006-01-02 15:04:05"))

		return nil
	},
}

func init() {
	findingsListCmd.Flags().String("severity", "", "Filter by severity: critical|high|medium|low|info")
	findingsListCmd.Flags().String("tool", "", "Filter by source tool")
	findingsListCmd.Flags().String("status", "", "Filter by status: open|resolved|acknowledged|false_positive|ignored")
	findingsListCmd.Flags().String("scan", "", "Filter by scan ID")
	findingsListCmd.Flags().Int("limit", 50, "Max number of results")

	findingsCmd.AddCommand(findingsListCmd, findingsShowCmd)
	rootCmd.AddCommand(findingsCmd)
}

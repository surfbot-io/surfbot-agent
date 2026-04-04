package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

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
		newOnly, _ := cmd.Flags().GetBool("new")
		resolvedOnly, _ := cmd.Flags().GetBool("resolved")

		if limit <= 0 {
			limit = 50
		}

		// --resolved overrides status filter
		if resolvedOnly {
			status = string(model.FindingStatusResolved)
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

		// --new: filter to findings where first_seen == last_seen
		if newOnly {
			var filtered []model.Finding
			for _, f := range findings {
				if f.FirstSeen.Equal(f.LastSeen) {
					filtered = append(filtered, f)
				}
			}
			findings = filtered
		}

		p := NewPrinter(os.Stdout)

		if len(findings) == 0 {
			if resolvedOnly {
				p.EmptyState("No resolved findings.",
					"Findings are auto-resolved when no longer detected in subsequent scans.")
			} else {
				p.EmptyState("No findings found.",
					"Run `surfbot scan <target>` first, or check targets with `surfbot target list`.")
			}
			return nil
		}

		w := p.NewTable()
		if resolvedOnly {
			p.Theme.Bold.Fprintln(w, "SEVERITY\tTITLE\tTOOL\tRESOLVED AT")
			p.Divider(70)
			for _, f := range findings {
				title := truncate(f.Title, 50)
				resolvedAt := ""
				if f.ResolvedAt != nil {
					resolvedAt = f.ResolvedAt.Format("2006-01-02 15:04:05")
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
					p.Severity(f.Severity),
					title,
					f.SourceTool,
					resolvedAt,
				)
			}
		} else if newOnly {
			p.Theme.Bold.Fprintln(w, "SEVERITY\tTITLE\tTOOL\tFIRST SEEN")
			p.Divider(70)
			for _, f := range findings {
				title := truncate(f.Title, 50)
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
					p.Severity(f.Severity),
					title,
					f.SourceTool,
					f.FirstSeen.Format("2006-01-02 15:04:05"),
				)
			}
		} else {
			p.Theme.Bold.Fprintln(w, "ID\tSEVERITY\tTITLE\tTOOL\tSTATUS")
			p.Divider(70)
			for _, f := range findings {
				short := f.ID
				if len(short) > 8 {
					short = short[:8]
				}
				title := truncate(f.Title, 50)
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
					short,
					p.Severity(f.Severity),
					title,
					f.SourceTool,
					f.Status,
				)
			}
		}
		w.Flush()

		// Summary with colored severity counts
		crit, high, med := 0, 0, 0
		for _, f := range findings {
			switch f.Severity {
			case model.SeverityCritical:
				crit++
			case model.SeverityHigh:
				high++
			case model.SeverityMedium:
				med++
			}
		}
		fmt.Fprintf(os.Stdout, "\n%d findings", len(findings))
		if crit > 0 {
			p.Theme.Critical.Fprintf(os.Stdout, " (%d critical)", crit)
		} else if high > 0 {
			p.Theme.High.Fprintf(os.Stdout, " (%d high)", high)
		} else if med > 0 {
			p.Theme.Medium.Fprintf(os.Stdout, " (%d medium)", med)
		}
		fmt.Fprintln(os.Stdout, "")

		if !newOnly && !resolvedOnly {
			p.Muted("Use `surfbot findings show <id>` for full details.\n")
		}

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

		p := NewPrinter(os.Stdout)

		p.Theme.Bold.Fprintf(os.Stdout, "Finding: %s\n", found.ID)
		fmt.Fprintf(os.Stdout, "Severity: %s\n", p.Severity(found.Severity))
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
			p.Theme.Bold.Fprintf(os.Stdout, "\nDescription:\n")
			fmt.Printf("  %s\n", found.Description)
		}
		if found.Evidence != "" {
			p.Theme.Bold.Fprintf(os.Stdout, "\nEvidence:\n")
			fmt.Printf("  %s\n", found.Evidence)
		}
		if found.Remediation != "" {
			p.Theme.Bold.Fprintf(os.Stdout, "\nRemediation:\n")
			fmt.Printf("  %s\n", found.Remediation)
		}
		if len(found.References) > 0 {
			p.Theme.Bold.Fprintf(os.Stdout, "\nReferences:\n")
			for _, ref := range found.References {
				fmt.Printf("  - %s\n", ref)
			}
		}
		p.Muted("\nFirst seen: %s\n", found.FirstSeen.Format("2006-01-02 15:04:05"))
		p.Muted("Last seen:  %s\n", found.LastSeen.Format("2006-01-02 15:04:05"))

		return nil
	},
}

func init() {
	findingsListCmd.Flags().String("severity", "", "Filter by severity: critical|high|medium|low|info")
	findingsListCmd.Flags().String("tool", "", "Filter by source tool")
	findingsListCmd.Flags().String("status", "", "Filter by status: open|resolved|acknowledged|false_positive|ignored")
	findingsListCmd.Flags().String("scan", "", "Filter by scan ID")
	findingsListCmd.Flags().Int("limit", 50, "Max number of results")
	findingsListCmd.Flags().Bool("new", false, "Show only new findings from last scan")
	findingsListCmd.Flags().Bool("resolved", false, "Show only recently resolved findings")

	findingsCmd.AddCommand(findingsListCmd, findingsShowCmd)
	rootCmd.AddCommand(findingsCmd)
}

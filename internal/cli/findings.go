package cli

import (
	"context"
	"encoding/json"
	"fmt"
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

		if newOnly {
			var filtered []model.Finding
			for _, f := range findings {
				if f.FirstSeen.Equal(f.LastSeen) {
					filtered = append(filtered, f)
				}
			}
			findings = filtered
		}

		if jsonOut {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(findings)
		}

		p := NewPrinter(cmd.OutOrStdout())

		if len(findings) == 0 {
			if resolvedOnly {
				p.EmptyState("No resolved findings.",
					"Findings are auto-resolved when no longer detected in subsequent scans.")
			} else {
				p.EmptyState("No findings.",
					"Run 'surfbot scan <domain>' to generate findings.")
			}
			return nil
		}

		w := p.NewTable()
		switch {
		case resolvedOnly:
			p.Theme.Bold.Fprintln(w, "SEVERITY\tTITLE\tTOOL\tRESOLVED AT")
			for _, f := range findings {
				title := truncate(f.Title, 50)
				resolvedAt := ""
				if f.ResolvedAt != nil {
					resolvedAt = f.ResolvedAt.Format("2006-01-02 15:04:05")
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
					p.Severity(f.Severity), title, f.SourceTool, resolvedAt)
			}
		case newOnly:
			p.Theme.Bold.Fprintln(w, "SEVERITY\tTITLE\tTOOL\tFIRST SEEN")
			for _, f := range findings {
				title := truncate(f.Title, 50)
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
					p.Severity(f.Severity), title, f.SourceTool,
					f.FirstSeen.Format("2006-01-02 15:04:05"))
			}
		default:
			p.Theme.Bold.Fprintln(w, "ID\tSEVERITY\tTITLE\tTOOL\tSTATUS")
			for _, f := range findings {
				short := f.ID
				if len(short) > 8 {
					short = short[:8]
				}
				title := truncate(f.Title, 50)
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
					short, p.Severity(f.Severity), title, f.SourceTool, f.Status)
			}
		}
		w.Flush()

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
		fmt.Fprintf(p.W, "\n%d findings", len(findings))
		switch {
		case crit > 0:
			p.Theme.Critical.Fprintf(p.W, " (%d critical)", crit)
		case high > 0:
			p.Theme.High.Fprintf(p.W, " (%d high)", high)
		case med > 0:
			p.Theme.Medium.Fprintf(p.W, " (%d medium)", med)
		}
		fmt.Fprintln(p.W, "")

		if !newOnly && !resolvedOnly {
			p.ActionHint("use 'surfbot findings show <id>' for full details.")
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

		p := NewPrinter(cmd.OutOrStdout())

		p.Theme.Bold.Fprintf(p.W, "Finding: %s\n", found.ID)
		fmt.Fprintf(p.W, "Severity: %s\n", p.Severity(found.Severity))
		p.Keyf("Title", "%s", found.Title)
		p.Keyf("Template", "%s (%s)", found.TemplateID, found.TemplateName)
		p.Keyf("Tool", "%s", found.SourceTool)
		p.Keyf("Status", "%s", found.Status)
		p.Keyf("Confidence", "%.0f%%", found.Confidence)
		if found.CVE != "" {
			p.Keyf("CVE", "%s", found.CVE)
		}
		if found.CVSS > 0 {
			p.Keyf("CVSS", "%.1f", found.CVSS)
		}
		if found.Description != "" {
			p.SectionHeader("Description")
			fmt.Fprintf(p.W, "  %s\n", found.Description)
		}
		if found.Evidence != "" {
			p.SectionHeader("Evidence")
			fmt.Fprintf(p.W, "  %s\n", found.Evidence)
		}
		if found.Remediation != "" {
			p.SectionHeader("Remediation")
			fmt.Fprintf(p.W, "  %s\n", found.Remediation)
		}
		if len(found.References) > 0 {
			p.SectionHeader("References")
			for _, ref := range found.References {
				p.Bullet("%s", ref)
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

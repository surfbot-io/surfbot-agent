package cli

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/surfbot-io/surfbot-agent/internal/model"
	"github.com/surfbot-io/surfbot-agent/internal/storage"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show agent status: DB path, targets count, last scan, findings summary",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		p := NewPrinter(os.Stdout)

		p.Theme.Bold.Fprintf(os.Stdout, "Surfbot Agent %s\n\n", Version)

		// DB info
		dbPath := store.DBPath()
		dbSize := "unknown"
		if info, err := os.Stat(dbPath); err == nil {
			dbSize = formatBytes(info.Size())
		}
		fmt.Printf("Database:    %s (%s)\n", dbPath, dbSize)

		// Counts
		targets, _ := store.CountTargets(ctx)
		assets, _ := store.CountAssets(ctx)

		fmt.Printf("Targets:     %d\n", targets)
		fmt.Printf("Assets:      %d\n", assets)

		// Findings with colored severity breakdown
		findings, _ := store.CountFindings(ctx)
		fmt.Fprintf(os.Stdout, "Findings:    %d", findings)
		if findings > 0 {
			allFindings, _ := store.ListFindings(ctx, storage.FindingListOptions{Limit: findings})
			parts := countSeveritiesColored(p, allFindings)
			if len(parts) > 0 {
				fmt.Fprintf(os.Stdout, " (%s)", joinColoredParts(parts))
			}
		}
		fmt.Fprintln(os.Stdout)

		// Last scan
		lastScanStr := "never"
		if last, err := store.LastScan(ctx); err == nil && last != nil {
			ago := time.Since(last.CreatedAt)
			target, _ := store.GetTarget(ctx, last.TargetID)
			targetName := last.TargetID
			if target != nil {
				targetName = target.Value
			}
			lastScanStr = fmt.Sprintf("%s — %s ago (%s)", targetName, formatDurationShort(ago), last.Status)
		}
		fmt.Printf("Last scan:   %s\n", lastScanStr)

		// Tools
		allTools := registry.Tools()
		availTools := registry.AvailableTools()
		fmt.Printf("Tools:       %d/%d available\n", len(availTools), len(allTools))

		return nil
	},
}

func init() {
	rootCmd.AddCommand(statusCmd)
}

func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

func formatDurationShort(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

type coloredPart struct {
	text string
}

func countSeveritiesColored(p *Printer, findings []model.Finding) []coloredPart {
	counts := map[model.Severity]int{}
	for _, f := range findings {
		counts[f.Severity]++
	}
	var parts []coloredPart
	for _, sev := range []model.Severity{model.SeverityCritical, model.SeverityHigh, model.SeverityMedium, model.SeverityLow} {
		if c, ok := counts[sev]; ok && c > 0 {
			c := p.Theme.SeverityColor(sev)
			parts = append(parts, coloredPart{text: c.Sprintf("%d %s", counts[sev], sev)})
		}
	}
	return parts
}

func joinColoredParts(parts []coloredPart) string {
	strs := make([]string, len(parts))
	for i, p := range parts {
		strs[i] = p.text
	}
	return fmt.Sprintf("%s", joinStrings(strs, ", "))
}

func joinStrings(ss []string, sep string) string {
	if len(ss) == 0 {
		return ""
	}
	result := ss[0]
	for _, s := range ss[1:] {
		result += sep + s
	}
	return result
}

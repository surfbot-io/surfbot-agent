package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
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

		fmt.Printf("Surfbot Agent %s\n\n", Version)

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

		// Findings with severity breakdown
		findings, _ := store.CountFindings(ctx)
		findingsStr := fmt.Sprintf("%d", findings)
		if findings > 0 {
			allFindings, _ := store.ListFindings(ctx, storage.FindingListOptions{Limit: findings})
			sevCounts := countSeverities(allFindings)
			if len(sevCounts) > 0 {
				findingsStr += " (" + strings.Join(sevCounts, ", ") + ")"
			}
		}
		fmt.Printf("Findings:    %s\n", findingsStr)

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

func countSeverities(findings []model.Finding) []string {
	counts := map[model.Severity]int{}
	for _, f := range findings {
		counts[f.Severity]++
	}
	var parts []string
	for _, sev := range []model.Severity{model.SeverityCritical, model.SeverityHigh, model.SeverityMedium, model.SeverityLow} {
		if c, ok := counts[sev]; ok && c > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", c, sev))
		}
	}
	return parts
}

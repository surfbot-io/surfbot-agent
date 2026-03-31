package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
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
		scans, _ := store.CountScans(ctx)
		findings, _ := store.CountFindings(ctx)
		assets, _ := store.CountAssets(ctx)

		fmt.Printf("Targets:     %d\n", targets)

		lastScanStr := "never"
		if last, err := store.LastScan(ctx); err == nil && last != nil {
			lastScanStr = last.CreatedAt.Format("2006-01-02 15:04")
		}
		fmt.Printf("Scans:       %d (last: %s)\n", scans, lastScanStr)
		fmt.Printf("Findings:    %d\n", findings)
		fmt.Printf("Assets:      %d\n", assets)

		// Tools
		allTools := registry.Tools()
		availTools := registry.AvailableTools()
		var toolNames []string
		for _, t := range allTools {
			toolNames = append(toolNames, t.Name())
		}
		fmt.Printf("\nTools:       %s (%d/%d available)\n",
			strings.Join(toolNames, ", "), len(availTools), len(allTools))
		fmt.Println("Connectors:  none configured")
		fmt.Println("Cloud:       not connected")

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

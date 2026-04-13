package cli

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/surfbot-io/surfbot-agent/internal/scoring"
)

type scoreOutput struct {
	Score     int                 `json:"score"`
	Breakdown []scoring.Component `json:"breakdown"`
}

var scoreCmd = &cobra.Command{
	Use:   "score [target]",
	Short: "Show security score",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		p := NewPrinter(cmd.OutOrStdout())

		sevCounts, err := store.CountFindingsBySeverity(ctx)
		if err != nil {
			return fmt.Errorf("counting findings: %w", err)
		}

		score, breakdown := scoring.ComputeSecurityScore(sevCounts)

		if jsonOut {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(scoreOutput{Score: score, Breakdown: breakdown})
		}

		p.ScoreBar(score)
		if len(breakdown) > 0 {
			fmt.Fprintln(p.W)
			for _, c := range breakdown {
				p.Bullet("%d %s finding%s (−%d each = −%d)",
					c.Count, c.Severity, plural(c.Count), c.Weight, c.Penalty)
			}
		}
		return nil
	},
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func init() {
	rootCmd.AddCommand(scoreCmd)
}

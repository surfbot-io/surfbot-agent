package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/surfbot-io/surfbot-agent/internal/model"
	"github.com/surfbot-io/surfbot-agent/internal/pipeline"
	"github.com/surfbot-io/surfbot-agent/internal/storage"
)

var scanCmd = &cobra.Command{
	Use:   "scan [target]",
	Short: "Run a security scan against a target",
	Long:  "Runs the full detection pipeline: discovery → resolution → port scan → http probe → vulnerability assessment",
	Args:  cobra.ExactArgs(1),
	RunE:  runScan,
}

func init() {
	scanCmd.Flags().StringP("type", "t", "full", "Scan type: full, quick, or discovery")
	scanCmd.Flags().StringSlice("tools", nil, "Specific tools to run (comma-separated)")
	scanCmd.Flags().IntP("rate-limit", "r", 0, "Global rate limit (requests/second)")
	scanCmd.Flags().Int("timeout", 300, "Per-phase timeout in seconds")
	scanCmd.Flags().StringP("output", "o", "", "Output results to file (JSON)")
	rootCmd.AddCommand(scanCmd)
}

func runScan(cmd *cobra.Command, args []string) error {
	ctx := context.Background()
	targetValue := args[0]

	target, err := autoCreateTarget(ctx, store, targetValue)
	if err != nil {
		return fmt.Errorf("resolving target: %w", err)
	}

	pipe := pipeline.New(store, registry)

	scanType, _ := cmd.Flags().GetString("type")
	tools, _ := cmd.Flags().GetStringSlice("tools")
	rateLimit, _ := cmd.Flags().GetInt("rate-limit")
	timeout, _ := cmd.Flags().GetInt("timeout")

	opts := pipeline.PipelineOptions{
		ScanType:  parseScanType(scanType),
		Tools:     tools,
		RateLimit: rateLimit,
		Timeout:   timeout,
	}

	result, err := pipe.Run(ctx, target.ID, opts)
	if err != nil {
		return err
	}

	pipeline.PrintSummary(result)

	outputPath, _ := cmd.Flags().GetString("output")
	if outputPath != "" {
		if err := pipeline.WriteJSONResult(result, outputPath); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to write output file: %v\n", err)
		}
	}

	return nil
}

func parseScanType(s string) model.ScanType {
	switch s {
	case "quick":
		return model.ScanTypeQuick
	case "discovery":
		return model.ScanTypeDiscovery
	default:
		return model.ScanTypeFull
	}
}

func autoCreateTarget(ctx context.Context, s *storage.SQLiteStore, value string) (*model.Target, error) {
	existing, err := s.GetTargetByValue(ctx, value)
	if err == nil {
		return existing, nil
	}
	if err != storage.ErrNotFound {
		return nil, err
	}

	target := &model.Target{
		Value:   value,
		Enabled: true,
	}
	if err := s.CreateTarget(ctx, target); err != nil {
		return nil, err
	}
	return target, nil
}

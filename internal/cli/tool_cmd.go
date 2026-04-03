package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/surfbot-io/surfbot-agent/internal/detection"
	"github.com/surfbot-io/surfbot-agent/internal/model"
	"github.com/surfbot-io/surfbot-agent/internal/storage"
)

// ToolResult is the JSON output structure for atomic tool commands.
type ToolResult struct {
	Tool       string          `json:"tool"`
	Phase      string          `json:"phase"`
	Target     string          `json:"target,omitempty"`
	DurationMs int64           `json:"duration_ms"`
	Assets     []AssetOutput   `json:"assets"`
	Findings   []FindingOutput `json:"findings"`
	Stats      ToolStats       `json:"stats"`
	Errors     []string        `json:"errors"`
}

// AssetOutput is the JSON output for a single asset.
type AssetOutput struct {
	Type     string            `json:"type"`
	Value    string            `json:"value"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// FindingOutput is the JSON output for a single finding.
type FindingOutput struct {
	TemplateID string `json:"template_id"`
	Title      string `json:"title"`
	Severity   string `json:"severity"`
	URL        string `json:"url,omitempty"`
	Evidence   string `json:"evidence,omitempty"`
}

// ToolStats holds input/output counts.
type ToolStats struct {
	InputCount  int `json:"input_count"`
	OutputCount int `json:"output_count"`
}

// buildToolCommand creates a cobra.Command for a single DetectionTool.
func buildToolCommand(tool detection.DetectionTool) *cobra.Command {
	cmd := &cobra.Command{
		Use:   fmt.Sprintf("%s [inputs...]", tool.Command()),
		Short: tool.Description(),
		Long: fmt.Sprintf(
			"%s\n\nInput type: %s\nOutput types: %s\nPhase: %s\nTool: %s",
			tool.Description(),
			tool.InputType(),
			strings.Join(tool.OutputTypes(), ", "),
			tool.Phase(),
			tool.Name(),
		),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAtomicTool(cmd, args, tool)
		},
	}

	cmd.Flags().Bool("stdin", false, "Read inputs from stdin (one per line)")
	cmd.Flags().IntP("rate-limit", "r", 0, "Rate limit (requests/second, 0 = tool default)")
	cmd.Flags().Int("timeout", 300, "Timeout in seconds")
	cmd.Flags().StringP("format", "f", "json", "Output format: json or text")
	cmd.Flags().Bool("persist", true, "Persist results to SQLite database")
	cmd.Flags().String("target", "", "Target ID to associate results with (auto-created if needed)")
	cmd.Flags().StringToString("extra", nil, "Extra tool-specific options (key=value pairs)")

	return cmd
}

func runAtomicTool(cmd *cobra.Command, args []string, tool detection.DetectionTool) error {
	inputs, err := collectInputs(cmd, args)
	if err != nil {
		return fmt.Errorf("failed to collect inputs: %w", err)
	}
	if len(inputs) == 0 {
		return fmt.Errorf("no inputs provided. Usage: surfbot %s <inputs...> or --stdin", tool.Command())
	}

	if !tool.Available() {
		return fmt.Errorf("tool %s is not available (missing dependency)", tool.Name())
	}

	rateLimit, _ := cmd.Flags().GetInt("rate-limit")
	timeout, _ := cmd.Flags().GetInt("timeout")
	extra, _ := cmd.Flags().GetStringToString("extra")
	opts := detection.RunOptions{
		RateLimit: rateLimit,
		Timeout:   timeout,
		ExtraArgs: extra,
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), time.Duration(timeout)*time.Second)
	defer cancel()

	startTime := time.Now()
	result, err := tool.Run(ctx, inputs, opts)
	duration := time.Since(startTime)

	output := buildToolResult(tool, inputs, result, duration, err)

	persist, _ := cmd.Flags().GetBool("persist")
	if persist && result != nil && store != nil {
		if persistErr := persistAtomicResults(cmd, tool, inputs, result); persistErr != nil {
			output.Errors = append(output.Errors, fmt.Sprintf("persist error: %v", persistErr))
		}
	}

	format, _ := cmd.Flags().GetString("format")
	return outputResult(cmd.OutOrStdout(), output, format)
}

// collectInputs gathers inputs from command args and optionally stdin.
func collectInputs(cmd *cobra.Command, args []string) ([]string, error) {
	var inputs []string
	inputs = append(inputs, args...)

	useStdin, _ := cmd.Flags().GetBool("stdin")
	if useStdin {
		stdinInputs, err := readStdin(os.Stdin)
		if err != nil {
			return nil, err
		}
		inputs = append(inputs, stdinInputs...)
	}

	return inputs, nil
}

// readStdin reads line-delimited values from a reader, skipping empty lines.
func readStdin(r io.Reader) ([]string, error) {
	var inputs []string
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			inputs = append(inputs, line)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading stdin: %w", err)
	}
	return inputs, nil
}

// buildToolResult converts RunResult + error into the JSON-friendly ToolResult.
func buildToolResult(tool detection.DetectionTool, inputs []string, result *detection.RunResult, duration time.Duration, runErr error) ToolResult {
	output := ToolResult{
		Tool:       tool.Name(),
		Phase:      tool.Phase(),
		DurationMs: duration.Milliseconds(),
		Assets:     []AssetOutput{},
		Findings:   []FindingOutput{},
		Stats: ToolStats{
			InputCount: len(inputs),
		},
		Errors: []string{},
	}

	if len(inputs) == 1 {
		output.Target = inputs[0]
	}

	if runErr != nil {
		output.Errors = append(output.Errors, runErr.Error())
	}

	if result != nil {
		for _, a := range result.Assets {
			ao := AssetOutput{
				Type:  string(a.Type),
				Value: a.Value,
			}
			if len(a.Metadata) > 0 {
				ao.Metadata = make(map[string]string, len(a.Metadata))
				for k, v := range a.Metadata {
					ao.Metadata[k] = fmt.Sprintf("%v", v)
				}
			}
			output.Assets = append(output.Assets, ao)
		}
		for _, f := range result.Findings {
			output.Findings = append(output.Findings, FindingOutput{
				TemplateID: f.TemplateID,
				Title:      f.Title,
				Severity:   string(f.Severity),
				URL:        f.Evidence,
			})
		}
		output.Stats.OutputCount = len(result.Assets) + len(result.Findings)
	}

	return output
}

// persistAtomicResults saves assets and findings to SQLite.
func persistAtomicResults(cmd *cobra.Command, tool detection.DetectionTool, inputs []string, result *detection.RunResult) error {
	ctx := cmd.Context()

	targetFlag, _ := cmd.Flags().GetString("target")
	targetID, err := resolveOrCreateTarget(ctx, store, targetFlag, inputs)
	if err != nil {
		return fmt.Errorf("resolving target: %w", err)
	}

	for i := range result.Assets {
		result.Assets[i].TargetID = targetID
		if err := store.UpsertAsset(ctx, &result.Assets[i]); err != nil {
			return fmt.Errorf("persisting asset: %w", err)
		}
	}

	for i := range result.Findings {
		if err := store.UpsertFinding(ctx, &result.Findings[i]); err != nil {
			return fmt.Errorf("persisting finding: %w", err)
		}
	}

	return nil
}

// resolveOrCreateTarget finds an existing target or creates one from the first input.
func resolveOrCreateTarget(ctx context.Context, s *storage.SQLiteStore, targetFlag string, inputs []string) (string, error) {
	if targetFlag != "" {
		t, err := s.GetTarget(ctx, targetFlag)
		if err == nil {
			return t.ID, nil
		}
		// Try by value
		t, err = s.GetTargetByValue(ctx, targetFlag)
		if err == nil {
			return t.ID, nil
		}
	}

	// Auto-create from first input
	if len(inputs) == 0 {
		return "", fmt.Errorf("no target and no inputs")
	}
	value := inputs[0]
	t, err := s.GetTargetByValue(ctx, value)
	if err == nil {
		return t.ID, nil
	}

	target := &model.Target{
		Value:   value,
		Enabled: true,
	}
	if err := s.CreateTarget(ctx, target); err != nil {
		return "", err
	}
	return target.ID, nil
}

// outputResult writes the ToolResult as JSON or text.
func outputResult(w io.Writer, output ToolResult, format string) error {
	switch format {
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(output)
	case "text":
		return printTextResult(w, output)
	default:
		return fmt.Errorf("unknown format: %s (use json or text)", format)
	}
}

func printTextResult(w io.Writer, output ToolResult) error {
	fmt.Fprintf(w, "%s — %s\n", output.Tool, output.Phase)
	fmt.Fprintln(w, strings.Repeat("─", 40))
	fmt.Fprintf(w, "Duration: %.1fs\n", float64(output.DurationMs)/1000)
	fmt.Fprintf(w, "Inputs:   %d\n", output.Stats.InputCount)
	fmt.Fprintf(w, "Outputs:  %d\n", output.Stats.OutputCount)

	if len(output.Assets) > 0 {
		fmt.Fprintf(w, "\nASSETS (%d)\n", len(output.Assets))
		for _, a := range output.Assets {
			fmt.Fprintf(w, "  %-12s %s\n", a.Type, a.Value)
		}
	}

	if len(output.Findings) > 0 {
		fmt.Fprintf(w, "\nFINDINGS (%d)\n", len(output.Findings))
		for _, f := range output.Findings {
			fmt.Fprintf(w, "  [%s] %s\n", f.Severity, f.Title)
		}
	}

	if len(output.Errors) > 0 {
		fmt.Fprintf(w, "\nERRORS (%d)\n", len(output.Errors))
		for _, e := range output.Errors {
			fmt.Fprintf(w, "  %s\n", e)
		}
	}

	return nil
}

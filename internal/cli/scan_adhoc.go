package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/surfbot-io/surfbot-agent/internal/cli/apiclient"
	"github.com/surfbot-io/surfbot-agent/internal/cli/common"
)

// adhocScanClient is the Dispatcher-facing slice of the API client.
// Extracted so tests can stub POST /api/v1/scans/ad-hoc without
// spinning up httptest.
type adhocScanClient interface {
	CreateAdHocScan(ctx context.Context, req apiclient.CreateAdHocRequest) (apiclient.CreateAdHocResponse, error)
}

var adhocScanClientFactory = func(cmd *cobra.Command) (adhocScanClient, error) {
	flagURL, _ := cmd.Flags().GetString("daemon-url")
	cfg := common.ResolveAPIConfig(flagURL)
	return apiclient.New(cfg.BaseURL, apiclient.WithAuthToken(cfg.AuthToken)), nil
}

var scanAdhocCmd = &cobra.Command{
	Use:   "adhoc",
	Short: "Dispatch an ad-hoc scan through the daemon API",
	Long: `Fire a one-off scan via POST /api/v1/scans/ad-hoc. The daemon applies
the target's template and schedule defaults to fill in tool configuration;
use --tool-config-override to supply tool params inline.

This is the replacement for the legacy /api/daemon/trigger shim. Exit
codes: 0 success, 1 daemon unreachable, 2 validation, 3 target not found,
4 target busy or in blackout.`,
	RunE: runScanAdhoc,
}

func init() {
	scanAdhocCmd.Flags().String("daemon-url", "", "Base URL of the surfbot daemon")
	scanAdhocCmd.Flags().StringP("output", "o", "table", "Output format: table|json|yaml")
	scanAdhocCmd.Flags().String("target", "", "Target id (required)")
	scanAdhocCmd.Flags().String("template", "", "Template id override")
	scanAdhocCmd.Flags().String("reason", "", "Free-form reason recorded in ad_hoc_scan_runs")
	scanAdhocCmd.Flags().String("requested-by", "cli", "Identity recorded on the run row")
	scanAdhocCmd.Flags().String("tool-config-override", "", "Path to a JSON file with per-tool overrides (optional)")
	scanAdhocCmd.Flags().Bool("wait", false, "Poll for completion (unsupported: no polling endpoint exists yet)")
	scanCmd.AddCommand(scanAdhocCmd)
}

func adhocScanFormat(cmd *cobra.Command) (common.OutputFormat, error) {
	if jsonOut {
		return common.FormatJSON, nil
	}
	raw, _ := cmd.Flags().GetString("output")
	return common.ParseOutputFormat(raw)
}

func adhocScanExit(cmd *cobra.Command, err error) error {
	format, _ := adhocScanFormat(cmd)
	code := common.HandleAPIError(err, format, cmd.ErrOrStderr())
	if code == common.ExitOK {
		return nil
	}
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	return errExit(code)
}

func runScanAdhoc(cmd *cobra.Command, _ []string) error {
	target, _ := cmd.Flags().GetString("target")
	if target == "" {
		return errors.New("--target is required")
	}
	reason, _ := cmd.Flags().GetString("reason")
	requestedBy, _ := cmd.Flags().GetString("requested-by")
	req := apiclient.CreateAdHocRequest{
		TargetID:    target,
		Reason:      reason,
		RequestedBy: requestedBy,
	}
	if tmpl, _ := cmd.Flags().GetString("template"); tmpl != "" {
		req.TemplateID = &tmpl
	}
	if path, _ := cmd.Flags().GetString("tool-config-override"); path != "" {
		override, oerr := readToolConfigOverride(path)
		if oerr != nil {
			cmd.SilenceUsage = true
			_, _ = fmt.Fprintln(cmd.ErrOrStderr(), oerr)
			return errExit(common.ExitValidation)
		}
		req.ToolConfigOverride = override
	}

	// --wait is spec'd as a warn-and-no-op: no polling endpoint exists
	// in 1.3a, so we tell the operator we're dispatching and exit 0
	// once the 202 lands. 1.5 may add a scan-status endpoint the CLI
	// can poll; this flag is forward-compatible.
	if wait, _ := cmd.Flags().GetBool("wait"); wait {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "[!] --wait is a no-op: no polling endpoint exists yet (SCHED1.3a scope)")
	}

	c, err := adhocScanClientFactory(cmd)
	if err != nil {
		return adhocScanExit(cmd, err)
	}
	out, err := c.CreateAdHocScan(context.Background(), req)
	if err != nil {
		return adhocScanExit(cmd, err)
	}
	format, _ := adhocScanFormat(cmd)
	return common.Render(cmd.OutOrStdout(), format, out, func(w io.Writer) error {
		_, _ = fmt.Fprintf(w, "ad_hoc_run_id: %s\n", out.AdHocRunID)
		if out.ScanID != "" {
			_, _ = fmt.Fprintf(w, "scan_id:       %s\n", out.ScanID)
		}
		return nil
	})
}

// readToolConfigOverride parses the file at path as
// map[string]json.RawMessage so the server's typed tool_config
// validation can do its job on the content without the CLI having
// to know every tool's param shape.
func readToolConfigOverride(path string) (map[string]json.RawMessage, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("tool-config-override must be a JSON object: %w", err)
	}
	return out, nil
}

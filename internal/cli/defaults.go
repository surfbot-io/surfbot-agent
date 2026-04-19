package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/surfbot-io/surfbot-agent/internal/cli/apiclient"
	"github.com/surfbot-io/surfbot-agent/internal/cli/common"
)

type defaultsClient interface {
	GetDefaults(ctx context.Context) (apiclient.ScheduleDefaults, error)
	UpdateDefaults(ctx context.Context, req apiclient.UpdateScheduleDefaultsRequest) (apiclient.ScheduleDefaults, error)
}

var defaultsClientFactory = func(cmd *cobra.Command) (defaultsClient, error) {
	flagURL, _ := cmd.Flags().GetString("daemon-url")
	cfg := common.ResolveAPIConfig(flagURL)
	return apiclient.New(cfg.BaseURL, apiclient.WithAuthToken(cfg.AuthToken)), nil
}

var defaultsCmd = &cobra.Command{
	Use:   "defaults",
	Short: "View and update the singleton schedule defaults",
	Long:  "Schedule defaults supply fallback values (RRULE, timezone, jitter, concurrency) for schedules that don't override them at the template or schedule layer.",
}

var defaultsShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show schedule defaults",
	RunE:  runDefaultsShow,
}

var defaultsUpdateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update schedule defaults (partial — server PUTs are full-replace; CLI fetches, merges, then PUTs)",
	RunE:  runDefaultsUpdate,
}

func init() {
	defaultsCmd.PersistentFlags().String("daemon-url", "", "Base URL of the surfbot daemon")
	defaultsCmd.PersistentFlags().StringP("output", "o", "table", "Output format: table|json|yaml")

	defaultsUpdateCmd.Flags().String("rrule", "", "Fallback RRULE")
	defaultsUpdateCmd.Flags().String("tzid", "", "Fallback timezone")
	defaultsUpdateCmd.Flags().Int("max-concurrent-scans", 0, "Daemon-wide scan concurrency cap")
	defaultsUpdateCmd.Flags().Int("jitter-seconds", -1, "Seconds of jitter added to each fire")
	defaultsUpdateCmd.Flags().Bool("run-on-start", false, "Whether a schedule fires immediately on daemon start")
	defaultsUpdateCmd.Flags().String("default-template-id", "", "Fallback template id (empty clears)")

	defaultsCmd.AddCommand(defaultsShowCmd, defaultsUpdateCmd)
	rootCmd.AddCommand(defaultsCmd)
}

func defaultsFormat(cmd *cobra.Command) (common.OutputFormat, error) {
	if jsonOut {
		return common.FormatJSON, nil
	}
	raw, _ := cmd.Flags().GetString("output")
	return common.ParseOutputFormat(raw)
}

func defaultsExit(cmd *cobra.Command, err error) error {
	format, _ := defaultsFormat(cmd)
	code := common.HandleAPIError(err, format, cmd.ErrOrStderr())
	if code == common.ExitOK {
		return nil
	}
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	return errExit(code)
}

func runDefaultsShow(cmd *cobra.Command, _ []string) error {
	c, err := defaultsClientFactory(cmd)
	if err != nil {
		return defaultsExit(cmd, err)
	}
	d, err := c.GetDefaults(context.Background())
	if err != nil {
		return defaultsExit(cmd, err)
	}
	format, _ := defaultsFormat(cmd)
	return common.Render(cmd.OutOrStdout(), format, d, func(w io.Writer) error {
		return renderDefaultsDetail(w, d)
	})
}

func renderDefaultsDetail(w io.Writer, d apiclient.ScheduleDefaults) error {
	tw := common.NewTable(w)
	defer func() { _ = tw.Flush() }()
	fmt.Fprintf(tw, "DefaultRRule:\t%s\n", d.DefaultRRule)
	fmt.Fprintf(tw, "DefaultTimezone:\t%s\n", d.DefaultTimezone)
	fmt.Fprintf(tw, "MaxConcurrentScans:\t%d\n", d.MaxConcurrentScans)
	fmt.Fprintf(tw, "RunOnStart:\t%t\n", d.RunOnStart)
	fmt.Fprintf(tw, "JitterSeconds:\t%d\n", d.JitterSeconds)
	if d.DefaultTemplateID != nil {
		fmt.Fprintf(tw, "DefaultTemplateID:\t%s\n", *d.DefaultTemplateID)
	}
	return nil
}

// runDefaultsUpdate performs a fetch-merge-PUT dance because the
// server treats PUT as full-replace. Fields the user didn't touch
// keep their current values. Empty `--rrule ""` / `--tzid ""` is
// treated as "no change" rather than "clear" — the server rejects
// empty required fields anyway.
func runDefaultsUpdate(cmd *cobra.Command, _ []string) error {
	c, err := defaultsClientFactory(cmd)
	if err != nil {
		return defaultsExit(cmd, err)
	}
	cur, err := c.GetDefaults(context.Background())
	if err != nil {
		return defaultsExit(cmd, err)
	}
	req := apiclient.UpdateScheduleDefaultsRequest{
		DefaultRRule:             cur.DefaultRRule,
		DefaultTimezone:          cur.DefaultTimezone,
		DefaultToolConfig:        cur.DefaultToolConfig,
		DefaultMaintenanceWindow: cur.DefaultMaintenanceWindow,
		MaxConcurrentScans:       cur.MaxConcurrentScans,
		RunOnStart:               cur.RunOnStart,
		JitterSeconds:            cur.JitterSeconds,
		DefaultTemplateID:        cur.DefaultTemplateID,
	}
	if cmd.Flags().Changed("rrule") {
		v, _ := cmd.Flags().GetString("rrule")
		req.DefaultRRule = v
	}
	if cmd.Flags().Changed("tzid") {
		v, _ := cmd.Flags().GetString("tzid")
		req.DefaultTimezone = v
	}
	if cmd.Flags().Changed("max-concurrent-scans") {
		v, _ := cmd.Flags().GetInt("max-concurrent-scans")
		req.MaxConcurrentScans = v
	}
	if cmd.Flags().Changed("jitter-seconds") {
		v, _ := cmd.Flags().GetInt("jitter-seconds")
		req.JitterSeconds = v
	}
	if cmd.Flags().Changed("run-on-start") {
		v, _ := cmd.Flags().GetBool("run-on-start")
		req.RunOnStart = v
	}
	if cmd.Flags().Changed("default-template-id") {
		v, _ := cmd.Flags().GetString("default-template-id")
		if v == "" {
			req.DefaultTemplateID = nil
		} else {
			req.DefaultTemplateID = &v
		}
	}
	updated, err := c.UpdateDefaults(context.Background(), req)
	if err != nil {
		return defaultsExit(cmd, err)
	}
	format, _ := defaultsFormat(cmd)
	return common.Render(cmd.OutOrStdout(), format, updated, func(w io.Writer) error {
		return renderDefaultsDetail(w, updated)
	})
}

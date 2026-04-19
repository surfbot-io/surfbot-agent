package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/surfbot-io/surfbot-agent/internal/cli/apiclient"
	"github.com/surfbot-io/surfbot-agent/internal/cli/common"
)

// scheduleClient is the narrow surface every `surfbot schedule`
// subcommand consumes. Extracted as an interface so tests swap in an
// in-memory stub without httptest fixtures.
type scheduleClient interface {
	ListSchedules(ctx context.Context, p apiclient.ListSchedulesParams) (apiclient.PaginatedResponse[apiclient.Schedule], error)
	GetSchedule(ctx context.Context, id string) (apiclient.Schedule, error)
	CreateSchedule(ctx context.Context, req apiclient.CreateScheduleRequest) (apiclient.Schedule, error)
	UpdateSchedule(ctx context.Context, id string, req apiclient.UpdateScheduleRequest) (apiclient.Schedule, error)
	DeleteSchedule(ctx context.Context, id string) error
	PauseSchedule(ctx context.Context, id string) (apiclient.Schedule, error)
	ResumeSchedule(ctx context.Context, id string) (apiclient.Schedule, error)
	UpcomingSchedules(ctx context.Context, p apiclient.UpcomingParams) (apiclient.UpcomingResponse, error)
	BulkSchedules(ctx context.Context, req apiclient.BulkScheduleRequest) (apiclient.BulkScheduleResponse, error)
}

// scheduleClientFactory is swapped in tests to return a stub. In
// production it builds an apiclient.Client from the resolved config.
var scheduleClientFactory = func(cmd *cobra.Command) (scheduleClient, error) {
	flagURL, _ := cmd.Flags().GetString("daemon-url")
	cfg := common.ResolveAPIConfig(flagURL)
	return apiclient.New(cfg.BaseURL, apiclient.WithAuthToken(cfg.AuthToken)), nil
}

var scheduleCmd = &cobra.Command{
	Use:   "schedule",
	Short: "Manage first-class scan schedules",
	Long:  "Create, list, and pause scan schedules attached to specific targets. Talks to the surfbot daemon over HTTP.",
}

var scheduleListCmd = &cobra.Command{
	Use:   "list",
	Short: "List schedules",
	RunE:  runScheduleList,
}

var scheduleShowCmd = &cobra.Command{
	Use:   "show <id>",
	Short: "Show a single schedule",
	Args:  cobra.ExactArgs(1),
	RunE:  runScheduleShow,
}

var scheduleCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a schedule",
	RunE:  runScheduleCreate,
}

var scheduleUpdateCmd = &cobra.Command{
	Use:   "update <id>",
	Short: "Update a schedule",
	Args:  cobra.ExactArgs(1),
	RunE:  runScheduleUpdate,
}

var scheduleDeleteCmd = &cobra.Command{
	Use:   "delete <id>",
	Short: "Delete a schedule (hard delete)",
	Args:  cobra.ExactArgs(1),
	RunE:  runScheduleDelete,
}

var schedulePauseCmd = &cobra.Command{
	Use:   "pause <id>",
	Short: "Pause a schedule",
	Args:  cobra.ExactArgs(1),
	RunE:  runSchedulePause,
}

var scheduleResumeCmd = &cobra.Command{
	Use:   "resume <id>",
	Short: "Resume a paused schedule",
	Args:  cobra.ExactArgs(1),
	RunE:  runScheduleResume,
}

var scheduleUpcomingCmd = &cobra.Command{
	Use:   "upcoming",
	Short: "Show upcoming schedule firings",
	RunE:  runScheduleUpcoming,
}

var scheduleBulkCmd = &cobra.Command{
	Use:   "bulk <operation> <id>...",
	Short: "Run a bulk operation (pause|resume|delete) across schedules",
	Args:  cobra.MinimumNArgs(2),
	RunE:  runScheduleBulk,
}

func init() {
	scheduleCmd.PersistentFlags().String("daemon-url", "", "Base URL of the surfbot daemon (default: $SURFBOT_DAEMON_URL or http://127.0.0.1:8470)")
	scheduleCmd.PersistentFlags().StringP("output", "o", "table", "Output format: table|json|yaml")

	// list
	scheduleListCmd.Flags().String("status", "", "Filter by status (active|paused)")
	scheduleListCmd.Flags().String("target", "", "Filter by target id")
	scheduleListCmd.Flags().String("template", "", "Filter by template id")
	scheduleListCmd.Flags().Int("limit", 50, "Max rows per page")
	scheduleListCmd.Flags().Int("offset", 0, "Row offset for pagination")

	// create
	scheduleCreateCmd.Flags().String("target", "", "Target id (required)")
	scheduleCreateCmd.Flags().String("template", "", "Template id (optional)")
	scheduleCreateCmd.Flags().String("name", "", "Schedule name (required)")
	scheduleCreateCmd.Flags().String("rrule", "", "RFC-5545 RRULE (required)")
	scheduleCreateCmd.Flags().String("dtstart", "", "RFC3339 DTSTART timestamp (default: now)")
	scheduleCreateCmd.Flags().String("tzid", "UTC", "IANA timezone")
	scheduleCreateCmd.Flags().Int("estimated-duration", 0, "Estimated scan duration in seconds (for overlap check)")

	// update
	scheduleUpdateCmd.Flags().String("name", "", "")
	scheduleUpdateCmd.Flags().String("rrule", "", "")
	scheduleUpdateCmd.Flags().String("dtstart", "", "")
	scheduleUpdateCmd.Flags().String("tzid", "", "")
	scheduleUpdateCmd.Flags().String("template", "", "")
	scheduleUpdateCmd.Flags().String("status", "", "active|paused")

	// delete
	scheduleDeleteCmd.Flags().BoolP("force", "y", false, "Skip confirmation prompt")

	// upcoming
	scheduleUpcomingCmd.Flags().Duration("horizon", 24*time.Hour, "Time horizon to expand (e.g. 24h, 7d)")
	scheduleUpcomingCmd.Flags().Int("limit", 100, "Max firings to return")
	scheduleUpcomingCmd.Flags().String("target", "", "Filter by target id")

	// bulk
	scheduleBulkCmd.Flags().BoolP("force", "y", false, "Skip confirmation prompt for bulk delete")

	scheduleCmd.AddCommand(scheduleListCmd, scheduleShowCmd, scheduleCreateCmd,
		scheduleUpdateCmd, scheduleDeleteCmd, schedulePauseCmd, scheduleResumeCmd,
		scheduleUpcomingCmd, scheduleBulkCmd)
	rootCmd.AddCommand(scheduleCmd)
}

// ---- run helpers ----

func scheduleFormat(cmd *cobra.Command) (common.OutputFormat, error) {
	if jsonOut {
		return common.FormatJSON, nil
	}
	raw, _ := cmd.Flags().GetString("output")
	return common.ParseOutputFormat(raw)
}

func scheduleExit(cmd *cobra.Command, err error) error {
	format, _ := scheduleFormat(cmd)
	code := common.HandleAPIError(err, format, cmd.ErrOrStderr())
	if code == common.ExitOK {
		return nil
	}
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	return errExit(code)
}

// ---- list ----

func runScheduleList(cmd *cobra.Command, _ []string) error {
	c, err := scheduleClientFactory(cmd)
	if err != nil {
		return scheduleExit(cmd, err)
	}
	status, _ := cmd.Flags().GetString("status")
	target, _ := cmd.Flags().GetString("target")
	tmpl, _ := cmd.Flags().GetString("template")
	limit, _ := cmd.Flags().GetInt("limit")
	offset, _ := cmd.Flags().GetInt("offset")

	ctx := context.Background()
	page, err := c.ListSchedules(ctx, apiclient.ListSchedulesParams{
		Status: status, TargetID: target, TemplateID: tmpl,
		Limit: limit, Offset: offset,
	})
	if err != nil {
		return scheduleExit(cmd, err)
	}
	format, _ := scheduleFormat(cmd)
	return common.Render(cmd.OutOrStdout(), format, page, func(w io.Writer) error {
		return renderScheduleTable(w, page.Items)
	})
}

func renderScheduleTable(w io.Writer, items []apiclient.Schedule) error {
	tw := common.NewTable(w)
	fmt.Fprintln(tw, "ID\tTARGET\tTEMPLATE\tSTATUS\tRRULE\tNEXT RUN")
	for _, s := range items {
		tmpl := ""
		if s.TemplateID != nil {
			tmpl = common.Ellipsize(*s.TemplateID, 12)
		}
		next := "—"
		if s.NextRunAt != nil {
			next = s.NextRunAt.Format(time.RFC3339)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			common.Ellipsize(s.ID, 12),
			common.Ellipsize(s.TargetID, 12),
			tmpl,
			s.Status,
			common.Ellipsize(s.RRule, 40),
			next,
		)
	}
	return tw.Flush()
}

// ---- show ----

func runScheduleShow(cmd *cobra.Command, args []string) error {
	c, err := scheduleClientFactory(cmd)
	if err != nil {
		return scheduleExit(cmd, err)
	}
	s, err := c.GetSchedule(context.Background(), args[0])
	if err != nil {
		return scheduleExit(cmd, err)
	}
	format, _ := scheduleFormat(cmd)
	return common.Render(cmd.OutOrStdout(), format, s, func(w io.Writer) error {
		return renderScheduleDetail(w, s)
	})
}

func renderScheduleDetail(w io.Writer, s apiclient.Schedule) error {
	tw := common.NewTable(w)
	defer func() { _ = tw.Flush() }()
	fmt.Fprintf(tw, "ID:\t%s\n", s.ID)
	fmt.Fprintf(tw, "Name:\t%s\n", s.Name)
	fmt.Fprintf(tw, "Target:\t%s\n", s.TargetID)
	if s.TemplateID != nil {
		fmt.Fprintf(tw, "Template:\t%s\n", *s.TemplateID)
	}
	fmt.Fprintf(tw, "Status:\t%s\n", s.Status)
	fmt.Fprintf(tw, "RRULE:\t%s\n", s.RRule)
	fmt.Fprintf(tw, "DTStart:\t%s\n", s.DTStart.Format(time.RFC3339))
	fmt.Fprintf(tw, "Timezone:\t%s\n", s.Timezone)
	if s.NextRunAt != nil {
		fmt.Fprintf(tw, "Next run:\t%s\n", s.NextRunAt.Format(time.RFC3339))
	}
	if s.LastRunAt != nil {
		status := "?"
		if s.LastRunStatus != nil {
			status = *s.LastRunStatus
		}
		fmt.Fprintf(tw, "Last run:\t%s (%s)\n", s.LastRunAt.Format(time.RFC3339), status)
	}
	return nil
}

// ---- create ----

func runScheduleCreate(cmd *cobra.Command, _ []string) error {
	c, err := scheduleClientFactory(cmd)
	if err != nil {
		return scheduleExit(cmd, err)
	}
	target, _ := cmd.Flags().GetString("target")
	name, _ := cmd.Flags().GetString("name")
	rrule, _ := cmd.Flags().GetString("rrule")
	tzid, _ := cmd.Flags().GetString("tzid")
	if target == "" || name == "" || rrule == "" {
		cmd.SilenceUsage = false
		return errors.New("--target, --name, and --rrule are required")
	}
	dtstart, err := parseOptionalRFC3339(cmd, "dtstart")
	if err != nil {
		return scheduleExit(cmd, err)
	}
	if dtstart == nil {
		now := time.Now().UTC()
		dtstart = &now
	}
	req := apiclient.CreateScheduleRequest{
		TargetID: target, Name: name, RRule: rrule,
		Timezone: tzid, DTStart: *dtstart,
	}
	if t, _ := cmd.Flags().GetString("template"); t != "" {
		req.TemplateID = &t
	}
	if d, _ := cmd.Flags().GetInt("estimated-duration"); d > 0 {
		req.EstimatedDurationSeconds = d
	}
	created, err := c.CreateSchedule(context.Background(), req)
	if err != nil {
		return scheduleExit(cmd, err)
	}
	format, _ := scheduleFormat(cmd)
	return common.Render(cmd.OutOrStdout(), format, created, func(w io.Writer) error {
		return renderScheduleDetail(w, created)
	})
}

// ---- update ----

func runScheduleUpdate(cmd *cobra.Command, args []string) error {
	c, err := scheduleClientFactory(cmd)
	if err != nil {
		return scheduleExit(cmd, err)
	}
	var req apiclient.UpdateScheduleRequest
	if cmd.Flags().Changed("name") {
		v, _ := cmd.Flags().GetString("name")
		req.Name = &v
	}
	if cmd.Flags().Changed("rrule") {
		v, _ := cmd.Flags().GetString("rrule")
		req.RRule = &v
	}
	if cmd.Flags().Changed("tzid") {
		v, _ := cmd.Flags().GetString("tzid")
		req.Timezone = &v
	}
	if cmd.Flags().Changed("template") {
		v, _ := cmd.Flags().GetString("template")
		if v == "" {
			req.ClearTemplate = true
		} else {
			req.TemplateID = &v
		}
	}
	if cmd.Flags().Changed("dtstart") {
		dt, perr := parseOptionalRFC3339(cmd, "dtstart")
		if perr != nil {
			return scheduleExit(cmd, perr)
		}
		req.DTStart = dt
	}
	if cmd.Flags().Changed("status") {
		status, _ := cmd.Flags().GetString("status")
		enabled := status == "active"
		if status != "active" && status != "paused" {
			return scheduleExit(cmd, fmt.Errorf("--status must be active or paused"))
		}
		req.Enabled = &enabled
	}
	updated, err := c.UpdateSchedule(context.Background(), args[0], req)
	if err != nil {
		return scheduleExit(cmd, err)
	}
	format, _ := scheduleFormat(cmd)
	return common.Render(cmd.OutOrStdout(), format, updated, func(w io.Writer) error {
		return renderScheduleDetail(w, updated)
	})
}

// ---- delete ----

func runScheduleDelete(cmd *cobra.Command, args []string) error {
	id := args[0]
	force, _ := cmd.Flags().GetBool("force")
	prompt := fmt.Sprintf("Delete schedule %s? This cannot be undone. Type 'yes': ", id)
	if !common.ConfirmDestructive(os.Stdin, cmd.OutOrStdout(), prompt, force) {
		cmd.SilenceUsage = true
		fmt.Fprintln(cmd.ErrOrStderr(), "aborted (pass --force/-y to confirm non-interactively)")
		return errExit(common.ExitValidation)
	}
	c, err := scheduleClientFactory(cmd)
	if err != nil {
		return scheduleExit(cmd, err)
	}
	if err := c.DeleteSchedule(context.Background(), id); err != nil {
		return scheduleExit(cmd, err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "deleted %s\n", id)
	return nil
}

// ---- pause / resume ----

func runSchedulePause(cmd *cobra.Command, args []string) error {
	c, err := scheduleClientFactory(cmd)
	if err != nil {
		return scheduleExit(cmd, err)
	}
	s, err := c.PauseSchedule(context.Background(), args[0])
	if err != nil {
		return scheduleExit(cmd, err)
	}
	format, _ := scheduleFormat(cmd)
	return common.Render(cmd.OutOrStdout(), format, s, func(w io.Writer) error {
		return renderScheduleDetail(w, s)
	})
}

func runScheduleResume(cmd *cobra.Command, args []string) error {
	c, err := scheduleClientFactory(cmd)
	if err != nil {
		return scheduleExit(cmd, err)
	}
	s, err := c.ResumeSchedule(context.Background(), args[0])
	if err != nil {
		return scheduleExit(cmd, err)
	}
	format, _ := scheduleFormat(cmd)
	return common.Render(cmd.OutOrStdout(), format, s, func(w io.Writer) error {
		return renderScheduleDetail(w, s)
	})
}

// ---- upcoming ----

func runScheduleUpcoming(cmd *cobra.Command, _ []string) error {
	c, err := scheduleClientFactory(cmd)
	if err != nil {
		return scheduleExit(cmd, err)
	}
	horizon, _ := cmd.Flags().GetDuration("horizon")
	limit, _ := cmd.Flags().GetInt("limit")
	target, _ := cmd.Flags().GetString("target")
	up, err := c.UpcomingSchedules(context.Background(), apiclient.UpcomingParams{
		Horizon: horizon, Limit: limit, TargetID: target,
	})
	if err != nil {
		return scheduleExit(cmd, err)
	}
	format, _ := scheduleFormat(cmd)
	return common.Render(cmd.OutOrStdout(), format, up, func(w io.Writer) error {
		tw := common.NewTable(w)
		fmt.Fprintln(tw, "SCHEDULE\tTARGET\tFIRES AT")
		for _, f := range up.Items {
			fmt.Fprintf(tw, "%s\t%s\t%s\n",
				common.Ellipsize(f.ScheduleID, 12),
				common.Ellipsize(f.TargetID, 16),
				f.FiresAt.Format(time.RFC3339))
		}
		if err := tw.Flush(); err != nil {
			return err
		}
		if len(up.BlackoutsInHorizon) > 0 {
			fmt.Fprintf(w, "\nBlackouts in horizon: %d\n", len(up.BlackoutsInHorizon))
		}
		return nil
	})
}

// ---- bulk ----

func runScheduleBulk(cmd *cobra.Command, args []string) error {
	op := args[0]
	ids := args[1:]
	switch op {
	case "pause", "resume", "delete":
	default:
		return scheduleExit(cmd, fmt.Errorf("unknown bulk operation %q (expected pause|resume|delete)", op))
	}
	if op == "delete" {
		force, _ := cmd.Flags().GetBool("force")
		prompt := fmt.Sprintf("Bulk delete %d schedule(s)? Type 'yes': ", len(ids))
		if !common.ConfirmDestructive(os.Stdin, cmd.OutOrStdout(), prompt, force) {
			cmd.SilenceUsage = true
			fmt.Fprintln(cmd.ErrOrStderr(), "aborted (pass --force/-y to confirm non-interactively)")
			return errExit(common.ExitValidation)
		}
	}
	c, err := scheduleClientFactory(cmd)
	if err != nil {
		return scheduleExit(cmd, err)
	}
	out, err := c.BulkSchedules(context.Background(), apiclient.BulkScheduleRequest{
		Operation: op, ScheduleIDs: ids,
	})
	if err != nil {
		return scheduleExit(cmd, err)
	}
	format, _ := scheduleFormat(cmd)
	return common.Render(cmd.OutOrStdout(), format, out, func(w io.Writer) error {
		fmt.Fprintf(w, "operation: %s\nsucceeded: %d\nfailed: %d\n",
			out.Operation, len(out.Succeeded), len(out.Failed))
		for _, f := range out.Failed {
			fmt.Fprintf(w, "  - %s: %s\n", f.ScheduleID, f.Error)
		}
		return nil
	})
}

// ---- helpers ----

func parseOptionalRFC3339(cmd *cobra.Command, flag string) (*time.Time, error) {
	raw, _ := cmd.Flags().GetString(flag)
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, fmt.Errorf("--%s must be RFC3339: %w", flag, err)
	}
	return &t, nil
}

// silence unused import guards.
var _ = json.Marshal
var _ = strings.Contains
var _ = io.Discard

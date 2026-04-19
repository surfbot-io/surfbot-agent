package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/surfbot-io/surfbot-agent/internal/cli/apiclient"
	"github.com/surfbot-io/surfbot-agent/internal/cli/common"
)

type blackoutClient interface {
	ListBlackouts(ctx context.Context, activeAt string, limit, offset int) (apiclient.PaginatedResponse[apiclient.Blackout], error)
	GetBlackout(ctx context.Context, id string) (apiclient.Blackout, error)
	CreateBlackout(ctx context.Context, req apiclient.CreateBlackoutRequest) (apiclient.Blackout, error)
	UpdateBlackout(ctx context.Context, id string, req apiclient.UpdateBlackoutRequest) (apiclient.Blackout, error)
	DeleteBlackout(ctx context.Context, id string) error
}

var blackoutClientFactory = func(cmd *cobra.Command) (blackoutClient, error) {
	flagURL, _ := cmd.Flags().GetString("daemon-url")
	cfg := common.ResolveAPIConfig(flagURL)
	return apiclient.New(cfg.BaseURL, apiclient.WithAuthToken(cfg.AuthToken)), nil
}

var blackoutCmd = &cobra.Command{
	Use:   "blackout",
	Short: "Manage blackout windows",
	Long:  "Define recurring periods when scans must not run. Windows are either global (every target) or scoped to a single target.",
}

var blackoutListCmd = &cobra.Command{
	Use:   "list",
	Short: "List blackouts",
	Long:  "Print blackout windows. Pass --active-at <rfc3339> to filter down to windows active at a given instant.",
	RunE:  runBlackoutList,
}

var blackoutShowCmd = &cobra.Command{
	Use:   "show <id>",
	Short: "Show a blackout",
	Long:  "Fetch a blackout by id and print its recurrence rule, duration, and scope.",
	Args:  cobra.ExactArgs(1),
	RunE:  runBlackoutShow,
}

var blackoutCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a blackout",
	Long:  "Create a blackout window. Leave --target-id empty for a global blackout; set it to scope to a single target.",
	RunE:  runBlackoutCreate,
}

var blackoutUpdateCmd = &cobra.Command{
	Use:   "update <id>",
	Short: "Update a blackout",
	Long:  "Patch selected blackout fields.",
	Args:  cobra.ExactArgs(1),
	RunE:  runBlackoutUpdate,
}

var blackoutDeleteCmd = &cobra.Command{
	Use:   "delete <id>",
	Short: "Delete a blackout",
	Long:  "Delete a blackout. Prompts for confirmation in a TTY; pass --force/-y to skip.",
	Args:  cobra.ExactArgs(1),
	RunE:  runBlackoutDelete,
}

func init() {
	blackoutCmd.PersistentFlags().String("daemon-url", "", "Base URL of the surfbot daemon")
	blackoutCmd.PersistentFlags().StringP("output", "o", "table", "Output format: table|json|yaml")

	blackoutListCmd.Flags().String("active-at", "", "RFC3339 instant — only return windows active at this time")
	blackoutListCmd.Flags().Int("limit", 50, "")
	blackoutListCmd.Flags().Int("offset", 0, "")

	blackoutCreateCmd.Flags().String("name", "", "Blackout name (required)")
	blackoutCreateCmd.Flags().String("rrule", "", "RFC-5545 RRULE for window starts (required)")
	blackoutCreateCmd.Flags().String("dtstart", "", "RFC3339 DTSTART (default: now)")
	blackoutCreateCmd.Flags().String("tzid", "UTC", "IANA timezone")
	blackoutCreateCmd.Flags().Duration("duration", 0, "Window duration (e.g. 8h, 30m) — required")
	blackoutCreateCmd.Flags().String("target-id", "", "Target id (leave empty for global scope)")

	blackoutUpdateCmd.Flags().String("name", "", "")
	blackoutUpdateCmd.Flags().String("rrule", "", "")
	blackoutUpdateCmd.Flags().String("tzid", "", "")
	blackoutUpdateCmd.Flags().Duration("duration", 0, "")
	blackoutUpdateCmd.Flags().String("target-id", "", "")
	blackoutUpdateCmd.Flags().Bool("clear-target", false, "Clear target (switch to global)")

	blackoutDeleteCmd.Flags().BoolP("force", "y", false, "Skip confirmation prompt")

	blackoutCmd.AddCommand(blackoutListCmd, blackoutShowCmd, blackoutCreateCmd,
		blackoutUpdateCmd, blackoutDeleteCmd)
	rootCmd.AddCommand(blackoutCmd)
}

func blackoutFormat(cmd *cobra.Command) (common.OutputFormat, error) {
	if jsonOut {
		return common.FormatJSON, nil
	}
	raw, _ := cmd.Flags().GetString("output")
	return common.ParseOutputFormat(raw)
}

func blackoutExit(cmd *cobra.Command, err error) error {
	format, _ := blackoutFormat(cmd)
	code := common.HandleAPIError(err, format, cmd.ErrOrStderr())
	if code == common.ExitOK {
		return nil
	}
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	return errExit(code)
}

func runBlackoutList(cmd *cobra.Command, _ []string) error {
	c, err := blackoutClientFactory(cmd)
	if err != nil {
		return blackoutExit(cmd, err)
	}
	at, _ := cmd.Flags().GetString("active-at")
	limit, _ := cmd.Flags().GetInt("limit")
	offset, _ := cmd.Flags().GetInt("offset")
	page, err := c.ListBlackouts(context.Background(), at, limit, offset)
	if err != nil {
		return blackoutExit(cmd, err)
	}
	format, _ := blackoutFormat(cmd)
	return common.Render(cmd.OutOrStdout(), format, page, func(w io.Writer) error {
		tw := common.NewTable(w)
		_, _ = fmt.Fprintln(tw, "ID\tNAME\tSCOPE\tTARGET\tRRULE\tDURATION\tENABLED")
		for _, b := range page.Items {
			target := "—"
			if b.TargetID != nil {
				target = common.Ellipsize(*b.TargetID, 12)
			}
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%t\n",
				common.Ellipsize(b.ID, 12),
				common.Ellipsize(b.Name, 24),
				b.Scope,
				target,
				common.Ellipsize(b.RRule, 32),
				time.Duration(b.DurationSeconds)*time.Second,
				b.Enabled,
			)
		}
		return tw.Flush()
	})
}

func runBlackoutShow(cmd *cobra.Command, args []string) error {
	c, err := blackoutClientFactory(cmd)
	if err != nil {
		return blackoutExit(cmd, err)
	}
	b, err := c.GetBlackout(context.Background(), args[0])
	if err != nil {
		return blackoutExit(cmd, err)
	}
	format, _ := blackoutFormat(cmd)
	return common.Render(cmd.OutOrStdout(), format, b, func(w io.Writer) error {
		return renderBlackoutDetail(w, b)
	})
}

func renderBlackoutDetail(w io.Writer, b apiclient.Blackout) error {
	tw := common.NewTable(w)
	defer func() { _ = tw.Flush() }()
	_, _ = fmt.Fprintf(tw, "ID:\t%s\n", b.ID)
	_, _ = fmt.Fprintf(tw, "Name:\t%s\n", b.Name)
	_, _ = fmt.Fprintf(tw, "Scope:\t%s\n", b.Scope)
	if b.TargetID != nil {
		_, _ = fmt.Fprintf(tw, "Target:\t%s\n", *b.TargetID)
	}
	_, _ = fmt.Fprintf(tw, "RRULE:\t%s\n", b.RRule)
	_, _ = fmt.Fprintf(tw, "Duration:\t%s\n", time.Duration(b.DurationSeconds)*time.Second)
	_, _ = fmt.Fprintf(tw, "Timezone:\t%s\n", b.Timezone)
	_, _ = fmt.Fprintf(tw, "Enabled:\t%t\n", b.Enabled)
	return nil
}

func runBlackoutCreate(cmd *cobra.Command, _ []string) error {
	name, _ := cmd.Flags().GetString("name")
	rrule, _ := cmd.Flags().GetString("rrule")
	dur, _ := cmd.Flags().GetDuration("duration")
	if name == "" || rrule == "" || dur <= 0 {
		return errors.New("--name, --rrule, and --duration are required")
	}
	tgt, _ := cmd.Flags().GetString("target-id")
	tz, _ := cmd.Flags().GetString("tzid")
	req := apiclient.CreateBlackoutRequest{
		Name:            name,
		RRule:           rrule,
		DurationSeconds: int(dur / time.Second),
		Timezone:        tz,
	}
	if tgt != "" {
		req.Scope = "target"
		req.TargetID = &tgt
	} else {
		req.Scope = "global"
	}
	c, err := blackoutClientFactory(cmd)
	if err != nil {
		return blackoutExit(cmd, err)
	}
	created, err := c.CreateBlackout(context.Background(), req)
	if err != nil {
		return blackoutExit(cmd, err)
	}
	format, _ := blackoutFormat(cmd)
	return common.Render(cmd.OutOrStdout(), format, created, func(w io.Writer) error {
		return renderBlackoutDetail(w, created)
	})
}

func runBlackoutUpdate(cmd *cobra.Command, args []string) error {
	c, err := blackoutClientFactory(cmd)
	if err != nil {
		return blackoutExit(cmd, err)
	}
	var req apiclient.UpdateBlackoutRequest
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
	if cmd.Flags().Changed("duration") {
		d, _ := cmd.Flags().GetDuration("duration")
		secs := int(d / time.Second)
		req.DurationSeconds = &secs
	}
	if cmd.Flags().Changed("target-id") {
		v, _ := cmd.Flags().GetString("target-id")
		req.TargetID = &v
	}
	if clear, _ := cmd.Flags().GetBool("clear-target"); clear {
		req.ClearTarget = true
	}
	updated, err := c.UpdateBlackout(context.Background(), args[0], req)
	if err != nil {
		return blackoutExit(cmd, err)
	}
	format, _ := blackoutFormat(cmd)
	return common.Render(cmd.OutOrStdout(), format, updated, func(w io.Writer) error {
		return renderBlackoutDetail(w, updated)
	})
}

func runBlackoutDelete(cmd *cobra.Command, args []string) error {
	id := args[0]
	force, _ := cmd.Flags().GetBool("force")
	prompt := fmt.Sprintf("Delete blackout %s? Type 'yes': ", id)
	if !common.ConfirmDestructive(os.Stdin, cmd.OutOrStdout(), prompt, force) {
		cmd.SilenceUsage = true
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "aborted (pass --force/-y to confirm non-interactively)")
		return errExit(common.ExitValidation)
	}
	c, err := blackoutClientFactory(cmd)
	if err != nil {
		return blackoutExit(cmd, err)
	}
	if err := c.DeleteBlackout(context.Background(), id); err != nil {
		return blackoutExit(cmd, err)
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "deleted %s\n", id)
	return nil
}

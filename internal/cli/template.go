package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/surfbot-io/surfbot-agent/internal/cli/apiclient"
	"github.com/surfbot-io/surfbot-agent/internal/cli/common"
)

type templateClient interface {
	ListTemplates(ctx context.Context, limit, offset int) (apiclient.PaginatedResponse[apiclient.Template], error)
	GetTemplate(ctx context.Context, id string) (apiclient.Template, error)
	CreateTemplate(ctx context.Context, req apiclient.CreateTemplateRequest) (apiclient.Template, error)
	UpdateTemplate(ctx context.Context, id string, req apiclient.UpdateTemplateRequest) (apiclient.Template, error)
	DeleteTemplate(ctx context.Context, id string, force bool) error
}

var templateClientFactory = func(cmd *cobra.Command) (templateClient, error) {
	flagURL, _ := cmd.Flags().GetString("daemon-url")
	cfg := common.ResolveAPIConfig(flagURL)
	return apiclient.New(cfg.BaseURL, apiclient.WithAuthToken(cfg.AuthToken)), nil
}

var templateCmd = &cobra.Command{
	Use:   "template",
	Short: "Manage scan templates",
	Long:  "Create, list, and maintain reusable scan templates. Templates group tool configuration and default RRULE so many schedules can share one definition.",
}

var templateListCmd = &cobra.Command{
	Use:   "list",
	Short: "List templates",
	RunE:  runTemplateList,
}

var templateShowCmd = &cobra.Command{
	Use:   "show <id>",
	Short: "Show a template",
	Args:  cobra.ExactArgs(1),
	RunE:  runTemplateShow,
}

var templateCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a template",
	RunE:  runTemplateCreate,
}

var templateUpdateCmd = &cobra.Command{
	Use:   "update <id>",
	Short: "Update a template",
	Args:  cobra.ExactArgs(1),
	RunE:  runTemplateUpdate,
}

var templateDeleteCmd = &cobra.Command{
	Use:   "delete <id>",
	Short: "Delete a template",
	Args:  cobra.ExactArgs(1),
	RunE:  runTemplateDelete,
}

func init() {
	templateCmd.PersistentFlags().String("daemon-url", "", "Base URL of the surfbot daemon")
	templateCmd.PersistentFlags().StringP("output", "o", "table", "Output format: table|json|yaml")

	templateListCmd.Flags().Int("limit", 50, "Max rows per page")
	templateListCmd.Flags().Int("offset", 0, "Offset for pagination")

	templateCreateCmd.Flags().String("name", "", "Template name (required)")
	templateCreateCmd.Flags().String("description", "", "Free-form description")
	templateCreateCmd.Flags().String("rrule", "", "Default RRULE for dependent schedules")
	templateCreateCmd.Flags().String("tzid", "UTC", "IANA timezone")
	templateCreateCmd.Flags().String("tool-config", "", "Path to a JSON file containing tool_config")
	templateCreateCmd.Flags().String("tool-config-inline", "", "Inline JSON for tool_config")

	templateUpdateCmd.Flags().String("name", "", "")
	templateUpdateCmd.Flags().String("description", "", "")
	templateUpdateCmd.Flags().String("rrule", "", "")
	templateUpdateCmd.Flags().String("tzid", "", "")
	templateUpdateCmd.Flags().String("tool-config", "", "")
	templateUpdateCmd.Flags().String("tool-config-inline", "", "")

	templateDeleteCmd.Flags().BoolP("force", "y", false, "Skip confirmation prompt")
	templateDeleteCmd.Flags().Bool("cascade", false, "Delete dependent schedules atomically on the server (?force=true)")

	templateCmd.AddCommand(templateListCmd, templateShowCmd, templateCreateCmd,
		templateUpdateCmd, templateDeleteCmd)
	rootCmd.AddCommand(templateCmd)
}

func templateFormat(cmd *cobra.Command) (common.OutputFormat, error) {
	if jsonOut {
		return common.FormatJSON, nil
	}
	raw, _ := cmd.Flags().GetString("output")
	return common.ParseOutputFormat(raw)
}

func templateExit(cmd *cobra.Command, err error) error {
	format, _ := templateFormat(cmd)
	code := common.HandleAPIError(err, format, cmd.ErrOrStderr())
	if code == common.ExitOK {
		return nil
	}
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	return errExit(code)
}

func runTemplateList(cmd *cobra.Command, _ []string) error {
	c, err := templateClientFactory(cmd)
	if err != nil {
		return templateExit(cmd, err)
	}
	limit, _ := cmd.Flags().GetInt("limit")
	offset, _ := cmd.Flags().GetInt("offset")
	page, err := c.ListTemplates(context.Background(), limit, offset)
	if err != nil {
		return templateExit(cmd, err)
	}
	format, _ := templateFormat(cmd)
	return common.Render(cmd.OutOrStdout(), format, page, func(w io.Writer) error {
		tw := common.NewTable(w)
		fmt.Fprintln(tw, "ID\tNAME\tRRULE\tTZ\tUPDATED")
		for _, t := range page.Items {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
				common.Ellipsize(t.ID, 12),
				common.Ellipsize(t.Name, 24),
				common.Ellipsize(t.RRule, 32),
				t.Timezone,
				t.UpdatedAt.Format(time.RFC3339))
		}
		return tw.Flush()
	})
}

func runTemplateShow(cmd *cobra.Command, args []string) error {
	c, err := templateClientFactory(cmd)
	if err != nil {
		return templateExit(cmd, err)
	}
	t, err := c.GetTemplate(context.Background(), args[0])
	if err != nil {
		return templateExit(cmd, err)
	}
	format, _ := templateFormat(cmd)
	return common.Render(cmd.OutOrStdout(), format, t, func(w io.Writer) error {
		return renderTemplateDetail(w, t)
	})
}

func renderTemplateDetail(w io.Writer, t apiclient.Template) error {
	tw := common.NewTable(w)
	defer func() { _ = tw.Flush() }()
	fmt.Fprintf(tw, "ID:\t%s\n", t.ID)
	fmt.Fprintf(tw, "Name:\t%s\n", t.Name)
	fmt.Fprintf(tw, "Description:\t%s\n", t.Description)
	fmt.Fprintf(tw, "RRULE:\t%s\n", t.RRule)
	fmt.Fprintf(tw, "Timezone:\t%s\n", t.Timezone)
	fmt.Fprintf(tw, "IsSystem:\t%t\n", t.IsSystem)
	fmt.Fprintf(tw, "Updated:\t%s\n", t.UpdatedAt.Format(time.RFC3339))
	if len(t.ToolConfig) > 0 {
		raw, _ := json.Marshal(t.ToolConfig)
		fmt.Fprintf(tw, "ToolConfig:\t%s\n", string(raw))
	}
	return nil
}

func runTemplateCreate(cmd *cobra.Command, _ []string) error {
	name, _ := cmd.Flags().GetString("name")
	rrule, _ := cmd.Flags().GetString("rrule")
	if name == "" || rrule == "" {
		return errors.New("--name and --rrule are required")
	}
	tc, err := readToolConfigFlags(cmd)
	if err != nil {
		cmd.SilenceUsage = true
		fmt.Fprintln(cmd.ErrOrStderr(), err)
		return errExit(common.ExitValidation)
	}
	desc, _ := cmd.Flags().GetString("description")
	tz, _ := cmd.Flags().GetString("tzid")
	req := apiclient.CreateTemplateRequest{
		Name: name, Description: desc, RRule: rrule,
		Timezone: tz, ToolConfig: tc,
	}
	c, err := templateClientFactory(cmd)
	if err != nil {
		return templateExit(cmd, err)
	}
	created, err := c.CreateTemplate(context.Background(), req)
	if err != nil {
		return templateExit(cmd, err)
	}
	format, _ := templateFormat(cmd)
	return common.Render(cmd.OutOrStdout(), format, created, func(w io.Writer) error {
		return renderTemplateDetail(w, created)
	})
}

func runTemplateUpdate(cmd *cobra.Command, args []string) error {
	c, err := templateClientFactory(cmd)
	if err != nil {
		return templateExit(cmd, err)
	}
	var req apiclient.UpdateTemplateRequest
	if cmd.Flags().Changed("name") {
		v, _ := cmd.Flags().GetString("name")
		req.Name = &v
	}
	if cmd.Flags().Changed("description") {
		v, _ := cmd.Flags().GetString("description")
		req.Description = &v
	}
	if cmd.Flags().Changed("rrule") {
		v, _ := cmd.Flags().GetString("rrule")
		req.RRule = &v
	}
	if cmd.Flags().Changed("tzid") {
		v, _ := cmd.Flags().GetString("tzid")
		req.Timezone = &v
	}
	if cmd.Flags().Changed("tool-config") || cmd.Flags().Changed("tool-config-inline") {
		tc, terr := readToolConfigFlags(cmd)
		if terr != nil {
			cmd.SilenceUsage = true
			fmt.Fprintln(cmd.ErrOrStderr(), terr)
			return errExit(common.ExitValidation)
		}
		req.ToolConfig = tc
	}
	updated, err := c.UpdateTemplate(context.Background(), args[0], req)
	if err != nil {
		return templateExit(cmd, err)
	}
	format, _ := templateFormat(cmd)
	return common.Render(cmd.OutOrStdout(), format, updated, func(w io.Writer) error {
		return renderTemplateDetail(w, updated)
	})
}

func runTemplateDelete(cmd *cobra.Command, args []string) error {
	id := args[0]
	force, _ := cmd.Flags().GetBool("force")
	cascade, _ := cmd.Flags().GetBool("cascade")
	prompt := fmt.Sprintf("Delete template %s?%s Type 'yes': ", id, cascadeSuffix(cascade))
	if !common.ConfirmDestructive(os.Stdin, cmd.OutOrStdout(), prompt, force) {
		cmd.SilenceUsage = true
		fmt.Fprintln(cmd.ErrOrStderr(), "aborted (pass --force/-y to confirm non-interactively)")
		return errExit(common.ExitValidation)
	}
	c, err := templateClientFactory(cmd)
	if err != nil {
		return templateExit(cmd, err)
	}
	if err := c.DeleteTemplate(context.Background(), id, cascade); err != nil {
		return templateExit(cmd, err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "deleted %s\n", id)
	return nil
}

func cascadeSuffix(cascade bool) string {
	if cascade {
		return " Dependent schedules will be deleted too."
	}
	return ""
}

// readToolConfigFlags parses the --tool-config <file> or
// --tool-config-inline <json> flag pair. Exactly one may be set;
// neither is OK and returns nil. Returns an error if both are set
// or if the payload doesn't decode as a JSON object.
func readToolConfigFlags(cmd *cobra.Command) (map[string]any, error) {
	filePath, _ := cmd.Flags().GetString("tool-config")
	inline, _ := cmd.Flags().GetString("tool-config-inline")
	if filePath != "" && inline != "" {
		return nil, errors.New("--tool-config and --tool-config-inline are mutually exclusive")
	}
	var raw []byte
	switch {
	case filePath != "":
		b, err := os.ReadFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", filePath, err)
		}
		raw = b
	case inline != "":
		raw = []byte(inline)
	default:
		return nil, nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("tool_config must be a JSON object: %w", err)
	}
	return out, nil
}

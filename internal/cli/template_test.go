package cli

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/surfbot-io/surfbot-agent/internal/cli/apiclient"
)

type stubTemplateClient struct {
	list         apiclient.PaginatedResponse[apiclient.Template]
	listErr      error
	get          apiclient.Template
	getErr       error
	created      apiclient.Template
	createErr    error
	updated      apiclient.Template
	updateErr    error
	deleteForce  bool
	deleteErr    error
	lastCreate   apiclient.CreateTemplateRequest
	deleteCalled bool
}

func (s *stubTemplateClient) ListTemplates(ctx context.Context, limit, offset int) (apiclient.PaginatedResponse[apiclient.Template], error) {
	return s.list, s.listErr
}
func (s *stubTemplateClient) GetTemplate(ctx context.Context, id string) (apiclient.Template, error) {
	return s.get, s.getErr
}
func (s *stubTemplateClient) CreateTemplate(ctx context.Context, req apiclient.CreateTemplateRequest) (apiclient.Template, error) {
	s.lastCreate = req
	return s.created, s.createErr
}
func (s *stubTemplateClient) UpdateTemplate(ctx context.Context, id string, req apiclient.UpdateTemplateRequest) (apiclient.Template, error) {
	return s.updated, s.updateErr
}
func (s *stubTemplateClient) DeleteTemplate(ctx context.Context, id string, force bool) error {
	s.deleteCalled = true
	s.deleteForce = force
	return s.deleteErr
}

func withStubTemplateClient(t *testing.T, stub *stubTemplateClient) {
	t.Helper()
	prev := templateClientFactory
	templateClientFactory = func(cmd *cobra.Command) (templateClient, error) { return stub, nil }
	t.Cleanup(func() { templateClientFactory = prev })
}

func runCLI(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	prevJSON := jsonOut
	jsonOut = false
	resetCobraFlags(rootCmd)
	var out, errBuf bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errBuf)
	rootCmd.SetArgs(args)
	t.Cleanup(func() {
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		rootCmd.SetArgs(nil)
		jsonOut = prevJSON
	})
	err := rootCmd.Execute()
	return out.String(), errBuf.String(), err
}

// resetCobraFlags walks every flag under cmd (including subcommands)
// and restores the flag value to its default. Cobra does not clear
// parsed values between Execute calls, so leftover values leak
// between tests — this helper keeps each test isolated.
func resetCobraFlags(cmd *cobra.Command) {
	walk := func(c *cobra.Command) {
		c.Flags().VisitAll(func(f *pflag.Flag) {
			_ = f.Value.Set(f.DefValue)
			f.Changed = false
		})
		c.PersistentFlags().VisitAll(func(f *pflag.Flag) {
			_ = f.Value.Set(f.DefValue)
			f.Changed = false
		})
	}
	walk(cmd)
	for _, sub := range cmd.Commands() {
		resetCobraFlags(sub)
	}
}

func TestTemplateListRendersTable(t *testing.T) {
	withStubTemplateClient(t, &stubTemplateClient{
		list: apiclient.PaginatedResponse[apiclient.Template]{
			Items: []apiclient.Template{{ID: "tmpl_1", Name: "nightly", RRule: "FREQ=DAILY"}},
		},
	})
	out, _, err := runCLI(t, "template", "list")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(out, "nightly") {
		t.Fatalf("output: %s", out)
	}
}

func TestTemplateCreateRejectsMissingRequired(t *testing.T) {
	withStubTemplateClient(t, &stubTemplateClient{})
	_, _, err := runCLI(t, "template", "create")
	if err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("want required-flags error, got %v", err)
	}
}

func TestTemplateCreateWithInlineToolConfig(t *testing.T) {
	stub := &stubTemplateClient{
		created: apiclient.Template{ID: "t1", Name: "n"},
	}
	withStubTemplateClient(t, stub)
	_, _, err := runCLI(t, "template", "create",
		"--name", "n", "--rrule", "FREQ=DAILY",
		"--tool-config-inline", `{"nuclei":{"severity":["critical"]}}`)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if stub.lastCreate.ToolConfig == nil {
		t.Fatalf("tool_config not forwarded: %+v", stub.lastCreate)
	}
	if _, ok := stub.lastCreate.ToolConfig["nuclei"]; !ok {
		t.Fatalf("missing nuclei key: %+v", stub.lastCreate.ToolConfig)
	}
}

func TestTemplateCreateRejectsInvalidToolConfigJSON(t *testing.T) {
	withStubTemplateClient(t, &stubTemplateClient{})
	_, _, err := runCLI(t, "template", "create",
		"--name", "n", "--rrule", "FREQ=DAILY",
		"--tool-config-inline", `not-json`)
	var e errExit
	if !errors.As(err, &e) || int(e) != 2 {
		t.Fatalf("expected exit 2, got %v", err)
	}
}

func TestTemplateDeleteCascade(t *testing.T) {
	stub := &stubTemplateClient{}
	withStubTemplateClient(t, stub)
	out, _, err := runCLI(t, "template", "delete", "t1", "--force", "--cascade")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !stub.deleteCalled || !stub.deleteForce {
		t.Fatalf("cascade not forwarded: called=%v force=%v", stub.deleteCalled, stub.deleteForce)
	}
	if !strings.Contains(out, "deleted t1") {
		t.Fatalf("output: %s", out)
	}
}

// TestTemplateDeleteBuiltinSurfacesError covers the SCHED2.3 R3 CLI
// gate: a 409 from /problems/system-template-immutable should exit 4
// (ExitConflict) and surface the operator-readable title on stderr.
func TestTemplateDeleteBuiltinSurfacesError(t *testing.T) {
	withStubTemplateClient(t, &stubTemplateClient{
		deleteErr: &apiclient.APIError{
			StatusCode: http.StatusConflict,
			Type:       "/problems/system-template-immutable",
			Title:      "Built-in template cannot be deleted",
			Detail:     "This template is part of the built-in defaults and cannot be deleted.",
		},
	})
	_, errOut, err := runCLI(t, "template", "delete", "builtin-id", "--force")
	var e errExit
	if !errors.As(err, &e) || int(e) != 4 {
		t.Fatalf("want exit 4, got %v", err)
	}
	if !strings.Contains(errOut, "Built-in template cannot be deleted") {
		t.Fatalf("stderr missing title: %s", errOut)
	}
}

func TestTemplateDeleteConflictExits4(t *testing.T) {
	withStubTemplateClient(t, &stubTemplateClient{
		deleteErr: &apiclient.APIError{
			StatusCode: http.StatusConflict,
			Title:      "Template in use",
			FieldErrors: []apiclient.FieldError{
				{Field: "dependents", Message: "s1"},
			},
		},
	})
	_, errOut, err := runCLI(t, "template", "delete", "t1", "--force")
	var e errExit
	if !errors.As(err, &e) || int(e) != 4 {
		t.Fatalf("want exit 4, got %v", err)
	}
	if !strings.Contains(errOut, "Template in use") {
		t.Fatalf("stderr: %s", errOut)
	}
}

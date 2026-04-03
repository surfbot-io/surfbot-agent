package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/surfbot-io/surfbot-agent/internal/detection"
	"github.com/surfbot-io/surfbot-agent/internal/model"
)

// mockTool implements detection.DetectionTool for testing.
type mockTool struct {
	name        string
	phase       string
	kind        detection.ToolKind
	available   bool
	command     string
	description string
	inputType   string
	outputTypes []string
	runFunc     func(ctx context.Context, inputs []string, opts detection.RunOptions) (*detection.RunResult, error)
}

func (m *mockTool) Name() string                   { return m.name }
func (m *mockTool) Phase() string                  { return m.phase }
func (m *mockTool) Kind() detection.ToolKind       { return m.kind }
func (m *mockTool) Available() bool                { return m.available }
func (m *mockTool) Command() string                { return m.command }
func (m *mockTool) Description() string            { return m.description }
func (m *mockTool) InputType() string              { return m.inputType }
func (m *mockTool) OutputTypes() []string          { return m.outputTypes }

func (m *mockTool) Run(ctx context.Context, inputs []string, opts detection.RunOptions) (*detection.RunResult, error) {
	if m.runFunc != nil {
		return m.runFunc(ctx, inputs, opts)
	}
	return &detection.RunResult{}, nil
}

func newMockTool() *mockTool {
	return &mockTool{
		name:        "mocktool",
		phase:       "test",
		kind:        detection.ToolKindNative,
		available:   true,
		command:     "mocktest",
		description: "A mock tool for testing",
		inputType:   "domains",
		outputTypes: []string{"subdomain"},
	}
}

func TestBuildToolCommand(t *testing.T) {
	tool := newMockTool()
	cmd := buildToolCommand(tool)

	assert.Equal(t, "mocktest [inputs...]", cmd.Use)
	assert.Equal(t, "A mock tool for testing", cmd.Short)

	// Verify all expected flags exist
	assert.NotNil(t, cmd.Flags().Lookup("stdin"))
	assert.NotNil(t, cmd.Flags().Lookup("rate-limit"))
	assert.NotNil(t, cmd.Flags().Lookup("timeout"))
	assert.NotNil(t, cmd.Flags().Lookup("format"))
	assert.NotNil(t, cmd.Flags().Lookup("persist"))
	assert.NotNil(t, cmd.Flags().Lookup("target"))
	assert.NotNil(t, cmd.Flags().Lookup("extra"))

	// Verify defaults
	assert.Equal(t, "json", cmd.Flags().Lookup("format").DefValue)
	assert.Equal(t, "300", cmd.Flags().Lookup("timeout").DefValue)
	assert.Equal(t, "true", cmd.Flags().Lookup("persist").DefValue)
}

func TestCollectInputsFromArgs(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().Bool("stdin", false, "")

	inputs, err := collectInputs(cmd, []string{"a.com", "b.com"})
	require.NoError(t, err)
	assert.Equal(t, []string{"a.com", "b.com"}, inputs)
}

func TestCollectInputsFromStdin(t *testing.T) {
	input := "a.com\nb.com\n\n  c.com  \n"
	results, err := readStdin(strings.NewReader(input))
	require.NoError(t, err)
	assert.Equal(t, []string{"a.com", "b.com", "c.com"}, results)
}

func TestCollectInputsStdinEmpty(t *testing.T) {
	results, err := readStdin(strings.NewReader("\n\n  \n"))
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestCollectInputsMixed(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().Bool("stdin", false, "")

	// Args only (no stdin)
	inputs, err := collectInputs(cmd, []string{"a.com"})
	require.NoError(t, err)
	assert.Equal(t, []string{"a.com"}, inputs)
}

func TestBuildToolResult(t *testing.T) {
	tool := newMockTool()
	inputs := []string{"example.com"}
	result := &detection.RunResult{
		Assets: []model.Asset{
			{Type: model.AssetTypeSubdomain, Value: "api.example.com", Metadata: map[string]interface{}{"source": "passive"}},
			{Type: model.AssetTypeSubdomain, Value: "www.example.com"},
		},
	}
	duration := 5 * time.Second

	output := buildToolResult(tool, inputs, result, duration, nil)

	assert.Equal(t, "mocktool", output.Tool)
	assert.Equal(t, "test", output.Phase)
	assert.Equal(t, "example.com", output.Target)
	assert.Equal(t, int64(5000), output.DurationMs)
	assert.Len(t, output.Assets, 2)
	assert.Equal(t, "subdomain", output.Assets[0].Type)
	assert.Equal(t, "api.example.com", output.Assets[0].Value)
	assert.Equal(t, "passive", output.Assets[0].Metadata["source"])
	assert.Equal(t, 1, output.Stats.InputCount)
	assert.Equal(t, 2, output.Stats.OutputCount)
	assert.Empty(t, output.Errors)
}

func TestBuildToolResultWithError(t *testing.T) {
	tool := newMockTool()
	inputs := []string{"example.com"}
	result := &detection.RunResult{
		Assets: []model.Asset{
			{Type: model.AssetTypeSubdomain, Value: "partial.example.com"},
		},
	}
	duration := 2 * time.Second
	runErr := assert.AnError

	output := buildToolResult(tool, inputs, result, duration, runErr)

	assert.Len(t, output.Errors, 1)
	assert.Contains(t, output.Errors[0], "assert.AnError")
	// Assets are still populated even with error
	assert.Len(t, output.Assets, 1)
	assert.Equal(t, 1, output.Stats.OutputCount)
}

func TestBuildToolResultWithFindings(t *testing.T) {
	tool := newMockTool()
	inputs := []string{"https://example.com"}
	result := &detection.RunResult{
		Findings: []model.Finding{
			{TemplateID: "cve-2024-1234", Title: "Test CVE", Severity: model.SeverityHigh, Evidence: "https://example.com/vuln"},
		},
	}
	duration := 10 * time.Second

	output := buildToolResult(tool, inputs, result, duration, nil)

	assert.Len(t, output.Findings, 1)
	assert.Equal(t, "cve-2024-1234", output.Findings[0].TemplateID)
	assert.Equal(t, "high", output.Findings[0].Severity)
	assert.Equal(t, 1, output.Stats.OutputCount)
}

func TestBuildToolResultMultipleInputs(t *testing.T) {
	tool := newMockTool()
	inputs := []string{"a.com", "b.com"}

	output := buildToolResult(tool, inputs, &detection.RunResult{}, 1*time.Second, nil)

	// Target should be empty when multiple inputs
	assert.Empty(t, output.Target)
	assert.Equal(t, 2, output.Stats.InputCount)
}

func TestOutputResultJSON(t *testing.T) {
	output := ToolResult{
		Tool:       "subfinder",
		Phase:      "discovery",
		Target:     "example.com",
		DurationMs: 5000,
		Assets:     []AssetOutput{{Type: "subdomain", Value: "api.example.com"}},
		Findings:   []FindingOutput{},
		Stats:      ToolStats{InputCount: 1, OutputCount: 1},
		Errors:     []string{},
	}

	var buf bytes.Buffer
	err := outputResult(&buf, output, "json")
	require.NoError(t, err)

	// Verify valid JSON that round-trips
	var decoded ToolResult
	err = json.Unmarshal(buf.Bytes(), &decoded)
	require.NoError(t, err)
	assert.Equal(t, "subfinder", decoded.Tool)
	assert.Equal(t, "example.com", decoded.Target)
	assert.Len(t, decoded.Assets, 1)
	assert.Equal(t, "api.example.com", decoded.Assets[0].Value)
}

func TestOutputResultText(t *testing.T) {
	output := ToolResult{
		Tool:       "subfinder",
		Phase:      "discovery",
		DurationMs: 12300,
		Assets:     []AssetOutput{{Type: "subdomain", Value: "api.example.com"}},
		Findings:   []FindingOutput{},
		Stats:      ToolStats{InputCount: 1, OutputCount: 1},
		Errors:     []string{},
	}

	var buf bytes.Buffer
	err := outputResult(&buf, output, "text")
	require.NoError(t, err)

	text := buf.String()
	assert.Contains(t, text, "subfinder")
	assert.Contains(t, text, "12.3s")
	assert.Contains(t, text, "api.example.com")
	assert.Contains(t, text, "ASSETS (1)")
}

func TestOutputResultInvalidFormat(t *testing.T) {
	output := ToolResult{}
	var buf bytes.Buffer
	err := outputResult(&buf, output, "xml")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown format")
}

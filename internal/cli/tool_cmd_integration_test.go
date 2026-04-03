package cli

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAtomicCommandRegistration(t *testing.T) {
	expectedCommands := map[string]string{
		"discover": "subfinder",
		"resolve":  "dnsx",
		"portscan": "naabu",
		"probe":    "httpx",
		"assess":   "nuclei",
	}

	for cmdName, toolName := range expectedCommands {
		found := false
		for _, cmd := range rootCmd.Commands() {
			if cmd.Name() == cmdName {
				found = true
				assert.Contains(t, cmd.Long, toolName, "command %s should reference tool %s", cmdName, toolName)
				break
			}
		}
		assert.True(t, found, "command %q not registered on rootCmd", cmdName)
	}
}

func TestAtomicCommandNoInputsError(t *testing.T) {
	tool := newMockTool()
	cmd := buildToolCommand(tool)

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	// Execute with no args and no stdin
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no inputs provided")
}

func TestToolsListJSON(t *testing.T) {
	var buf bytes.Buffer
	toolsListCmd.SetOut(&buf)

	// Reset flags to avoid state leakage
	toolsListCmd.Flags().Set("output", "json")
	err := toolsListCmd.RunE(toolsListCmd, nil)
	require.NoError(t, err)

	var result struct {
		Tools []ToolInfo `json:"tools"`
	}
	err = json.Unmarshal(buf.Bytes(), &result)
	require.NoError(t, err)
	require.Len(t, result.Tools, 5)

	// Verify each tool has all required fields
	for _, tool := range result.Tools {
		assert.NotEmpty(t, tool.Name, "tool missing name")
		assert.NotEmpty(t, tool.Command, "tool %s missing command", tool.Name)
		assert.NotEmpty(t, tool.Phase, "tool %s missing phase", tool.Name)
		assert.NotEmpty(t, tool.Description, "tool %s missing description", tool.Name)
		assert.NotEmpty(t, tool.InputType, "tool %s missing input_type", tool.Name)
		assert.NotEmpty(t, tool.OutputTypes, "tool %s missing output_types", tool.Name)
		assert.NotEmpty(t, tool.Kind, "tool %s missing kind", tool.Name)
	}

	// Verify specific tools
	names := make(map[string]ToolInfo)
	for _, tool := range result.Tools {
		names[tool.Name] = tool
	}
	assert.Contains(t, names, "subfinder")
	assert.Equal(t, "discover", names["subfinder"].Command)
	assert.Equal(t, "domains", names["subfinder"].InputType)
	assert.Equal(t, []string{"subdomain"}, names["subfinder"].OutputTypes)

	assert.Contains(t, names, "nuclei")
	assert.Equal(t, "assess", names["nuclei"].Command)
	assert.Equal(t, "urls", names["nuclei"].InputType)
}

func TestToolsListText(t *testing.T) {
	var buf bytes.Buffer
	toolsListCmd.SetOut(&buf)

	toolsListCmd.Flags().Set("output", "")
	// Need jsonOut to be false for text mode
	oldJsonOut := jsonOut
	jsonOut = false
	defer func() { jsonOut = oldJsonOut }()

	err := toolsListCmd.RunE(toolsListCmd, nil)
	require.NoError(t, err)

	text := buf.String()
	assert.Contains(t, text, "NAME")
	assert.Contains(t, text, "COMMAND")
	assert.Contains(t, text, "PHASE")
	assert.Contains(t, text, "INPUT")
	assert.Contains(t, text, "OUTPUT")
	assert.Contains(t, text, "STATUS")
	assert.Contains(t, text, "subfinder")
	assert.Contains(t, text, "discover")
	assert.Contains(t, text, "tools available")
}

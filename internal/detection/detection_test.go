package detection

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegistryCreation(t *testing.T) {
	r := NewRegistry()
	tools := r.Tools()
	require.Len(t, tools, 5)

	// Verify pipeline order
	assert.Equal(t, "subfinder", tools[0].Name())
	assert.Equal(t, "dnsx", tools[1].Name())
	assert.Equal(t, "naabu", tools[2].Name())
	assert.Equal(t, "httpx", tools[3].Name())
	assert.Equal(t, "nuclei", tools[4].Name())
}

func TestRegistryGetByName(t *testing.T) {
	r := NewRegistry()

	tool, ok := r.GetByName("subfinder")
	assert.True(t, ok)
	assert.Equal(t, "subfinder", tool.Name())

	tool, ok = r.GetByName("nuclei")
	assert.True(t, ok)
	assert.Equal(t, "nuclei", tool.Name())

	_, ok = r.GetByName("nonexistent")
	assert.False(t, ok)
}

func TestAvailableTools(t *testing.T) {
	r := NewRegistry()
	available := r.AvailableTools()

	// dnsx, naabu, httpx are native (always available)
	// nuclei is library (always available)
	// subfinder depends on binary
	assert.GreaterOrEqual(t, len(available), 4)

	names := make(map[string]bool)
	for _, tool := range available {
		names[tool.Name()] = true
	}
	assert.True(t, names["dnsx"])
	assert.True(t, names["naabu"])
	assert.True(t, names["httpx"])
	assert.True(t, names["nuclei"])
}

func TestToolMetadata(t *testing.T) {
	expected := []struct {
		name  string
		phase string
		kind  ToolKind
	}{
		{"subfinder", "discovery", ToolKindNative},
		{"dnsx", "resolution", ToolKindNative},
		{"naabu", "port_scan", ToolKindNative},
		{"httpx", "http_probe", ToolKindNative},
		{"nuclei", "assessment", ToolKindLibrary},
	}

	r := NewRegistry()
	tools := r.Tools()

	for i, exp := range expected {
		assert.Equal(t, exp.name, tools[i].Name(), "tool %d name", i)
		assert.Equal(t, exp.phase, tools[i].Phase(), "tool %d phase", i)
		assert.Equal(t, exp.kind, tools[i].Kind(), "tool %d kind", i)
	}
}

func TestToolCLIMetadata(t *testing.T) {
	expected := []struct {
		name        string
		command     string
		description string
		inputType   string
		outputTypes []string
	}{
		{"subfinder", "discover", "Discover subdomains for a target domain using passive sources", "domains", []string{"subdomain"}},
		{"dnsx", "resolve", "Resolve domains to IP addresses via DNS lookup", "domains", []string{"ipv4", "ipv6"}},
		{"naabu", "portscan", "Scan hosts for open TCP ports", "ips", []string{"hostport"}},
		{"httpx", "probe", "Probe host:port pairs for live HTTP services and detect technologies", "hostports", []string{"url", "technology"}},
		{"nuclei", "assess", "Run vulnerability assessment using nuclei templates", "urls", []string{"finding"}},
	}

	r := NewRegistry()
	tools := r.Tools()
	require.Len(t, tools, len(expected))

	// Check no duplicate command names
	commands := make(map[string]bool)
	for _, tool := range tools {
		assert.NotEmpty(t, tool.Command(), "tool %s has empty Command()", tool.Name())
		assert.NotEmpty(t, tool.Description(), "tool %s has empty Description()", tool.Name())
		assert.NotEmpty(t, tool.InputType(), "tool %s has empty InputType()", tool.Name())
		assert.NotEmpty(t, tool.OutputTypes(), "tool %s has empty OutputTypes()", tool.Name())

		assert.False(t, commands[tool.Command()], "duplicate command name: %s", tool.Command())
		commands[tool.Command()] = true
	}

	for i, exp := range expected {
		assert.Equal(t, exp.command, tools[i].Command(), "tool %d command", i)
		assert.Equal(t, exp.description, tools[i].Description(), "tool %d description", i)
		assert.Equal(t, exp.inputType, tools[i].InputType(), "tool %d inputType", i)
		assert.Equal(t, exp.outputTypes, tools[i].OutputTypes(), "tool %d outputTypes", i)
	}
}

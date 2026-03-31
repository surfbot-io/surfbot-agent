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

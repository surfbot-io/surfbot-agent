package detection

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSubfinderOutputParsing(t *testing.T) {
	results, err := ParseSubfinderFile("testdata/subfinder_sample.txt")
	require.NoError(t, err)

	assert.Len(t, results, 5)
	assert.Contains(t, results, "www.example.com")
	assert.Contains(t, results, "mail.example.com")
	assert.Contains(t, results, "api.example.com")
	assert.Contains(t, results, "cdn.example.com")
	assert.Contains(t, results, "blog.example.com")
}

func TestSubfinderOutputDedup(t *testing.T) {
	data := []byte("www.example.com\nWWW.EXAMPLE.COM\nwww.example.com\napi.example.com\n")
	results := ParseSubfinderOutput(data)

	// Should be deduplicated and lowercased
	assert.Len(t, results, 2)
	assert.Contains(t, results, "www.example.com")
	assert.Contains(t, results, "api.example.com")
}

// TestSubfinderAvailable documents the SDK-embedded contract: since the
// tool no longer shells out to a binary, Available() is a constant true.
// Preserving the test ensures nobody silently regresses this by bringing
// back a binary dependency.
func TestSubfinderAvailable(t *testing.T) {
	s := NewSubfinderTool()
	assert.True(t, s.Available(),
		"subfinder is SDK-embedded (ToolKindLibrary); Available must be unconditionally true")
	assert.Equal(t, ToolKindLibrary, s.Kind(),
		"subfinder was migrated from a subprocess binary to the SDK — ToolKind must reflect that")
}

// TestSubfinderKindIsLibrary pins the contract at the Kind level too so a
// future change that flips Kind back to Native without updating Available
// also fails.
func TestSubfinderKindIsLibrary(t *testing.T) {
	s := NewSubfinderTool()
	assert.Equal(t, ToolKindLibrary, s.Kind())
}

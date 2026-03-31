package detection

import (
	"os/exec"
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

func TestSubfinderAvailable(t *testing.T) {
	s := NewSubfinderTool()
	available := s.Available()

	_, err := exec.LookPath("subfinder")
	expectedAvailable := err == nil

	assert.Equal(t, expectedAvailable, available)
}

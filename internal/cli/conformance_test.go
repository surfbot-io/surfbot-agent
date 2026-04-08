package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/fatih/color"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestJSONHasNoANSI asserts that commands supporting --json produce output
// with zero ANSI escape sequences. Covers the JSON mode guarantee from
// SPEC-N8 §2.7.
func TestJSONHasNoANSI(t *testing.T) {
	// Use a temp DB so we don't touch the user's real store.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	cases := []struct {
		name string
		args []string
	}{
		{"target-list-json", []string{"target", "list", "--json", "--db", tmp + "/test.db"}},
		{"assets-list-json", []string{"assets", "--json", "--db", tmp + "/test.db"}},
		{"tools-list-json", []string{"tools", "list", "-o", "json", "--db", tmp + "/test.db"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Force color ON so any accidental leak would show up.
			color.NoColor = false
			defer func() { color.NoColor = true }()

			var stdout, stderr bytes.Buffer
			rootCmd.SetOut(&stdout)
			rootCmd.SetErr(&stderr)
			rootCmd.SetArgs(tc.args)
			_ = rootCmd.Execute()

			out := stdout.String()
			assert.False(t, strings.ContainsAny(out, "\x1b"),
				"json output should not contain ANSI escapes: %q", out)
		})
	}
}

// TestNoColorEnv asserts that setting NO_COLOR=1 disables color output
// end-to-end via the PersistentPreRunE hook. Covers SPEC-N8 §2.7 and the
// --no-color / NO_COLOR equivalence requirement.
func TestNoColorEnv(t *testing.T) {
	origNoColor := color.NoColor
	defer func() { color.NoColor = origNoColor }()

	require.NoError(t, os.Setenv("NO_COLOR", "1"))
	defer os.Unsetenv("NO_COLOR")

	color.NoColor = false
	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{"version"})
	_ = rootCmd.Execute()

	assert.True(t, color.NoColor, "NO_COLOR env should set color.NoColor")
	assert.False(t, strings.ContainsAny(buf.String(), "\x1b"),
		"version output should not contain ANSI escapes: %q", buf.String())
}

// TestNoColorFlag asserts that --no-color is equivalent to NO_COLOR=1.
func TestNoColorFlag(t *testing.T) {
	origNoColor := color.NoColor
	defer func() { color.NoColor = origNoColor }()

	color.NoColor = false
	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{"--no-color", "version"})
	_ = rootCmd.Execute()

	assert.True(t, color.NoColor, "--no-color flag should set color.NoColor")
	assert.False(t, strings.ContainsAny(buf.String(), "\x1b"))
}

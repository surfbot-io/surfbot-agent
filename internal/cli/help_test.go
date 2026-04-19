package cli

import (
	"strings"
	"testing"
)

// TestHelpOutputCoversNewCommands asserts that every SCHED1.3b command
// tree surfaces its subcommands in --help output. This is the closest
// thing to a golden file without coupling to cobra's exact formatting.
func TestHelpOutputCoversNewCommands(t *testing.T) {
	cases := []struct {
		args     []string
		expected []string
	}{
		{
			args:     []string{"schedule", "--help"},
			expected: []string{"list", "show", "create", "update", "delete", "pause", "resume", "upcoming", "bulk"},
		},
		{
			args:     []string{"template", "--help"},
			expected: []string{"list", "show", "create", "update", "delete"},
		},
		{
			args:     []string{"blackout", "--help"},
			expected: []string{"list", "show", "create", "update", "delete"},
		},
		{
			args:     []string{"defaults", "--help"},
			expected: []string{"show", "update"},
		},
		{
			args:     []string{"scan", "--help"},
			expected: []string{"adhoc"},
		},
	}

	for _, tc := range cases {
		out, _, err := runCLI(t, tc.args...)
		if err != nil {
			t.Fatalf("%v: unexpected error: %v", tc.args, err)
		}
		for _, want := range tc.expected {
			if !strings.Contains(out, want) {
				t.Errorf("%v help missing %q\nfull help:\n%s", tc.args, want, out)
			}
		}
	}
}

func TestRootHelpListsNewTopLevelCommands(t *testing.T) {
	out, _, err := runCLI(t, "--help")
	if err != nil {
		t.Fatalf("root help: %v", err)
	}
	for _, want := range []string{"schedule", "template", "blackout", "defaults", "scan"} {
		if !strings.Contains(out, want) {
			t.Errorf("root help missing %q", want)
		}
	}
}

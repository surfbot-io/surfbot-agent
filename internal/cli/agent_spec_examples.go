package cli

import (
	"strings"
	"sync"

	"github.com/spf13/cobra"
)

var examplesOnce sync.Once

// attachDefaultExamples adds canonical examples to a curated set of
// commands so the agent-spec output ships with at least one usage
// example per important atomic tool and high-level command. Idempotent.
func attachDefaultExamples(root *cobra.Command) {
	examplesOnce.Do(func() {
		add := func(path string, title, cmd, explanation string) {
			c := findCommandByPath(root, strings.Split(path, "."))
			if c == nil {
				return
			}
			AddExample(c, title, cmd, explanation)
		}

		add("discover",
			"Discover subdomains and pipe to resolver",
			"surfbot discover example.com --json | surfbot resolve --json",
			"Atomic composition: discover emits assets; resolve consumes assets from stdin.")

		add("resolve",
			"Resolve discovered subdomains to IPs",
			"surfbot discover example.com --json | surfbot resolve --json",
			"")

		add("portscan",
			"Scan ports on resolved hosts",
			"surfbot resolve --json < hosts.json | surfbot portscan --json",
			"")

		add("probe",
			"HTTP-probe live ports",
			"surfbot portscan --json < ports.json | surfbot probe --json",
			"")

		add("assess",
			"Run nuclei templates against probed URLs",
			"surfbot probe --json < urls.json | surfbot assess --json",
			"Terminal step in the scan pipeline; emits findings, not assets.")

		add("scan",
			"Run the full pipeline as a recipe",
			"surfbot scan example.com",
			"Equivalent to discover|resolve|portscan|probe|assess.")

		add("findings.list",
			"List findings as JSON",
			"surfbot findings list --json",
			"")

		add("target.add",
			"Register a new target",
			"surfbot target add example.com",
			"")
	})
}

func findCommandByPath(root *cobra.Command, path []string) *cobra.Command {
	cur := root
	for _, seg := range path {
		var next *cobra.Command
		for _, c := range cur.Commands() {
			if c.Name() == seg {
				next = c
				break
			}
		}
		if next == nil {
			return nil
		}
		cur = next
	}
	return cur
}

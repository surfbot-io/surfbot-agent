package cli

import (
	"fmt"
	"io"
	"strings"
)

// renderMarkdown writes a human-readable version of the spec. No external
// markdown library — we just build strings.
func renderMarkdown(w io.Writer, spec Spec) error {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s agent-spec\n\n", spec.Binary)
	fmt.Fprintf(&b, "- spec_version: `%s`\n", spec.SpecVersion)
	fmt.Fprintf(&b, "- agent_version: `%s`\n", spec.AgentVersion)
	fmt.Fprintf(&b, "- generated_at: `%s`\n\n", spec.GeneratedAt)
	fmt.Fprintf(&b, "%s\n\n", spec.Description)

	if len(spec.GlobalFlags) > 0 {
		b.WriteString("## Global flags\n\n")
		for _, f := range spec.GlobalFlags {
			fmt.Fprintf(&b, "- `--%s` (%s) — %s\n", f.Name, f.Type, f.Description)
		}
		b.WriteString("\n")
	}

	// Group by category.
	byCat := map[string][]Command{}
	var cats []string
	for _, c := range spec.Commands {
		if _, ok := byCat[c.Category]; !ok {
			cats = append(cats, c.Category)
		}
		byCat[c.Category] = append(byCat[c.Category], c)
	}

	for _, cat := range cats {
		fmt.Fprintf(&b, "## %s\n\n", cat)
		for _, c := range byCat[cat] {
			fmt.Fprintf(&b, "### `surfbot %s`\n\n", strings.Join(c.Path, " "))
			if c.Summary != "" {
				fmt.Fprintf(&b, "%s\n\n", c.Summary)
			}
			if c.Input.Type != "" {
				fmt.Fprintf(&b, "- input: `%s` (%s)\n", c.Input.Type, c.Input.Source)
			}
			if c.Output.Type != "" {
				fmt.Fprintf(&b, "- output: `%s` (%s)\n", c.Output.Type, c.Output.StdoutFormat)
			}
			if len(c.Flags) > 0 {
				b.WriteString("- flags:\n")
				for _, f := range c.Flags {
					fmt.Fprintf(&b, "  - `--%s` (%s) — %s\n", f.Name, f.Type, f.Description)
				}
			}
			for _, ex := range c.Examples {
				fmt.Fprintf(&b, "- example: `%s` — %s\n", ex.Cmd, ex.Title)
			}
			b.WriteString("\n")
		}
	}

	if len(spec.Composition.Recipes) > 0 {
		b.WriteString("## Recipes\n\n")
		for _, r := range spec.Composition.Recipes {
			fmt.Fprintf(&b, "### %s — equivalent to `%s`\n\n", r.Name, r.EquivalentTo)
			for _, s := range r.Steps {
				fmt.Fprintf(&b, "1. `%s`", s.Cmd)
				if s.Consumes != "" {
					fmt.Fprintf(&b, " (consumes %s)", s.Consumes)
				}
				if s.Produces != "" {
					fmt.Fprintf(&b, " → produces %s", s.Produces)
				}
				b.WriteString("\n")
			}
			b.WriteString("\n")
		}
	}

	_, err := io.WriteString(w, b.String())
	return err
}

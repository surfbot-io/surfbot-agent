package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/surfbot-io/surfbot-agent/internal/detection"
)

// collectCobraCommands returns every non-skipped command in a Cobra tree
// as dotted-path strings, excluding the root itself.
func collectCobraCommands(root *cobra.Command) []string {
	var out []string
	var rec func(cmd *cobra.Command, path []string)
	rec = func(cmd *cobra.Command, path []string) {
		if cmd != root && !shouldSkip(cmd) {
			out = append(out, strings.Join(path, "."))
		}
		for _, c := range cmd.Commands() {
			rec(c, append(append([]string{}, path...), c.Name()))
		}
	}
	rec(root, nil)
	return out
}

// TestSpecCompleteness enforces that every reachable Cobra command
// appears in the spec — no silent drift when new commands are added.
func TestSpecCompleteness(t *testing.T) {
	spec := BuildSpec(rootCmd)

	inSpec := map[string]bool{}
	for _, c := range spec.Commands {
		inSpec[strings.Join(c.Path, ".")] = true
	}

	for _, path := range collectCobraCommands(rootCmd) {
		if !inSpec[path] {
			t.Errorf("command %q missing from agent-spec output", path)
		}
	}
}

// TestDetectionCoverage enforces every registered detection tool is
// represented as an atomic-detection command with non-empty I/O types.
func TestDetectionCoverage(t *testing.T) {
	spec := BuildSpec(rootCmd)
	byName := map[string]Command{}
	for _, c := range spec.Commands {
		byName[c.Name] = c
	}

	for _, tool := range detection.NewRegistry().Tools() {
		c, ok := byName[tool.Command()]
		if !ok {
			t.Errorf("tool %q (command %q) missing from spec", tool.Name(), tool.Command())
			continue
		}
		if c.Category != "atomic-detection" {
			t.Errorf("tool command %q has category %q, want atomic-detection", tool.Command(), c.Category)
		}
		if c.Input.Type == "" {
			t.Errorf("tool command %q has empty input.type", tool.Command())
		}
		if c.Output.Type == "" {
			t.Errorf("tool command %q has empty output.type", tool.Command())
		}
	}
}

// TestPipeConsistency enforces that every pipe rule's producer exists
// as a command in the spec, every consumer does too, and every declared
// InputType on a detection tool has at least one pipe rule feeding it.
// The last check is the real invariant: a tool whose InputType has no
// producer is unreachable through composition, which is exactly the
// bug SUR-237 surfaced.
func TestPipeConsistency(t *testing.T) {
	spec := BuildSpec(rootCmd)
	known := map[string]bool{}
	for _, c := range spec.Commands {
		known[c.Name] = true
	}
	for _, rule := range spec.Composition.Pipes {
		if !known[rule.From] {
			t.Errorf("pipe from %q: command not in spec", rule.From)
		}
		for _, to := range rule.To {
			if !known[to] {
				t.Errorf("pipe %q → %q: consumer not in spec", rule.From, to)
			}
		}
	}

	// Every non-initial tool must be the target of at least one pipe.
	// "discover" is the sole initial tool (its InputType "domains" is
	// supplied by the user on the CLI, not by another tool).
	fedBy := map[string][]string{}
	for _, rule := range spec.Composition.Pipes {
		for _, to := range rule.To {
			fedBy[to] = append(fedBy[to], rule.From)
		}
	}
	for _, tool := range detection.NewRegistry().Tools() {
		if tool.Command() == "discover" {
			continue
		}
		if len(fedBy[tool.Command()]) == 0 {
			t.Errorf("tool %q (InputType=%q) has no upstream producer in pipe rules",
				tool.Command(), tool.InputType())
		}
	}
}

// TestAllInputTypesHaveProducer walks the detection registry directly
// and asserts that every non-initial tool's InputType is produced by
// at least one other tool's OutputTypes. This is the structural
// counterpart to TestPipeConsistency: it would catch a regression even
// if BuildPipeRules were swapped out.
func TestAllInputTypesHaveProducer(t *testing.T) {
	tools := detection.NewRegistry().Tools()
	for _, consumer := range tools {
		if consumer.Command() == "discover" {
			continue
		}
		in := consumer.InputType()
		if in == "" {
			t.Errorf("tool %q has empty InputType", consumer.Command())
			continue
		}
		found := false
		for _, producer := range tools {
			if producer.Command() == consumer.Command() {
				continue
			}
			for _, out := range producer.OutputTypes() {
				if matchesType(out, in) {
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			t.Errorf("tool %q InputType %q has no producer in the registry",
				consumer.Command(), in)
		}
	}
}

// TestRecipeExecutability enforces every recipe step parses against the
// live Cobra tree — no unknown commands, no stale recipes.
func TestRecipeExecutability(t *testing.T) {
	for _, r := range BuildRecipes() {
		for _, step := range r.Steps {
			// Extract just the subcommand name (first word).
			first := strings.Fields(step.Cmd)
			if len(first) == 0 {
				t.Errorf("recipe %q: empty step", r.Name)
				continue
			}
			found := false
			for _, c := range rootCmd.Commands() {
				if c.Name() == first[0] {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("recipe %q step %q: subcommand %q not in Cobra tree", r.Name, step.Cmd, first[0])
			}
		}
	}
}

// TestSchemaOnlyFilter enforces that --schema-only output preserves
// types + keeps every command but strips non-schema fields.
func TestSchemaOnlyFilter(t *testing.T) {
	spec := BuildSpec(rootCmd)
	lite := schemaOnly(spec)
	if len(lite.Types) == 0 {
		t.Error("schema-only output missing types")
	}
	if len(lite.Commands) != len(spec.Commands) {
		t.Errorf("schema-only dropped commands: %d vs %d", len(lite.Commands), len(spec.Commands))
	}
	for _, c := range lite.Commands {
		if c.Summary != "" || c.Long != "" || len(c.Flags) > 0 {
			t.Errorf("schema-only command %q still has descriptive fields", c.Name)
		}
	}
}

// TestCommandFilter enforces --command path filtering.
func TestCommandFilter(t *testing.T) {
	spec := BuildSpec(rootCmd)
	filtered, ok := filterCommand(spec, "discover")
	if !ok {
		t.Fatal("expected discover to be found")
	}
	if len(filtered.Commands) != 1 || filtered.Commands[0].Name != "discover" {
		t.Errorf("filter returned wrong command(s): %v", filtered.Commands)
	}
	if _, ok := filterCommand(spec, "nonsense"); ok {
		t.Error("expected nonsense to not be found")
	}
	if _, ok := filterCommand(spec, "findings.list"); !ok {
		t.Error("expected dotted path findings.list to resolve")
	}
}

// TestJSONRoundTrip enforces the full spec round-trips through JSON.
func TestJSONRoundTrip(t *testing.T) {
	spec := BuildSpec(rootCmd)
	b, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back Spec
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.SpecVersion != spec.SpecVersion {
		t.Errorf("round-trip mismatch: %s vs %s", back.SpecVersion, spec.SpecVersion)
	}
}

// TestMarkdownRenders ensures the md renderer produces non-empty output
// for every major section.
func TestMarkdownRenders(t *testing.T) {
	spec := BuildSpec(rootCmd)
	var buf bytes.Buffer
	if err := renderMarkdown(&buf, spec); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"# surfbot agent-spec", "## Global flags", "## Recipes"} {
		if !strings.Contains(out, want) {
			t.Errorf("markdown output missing %q", want)
		}
	}
}

// TestSpecBuiltinTemplates enforces SPEC-SCHED2.3: the agent-spec
// document exposes the three seeded builtin templates so an LLM can
// call `--template <name>` without first listing.
func TestSpecBuiltinTemplates(t *testing.T) {
	spec := BuildSpec(rootCmd)
	if len(spec.BuiltinTemplates) != 3 {
		t.Fatalf("want 3 builtin templates, got %d", len(spec.BuiltinTemplates))
	}
	names := map[string]bool{}
	for _, b := range spec.BuiltinTemplates {
		names[b.Name] = true
		if b.RRule == "" {
			t.Errorf("builtin %q missing rrule", b.Name)
		}
		if len(b.ToolConfig) == 0 {
			t.Errorf("builtin %q missing tool_config", b.Name)
		}
		if b.Description == "" {
			t.Errorf("builtin %q missing description", b.Name)
		}
	}
	for _, want := range []string{"Default", "Fast", "Deep"} {
		if !names[want] {
			t.Errorf("builtin %q missing from agent-spec output", want)
		}
	}
}

// TestSpecDeterministicOrdering enforces alphabetical ordering of
// commands and global flags.
func TestSpecDeterministicOrdering(t *testing.T) {
	spec := BuildSpec(rootCmd)
	for i := 1; i < len(spec.Commands); i++ {
		prev := strings.Join(spec.Commands[i-1].Path, ".")
		cur := strings.Join(spec.Commands[i].Path, ".")
		if prev > cur {
			t.Errorf("commands not sorted: %q after %q", cur, prev)
		}
	}
	for i := 1; i < len(spec.GlobalFlags); i++ {
		if spec.GlobalFlags[i-1].Name > spec.GlobalFlags[i].Name {
			t.Errorf("global flags not sorted")
		}
	}
}

package cli

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/surfbot-io/surfbot-agent/internal/detection"
)

// SpecVersion is the semver of the agent-spec document format itself.
// Bump major for breaking changes to the envelope; minor for additive fields.
const SpecVersion = "1.1.0"

// Spec is the top-level agent-spec document.
type Spec struct {
	SpecVersion  string            `json:"spec_version"`
	AgentVersion string            `json:"agent_version"`
	GeneratedAt  string            `json:"generated_at"`
	Binary       string            `json:"binary"`
	Description  string            `json:"description"`
	GlobalFlags  []Flag            `json:"global_flags"`
	Commands     []Command         `json:"commands"`
	Types        map[string]Schema `json:"types"`
	Composition  Composition       `json:"composition"`
}

// Command describes a single Cobra command for an LLM consumer.
type Command struct {
	Name            string        `json:"name"`
	Path            []string      `json:"path"`
	Summary         string        `json:"summary"`
	Long            string        `json:"long,omitempty"`
	Category        string        `json:"category"`
	Stable          bool          `json:"stable"`
	RequiresDB      bool          `json:"requires_db"`
	RequiresNetwork bool          `json:"requires_network"`
	Flags           []Flag        `json:"flags"`
	PositionalArgs  []PosArg      `json:"positional_args"`
	Input           IOContract    `json:"input"`
	Output          IOContract    `json:"output"`
	SideEffects     []string      `json:"side_effects,omitempty"`
	Examples        []SpecExample `json:"examples,omitempty"`
	Errors          []ErrorDoc    `json:"errors,omitempty"`
}

// Flag is one Cobra flag, persistent or local.
type Flag struct {
	Name        string      `json:"name"`
	Short       string      `json:"short,omitempty"`
	Type        string      `json:"type"`
	Default     interface{} `json:"default,omitempty"`
	Description string      `json:"description,omitempty"`
	Env         string      `json:"env,omitempty"`
	Required    bool        `json:"required,omitempty"`
}

// PosArg describes a positional argument.
type PosArg struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	Required  bool   `json:"required"`
	Repeating bool   `json:"repeating"`
}

// IOContract declares where a command reads input from or writes output to.
//
// `type` is the primary (canonical) input/output type — the one the
// scan recipe uses and the field every existing consumer already reads.
// `types` is the full list for tools that accept or produce more than
// one type (e.g. httpx probe accepts both "hostports" and "domains").
// For single-type commands, `types` contains exactly one element equal
// to `type`, so consumers can read either field.
type IOContract struct {
	Source       string   `json:"source,omitempty"`
	StdoutFormat string   `json:"stdout_format,omitempty"`
	Type         string   `json:"type"`
	Types        []string `json:"types,omitempty"`
	SchemaRef    string   `json:"schema_ref,omitempty"`
	TextStable   bool     `json:"text_stable,omitempty"`
}

// ErrorDoc documents an exit code.
type ErrorDoc struct {
	ExitCode int    `json:"exit_code"`
	When     string `json:"when"`
}

// Composition groups pipe rules and named recipes.
type Composition struct {
	Pipes   []PipeRule `json:"pipes"`
	Recipes []Recipe   `json:"recipes"`
}

// PipeRule declares a valid output→input pipe between commands.
type PipeRule struct {
	From      string   `json:"from"`
	To        []string `json:"to"`
	Carrier   string   `json:"carrier"`
	Transform string   `json:"transform,omitempty"`
}

// Recipe is a named canonical composition of atomic commands.
type Recipe struct {
	Name         string       `json:"name"`
	EquivalentTo string       `json:"equivalent_to"`
	Steps        []RecipeStep `json:"steps"`
}

// RecipeStep is one step in a recipe.
type RecipeStep struct {
	Cmd      string `json:"cmd"`
	Consumes string `json:"consumes,omitempty"`
	Produces string `json:"produces,omitempty"`
}

var (
	agentSpecFormat  string
	agentSpecCommand string
	agentSpecSchemas bool
	agentSpecVersion bool
)

var agentSpecCmd = &cobra.Command{
	Use:   "agent-spec",
	Short: "Emit a machine-readable contract of every command for LLM orchestration",
	Long: `agent-spec emits a self-describing JSON (or markdown) document that tells an
LLM agent every subcommand, flag, input/output schema, and composition rule
for the Surfbot CLI.

Give the JSON output to a cold LLM and it has everything it needs to drive
Surfbot atomically — no prior knowledge required.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if agentSpecVersion {
			fmt.Fprintf(cmd.OutOrStdout(), "spec_version=%s agent_version=%s\n", SpecVersion, Version)
			return nil
		}

		spec := BuildSpec(rootCmd)

		if agentSpecCommand != "" {
			filtered, ok := filterCommand(spec, agentSpecCommand)
			if !ok {
				fmt.Fprintf(cmd.ErrOrStderr(), "unknown command: %s\n", agentSpecCommand)
				return errExit(2)
			}
			spec = filtered
		}

		if agentSpecSchemas {
			spec = schemaOnly(spec)
		}

		switch agentSpecFormat {
		case "", "json":
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(spec)
		case "md", "markdown":
			return renderMarkdown(cmd.OutOrStdout(), spec)
		default:
			return fmt.Errorf("unknown --format: %s (want json or md)", agentSpecFormat)
		}
	},
}

// errExit is a sentinel used to communicate a specific exit code.
type errExit int

func (e errExit) Error() string { return fmt.Sprintf("exit %d", int(e)) }

func init() {
	agentSpecCmd.SilenceErrors = true
	agentSpecCmd.SilenceUsage = true
	agentSpecCmd.Flags().StringVar(&agentSpecFormat, "format", "json", "Output format: json or md")
	agentSpecCmd.Flags().StringVar(&agentSpecCommand, "command", "", "Return spec for a single command (dotted path, e.g. findings.list)")
	agentSpecCmd.Flags().BoolVar(&agentSpecSchemas, "schema-only", false, "Only emit types + command input/output schema refs")
	agentSpecCmd.Flags().BoolVar(&agentSpecVersion, "version", false, "Print only spec + agent version")
	rootCmd.AddCommand(agentSpecCmd)
}

// BuildSpec assembles the full Spec document by walking the given root
// Cobra command. Exported for tests and for potential use by other tools.
func BuildSpec(root *cobra.Command) Spec {
	attachDefaultExamples(root)
	walker := newWalker(detection.NewRegistry())
	commands := walker.walk(root)
	sort.Slice(commands, func(i, j int) bool {
		return strings.Join(commands[i].Path, ".") < strings.Join(commands[j].Path, ".")
	})

	return Spec{
		SpecVersion:  SpecVersion,
		AgentVersion: Version,
		GeneratedAt:  time.Now().UTC().Format(time.RFC3339),
		Binary:       "surfbot",
		Description:  "Surfbot Agent — local security scanner with atomic, LLM-orchestrable commands.",
		GlobalFlags:  collectGlobalFlags(root),
		Commands:     commands,
		Types:        BuildTypeSchemas(),
		Composition: Composition{
			Pipes:   BuildPipeRules(detection.NewRegistry()),
			Recipes: BuildRecipes(),
		},
	}
}

func collectGlobalFlags(root *cobra.Command) []Flag {
	var out []Flag
	root.PersistentFlags().VisitAll(func(f *pflagFlag) {
		out = append(out, flagFromPFlag(f))
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func filterCommand(spec Spec, dotted string) (Spec, bool) {
	target := strings.Split(dotted, ".")
	for _, c := range spec.Commands {
		if pathEqual(c.Path, target) {
			spec.Commands = []Command{c}
			return spec, true
		}
	}
	return spec, false
}

func pathEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func schemaOnly(spec Spec) Spec {
	lite := make([]Command, 0, len(spec.Commands))
	for _, c := range spec.Commands {
		lite = append(lite, Command{
			Name:   c.Name,
			Path:   c.Path,
			Input:  IOContract{Type: c.Input.Type, SchemaRef: c.Input.SchemaRef},
			Output: IOContract{Type: c.Output.Type, SchemaRef: c.Output.SchemaRef, StdoutFormat: c.Output.StdoutFormat},
		})
	}
	return Spec{
		SpecVersion:  spec.SpecVersion,
		AgentVersion: spec.AgentVersion,
		GeneratedAt:  spec.GeneratedAt,
		Binary:       spec.Binary,
		Types:        spec.Types,
		Commands:     lite,
	}
}

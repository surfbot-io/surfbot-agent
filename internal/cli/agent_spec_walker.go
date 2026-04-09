package cli

import (
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/surfbot-io/surfbot-agent/internal/detection"
)

// pflagFlag is an alias so other files in the package can reference pflag
// without each importing it.
type pflagFlag = pflag.Flag

// walker walks a Cobra tree and produces []Command. It enriches commands
// whose name matches a detection tool in the registry with I/O metadata.
type walker struct {
	reg *detection.Registry
}

func newWalker(reg *detection.Registry) *walker { return &walker{reg: reg} }

// walk returns every visible non-root command in the tree.
func (w *walker) walk(root *cobra.Command) []Command {
	var out []Command
	var rec func(cmd *cobra.Command, path []string)
	rec = func(cmd *cobra.Command, path []string) {
		if cmd != root && !shouldSkip(cmd) {
			out = append(out, w.commandFrom(cmd, path))
		}
		for _, child := range cmd.Commands() {
			rec(child, append(append([]string{}, path...), child.Name()))
		}
	}
	rec(root, nil)
	return out
}

// shouldSkip filters out commands that don't belong in the spec.
func shouldSkip(cmd *cobra.Command) bool {
	if cmd.Hidden {
		return true
	}
	switch cmd.Name() {
	case "help", "completion":
		return true
	}
	return false
}

func (w *walker) commandFrom(cmd *cobra.Command, path []string) Command {
	c := Command{
		Name:           cmd.Name(),
		Path:           append([]string{}, path...),
		Summary:        cmd.Short,
		Long:           strings.TrimSpace(cmd.Long),
		Stable:         true,
		Flags:          collectLocalFlags(cmd),
		PositionalArgs: inferPositionalArgs(cmd),
		Category:       classify(cmd),
		RequiresDB:     !skipDB(cmd),
		Examples:       getExamples(cmd),
	}

	// Enrich atomic detection commands with I/O metadata from the registry.
	// Tool commands register themselves under tool.Command() (e.g. "discover"),
	// which doesn't match tool.Name() (e.g. "subfinder") — match by Command().
	var tool detection.DetectionTool
	for _, t := range w.reg.Tools() {
		if t.Command() == cmd.Name() {
			tool = t
			break
		}
	}
	if tool != nil {
		c.Category = "atomic-detection"
		c.RequiresDB = false
		c.RequiresNetwork = true
		c.Input = IOContract{
			Source:    "argv",
			Type:      tool.InputType(),
			SchemaRef: schemaRefForInputType(tool.InputType()),
		}
		c.Output = IOContract{
			StdoutFormat: "json",
			Type:         joinOutputTypes(tool.OutputTypes()),
			SchemaRef:    "#/types/AssetList",
		}
		c.SideEffects = []string{"writes_findings_table", "writes_assets_table"}
	}

	return c
}

func classify(cmd *cobra.Command) string {
	// Classify by the top-level ancestor directly under rootCmd.
	name := cmd.Name()
	for p := cmd; p != nil && p.Parent() != nil; p = p.Parent() {
		if p.Parent().Name() == "surfbot" {
			name = p.Name()
			break
		}
	}
	switch name {
	case "scan":
		return "scan"
	case "target":
		return "target"
	case "findings":
		return "findings"
	case "assets":
		return "assets"
	case "score":
		return "score"
	case "status":
		return "status"
	case "daemon":
		return "daemon"
	case "agent-spec", "version", "tools":
		return "meta"
	case "fix", "connectors":
		return "meta"
	}
	return "meta"
}

func collectLocalFlags(cmd *cobra.Command) []Flag {
	var out []Flag
	cmd.LocalFlags().VisitAll(func(f *pflag.Flag) {
		// Don't re-emit global persistent flags on each command.
		if cmd.Root().PersistentFlags().Lookup(f.Name) != nil {
			return
		}
		out = append(out, flagFromPFlag(f))
	})
	return out
}

func flagFromPFlag(f *pflag.Flag) Flag {
	return Flag{
		Name:        f.Name,
		Short:       f.Shorthand,
		Type:        f.Value.Type(),
		Default:     parseDefault(f),
		Description: f.Usage,
	}
}

func parseDefault(f *pflag.Flag) interface{} {
	if f.DefValue == "" {
		return nil
	}
	switch f.Value.Type() {
	case "bool":
		return f.DefValue == "true"
	}
	return f.DefValue
}

func inferPositionalArgs(cmd *cobra.Command) []PosArg {
	// Cobra doesn't give us typed positional args; parse from Use.
	// e.g. "add <domain>" → [{domain,string,required}]
	// e.g. "enable <name>" → [{name,string,required}]
	parts := strings.Fields(cmd.Use)
	var out []PosArg
	for _, p := range parts[1:] {
		var required bool
		var repeating bool
		name := p
		switch {
		case strings.HasPrefix(p, "<") && strings.HasSuffix(p, ">"):
			required = true
			name = strings.TrimSuffix(strings.TrimPrefix(p, "<"), ">")
		case strings.HasPrefix(p, "[") && strings.HasSuffix(p, "]"):
			required = false
			name = strings.TrimSuffix(strings.TrimPrefix(p, "["), "]")
		default:
			continue
		}
		if strings.HasSuffix(name, "...") {
			repeating = true
			name = strings.TrimSuffix(name, "...")
		}
		out = append(out, PosArg{Name: name, Type: "string", Required: required, Repeating: repeating})
	}
	return out
}

func schemaRefForInputType(t string) string {
	switch t {
	case "domains":
		return "#/types/DomainList"
	case "ips", "hostports", "urls", "assets":
		return "#/types/AssetList"
	}
	return ""
}

func joinOutputTypes(types []string) string {
	if len(types) == 0 {
		return "assets"
	}
	return "assets"
}

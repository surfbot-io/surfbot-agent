package cli

import (
	"sort"

	"github.com/surfbot-io/surfbot-agent/internal/detection"
)

// BuildRecipes returns the hand-coded list of canonical compositions.
// Start with "scan" only — the recipe mirrors the real scan pipeline in
// internal/cli/scan.go. Keep this list short; non-obvious pipelines
// belong in user-facing docs, not here.
func BuildRecipes() []Recipe {
	return []Recipe{
		{
			Name:         "scan",
			EquivalentTo: "surfbot scan <domain>",
			Steps: []RecipeStep{
				{Cmd: "discover <domain> --json", Produces: "AssetList"},
				{Cmd: "resolve --json", Consumes: "AssetList", Produces: "AssetList"},
				{Cmd: "portscan --json", Consumes: "AssetList", Produces: "AssetList"},
				{Cmd: "probe --json", Consumes: "AssetList", Produces: "AssetList"},
				{Cmd: "assess --json", Consumes: "AssetList", Produces: "FindingList"},
			},
		},
	}
}

// BuildPipeRules derives pipe rules from the detection registry by
// matching each tool's output types against every other tool's input type.
// Additional hand-coded augmentation for non-detection commands can be
// appended here if ever needed.
func BuildPipeRules(reg *detection.Registry) []PipeRule {
	tools := reg.Tools()

	// Map each tool's command name → inputType for fast lookup.
	inputOf := map[string]string{}
	for _, t := range tools {
		inputOf[t.Command()] = t.InputType()
	}

	var rules []PipeRule
	for _, t := range tools {
		outs := t.OutputTypes()
		if len(outs) == 0 {
			continue
		}
		// Find every tool whose input type matches any of this tool's outputs.
		toSet := map[string]bool{}
		for _, out := range outs {
			for _, other := range tools {
				if other.Command() == t.Command() {
					continue
				}
				if matchesType(out, other.InputType()) {
					toSet[other.Command()] = true
				}
			}
		}
		if len(toSet) == 0 {
			continue
		}
		to := make([]string, 0, len(toSet))
		for k := range toSet {
			to = append(to, k)
		}
		sort.Strings(to)
		rules = append(rules, PipeRule{
			From:      t.Command(),
			To:        to,
			Carrier:   "AssetList",
			Transform: "identity",
		})
	}
	sort.Slice(rules, func(i, j int) bool { return rules[i].From < rules[j].From })
	return rules
}

// matchesType is intentionally liberal — any detection tool emits an
// AssetList and any downstream detection tool consumes an AssetList, so
// type compatibility here is effectively "both sides speak the same
// carrier." Specific asset-kind filtering is a runtime concern.
func matchesType(out, in string) bool {
	if out == "" || in == "" {
		return false
	}
	// Same literal type is always compatible.
	if out == in {
		return true
	}
	// Common carriers.
	switch in {
	case "domains":
		return out == "subdomains" || out == "domains"
	case "ips":
		return out == "ips" || out == "resolved"
	case "hostports":
		return out == "hostports" || out == "ports"
	case "urls":
		return out == "urls" || out == "http"
	}
	return false
}

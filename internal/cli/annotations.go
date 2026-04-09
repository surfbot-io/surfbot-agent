package cli

import (
	"encoding/json"

	"github.com/spf13/cobra"
)

// SpecExample is one example attached to a Cobra command for the agent-spec
// output. Examples are stored in cobra.Command.Annotations as a JSON-encoded
// array under the key "surfbot.examples".
type SpecExample struct {
	Title       string `json:"title"`
	Cmd         string `json:"cmd"`
	Explanation string `json:"explanation,omitempty"`
}

const specExamplesKey = "surfbot.examples"

// AddExample attaches an example to a Cobra command for the agent-spec
// output. Multiple calls append.
func AddExample(cmd *cobra.Command, title, command, explanation string) {
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	var examples []SpecExample
	if raw, ok := cmd.Annotations[specExamplesKey]; ok && raw != "" {
		_ = json.Unmarshal([]byte(raw), &examples)
	}
	examples = append(examples, SpecExample{Title: title, Cmd: command, Explanation: explanation})
	b, _ := json.Marshal(examples)
	cmd.Annotations[specExamplesKey] = string(b)
}

// getExamples extracts examples previously attached via AddExample.
func getExamples(cmd *cobra.Command) []SpecExample {
	if cmd.Annotations == nil {
		return nil
	}
	raw, ok := cmd.Annotations[specExamplesKey]
	if !ok || raw == "" {
		return nil
	}
	var examples []SpecExample
	_ = json.Unmarshal([]byte(raw), &examples)
	return examples
}

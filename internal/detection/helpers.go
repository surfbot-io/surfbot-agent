package detection

import (
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/surfbot-io/surfbot-agent/internal/model"
)

func buildToolRun(tool DetectionTool, startedAt time.Time, status model.ToolRunStatus, errMsg string, targetsCount, findingsCount int) model.ToolRun {
	now := time.Now().UTC()
	return model.ToolRun{
		ID:            uuid.New().String(),
		ToolName:      tool.Name(),
		Phase:         tool.Phase(),
		Status:        status,
		StartedAt:     startedAt,
		FinishedAt:    &now,
		DurationMs:    now.Sub(startedAt).Milliseconds(),
		TargetsCount:  targetsCount,
		FindingsCount: findingsCount,
		ErrorMessage:  errMsg,
		Config:        map[string]interface{}{},
		CreatedAt:     now,
		UpdatedAt:     now,
	}
}

// Size cap for captured log tails. Big enough to show the last few lines of
// tool stderr for debugging, small enough to keep tool_runs.config JSON
// blobs under a reasonable size even for chatty tools like naabu (which
// spams per-port errors in verbose mode).
const toolLogTailMaxBytes = 4096

// tailString returns the last `max` bytes of s, prefixed with a "…\n" marker
// when it had to truncate, so the user knows the earlier output was dropped.
// Trimmed at the first newline after the cut to avoid mid-line slices.
func tailString(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := s[len(s)-max:]
	if i := strings.IndexByte(cut, '\n'); i >= 0 && i < max-1 {
		cut = cut[i+1:]
	}
	return "…\n" + cut
}

// inputPreview returns the first `max` elements of inputs for surfacing in
// tool_run.config without blowing up the JSON size when a tool receives
// thousands of targets (nuclei on a large scan can easily hit 1000+ URLs).
func inputPreview(inputs []string, max int) []string {
	if len(inputs) <= max {
		out := make([]string, len(inputs))
		copy(out, inputs)
		return out
	}
	out := make([]string, max)
	copy(out, inputs[:max])
	return out
}

// attachExecContext stuffs exec telemetry (command line, exit code, stderr
// tail) into tool_run.Config under a stable schema so the webui and any
// other consumer can render logs without per-tool special casing.
//
// Convention of keys inside Config:
//
//	command        — string, space-joined argv as run
//	exit_code      — int, process exit code (0 on success or when not a subprocess)
//	stderr_tail    — string, tail of stderr captured during the run (may be "")
//	input_preview  — []string, up to 8 of the inputs passed in
//	inputs_truncated — bool, true when input_preview was cut
func attachExecContext(tr *model.ToolRun, command string, exitCode int, stderr string, inputs []string) {
	if tr.Config == nil {
		tr.Config = map[string]interface{}{}
	}
	if command != "" {
		tr.Config["command"] = command
	}
	tr.Config["exit_code"] = exitCode
	if stderr != "" {
		tr.Config["stderr_tail"] = tailString(strings.TrimRight(stderr, "\n"), toolLogTailMaxBytes)
	}
	const inputPreviewCap = 8
	tr.Config["input_preview"] = inputPreview(inputs, inputPreviewCap)
	if len(inputs) > inputPreviewCap {
		tr.Config["inputs_truncated"] = true
	}
}

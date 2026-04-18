package detection

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/surfbot-io/surfbot-agent/internal/model"
)

// SubfinderTool discovers subdomains using the subfinder binary as a subprocess.
type SubfinderTool struct{}

func NewSubfinderTool() *SubfinderTool { return &SubfinderTool{} }

func (s *SubfinderTool) Name() string   { return "subfinder" }
func (s *SubfinderTool) Phase() string  { return "discovery" }
func (s *SubfinderTool) Kind() ToolKind { return ToolKindNative }

func (s *SubfinderTool) Available() bool {
	_, err := exec.LookPath("subfinder")
	return err == nil
}

func (s *SubfinderTool) Command() string       { return "discover" }
func (s *SubfinderTool) Description() string   { return "Discover subdomains for a target domain using passive sources" }
func (s *SubfinderTool) InputType() string     { return "domains" }
func (s *SubfinderTool) OutputTypes() []string { return []string{"subdomain"} }

func (s *SubfinderTool) Run(ctx context.Context, inputs []string, opts RunOptions) (*RunResult, error) {
	startedAt := time.Now().UTC()

	if !s.Available() {
		tr := buildToolRun(s, startedAt, model.ToolRunSkipped, "subfinder binary not found in PATH", len(inputs), 0)
		attachExecContext(&tr, "", 0, "", inputs)
		return &RunResult{ToolRun: tr}, nil
	}

	result := &RunResult{}
	// Accumulate stderr and last-run command across per-domain executions
	// so the tool_run record shows what actually ran end-to-end even when
	// subfinder is invoked once per input.
	var stderrAcc strings.Builder
	var lastCmd string
	var lastExit int

	for _, domain := range inputs {
		subs, cmdStr, stderrTail, exitCode, err := s.enumerate(ctx, domain, opts)
		lastCmd = cmdStr
		lastExit = exitCode
		if stderrTail != "" {
			stderrAcc.WriteString(stderrTail)
			stderrAcc.WriteByte('\n')
		}
		if err != nil {
			tr := buildToolRun(s, startedAt, model.ToolRunFailed, err.Error(), len(inputs), 0)
			attachExecContext(&tr, cmdStr, exitCode, stderrAcc.String(), inputs)
			result.ToolRun = tr
			return result, nil
		}

		// Always include the root domain
		subs[strings.ToLower(domain)] = struct{}{}

		for sub := range subs {
			result.Assets = append(result.Assets, model.Asset{
				ID:        uuid.New().String(),
				Type:      model.AssetTypeSubdomain,
				Value:     sub,
				Status:    model.AssetStatusNew,
				Tags:      []string{},
				Metadata:  map[string]interface{}{},
				FirstSeen: time.Now().UTC(),
				LastSeen:  time.Now().UTC(),
			})
		}
	}

	tr := buildToolRun(s, startedAt, model.ToolRunCompleted, "", len(inputs), len(result.Assets))
	tr.OutputSummary = fmt.Sprintf("Discovered %d subdomain(s) across %d input domain(s)", len(result.Assets), len(inputs))
	attachExecContext(&tr, lastCmd, lastExit, stderrAcc.String(), inputs)
	result.ToolRun = tr
	return result, nil
}

func (s *SubfinderTool) enumerate(ctx context.Context, domain string, opts RunOptions) (map[string]struct{}, string, string, int, error) {
	args := []string{
		"-d", domain,
		"-silent",
		"-duc", // disable update check
	}

	if opts.Timeout > 0 {
		args = append(args, "-timeout", fmt.Sprintf("%d", opts.Timeout))
	}

	if threads, ok := opts.ExtraArgs["threads"]; ok {
		args = append(args, "-t", threads)
	}

	// daemon-context-audited (SPEC-X1): subfinder is the only real
	// subprocess in the detection pipeline; using CommandContext ensures
	// runner-cancellation propagates so `daemon stop` leaves no orphans.
	cmd := exec.CommandContext(ctx, "subfinder", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	cmdStr := "subfinder " + strings.Join(args, " ")
	runErr := cmd.Run()
	exitCode := 0
	if runErr != nil {
		exitCode = cmd.ProcessState.ExitCode()
		// If we got some output, parse it anyway — subfinder sometimes
		// fails the overall process but produces partial results.
		if stdout.Len() == 0 {
			return nil, cmdStr, stderr.String(), exitCode, fmt.Errorf("subfinder failed: %s", stderr.String())
		}
	}

	return ParseSubfinderOutput(stdout.Bytes()), cmdStr, stderr.String(), exitCode, nil
}

// ParseSubfinderOutput parses subfinder text output (one subdomain per line).
func ParseSubfinderOutput(data []byte) map[string]struct{} {
	results := make(map[string]struct{})
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		sub := strings.ToLower(strings.TrimSpace(scanner.Text()))
		if sub != "" {
			results[sub] = struct{}{}
		}
	}
	return results
}

// ParseSubfinderFile parses a file with one subdomain per line (for testing).
func ParseSubfinderFile(path string) (map[string]struct{}, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseSubfinderOutput(data), nil
}

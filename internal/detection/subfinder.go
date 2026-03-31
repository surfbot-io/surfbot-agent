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

func (s *SubfinderTool) Run(ctx context.Context, inputs []string, opts RunOptions) (*RunResult, error) {
	startedAt := time.Now().UTC()

	if !s.Available() {
		tr := buildToolRun(s, startedAt, model.ToolRunSkipped, "subfinder binary not found in PATH", len(inputs), 0)
		return &RunResult{ToolRun: tr}, nil
	}

	result := &RunResult{}

	for _, domain := range inputs {
		subs, err := s.enumerate(ctx, domain, opts)
		if err != nil {
			result.ToolRun = buildToolRun(s, startedAt, model.ToolRunFailed, err.Error(), len(inputs), 0)
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

	result.ToolRun = buildToolRun(s, startedAt, model.ToolRunCompleted, "", len(inputs), len(result.Assets))
	return result, nil
}

func (s *SubfinderTool) enumerate(ctx context.Context, domain string, opts RunOptions) (map[string]struct{}, error) {
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

	cmd := exec.CommandContext(ctx, "subfinder", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// If we got some output, parse it anyway
		if stdout.Len() == 0 {
			return nil, fmt.Errorf("subfinder failed: %s", stderr.String())
		}
	}

	return ParseSubfinderOutput(stdout.Bytes()), nil
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

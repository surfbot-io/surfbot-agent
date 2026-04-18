package detection

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/projectdiscovery/goflags"
	subfinderRunner "github.com/projectdiscovery/subfinder/v2/pkg/runner"

	"github.com/surfbot-io/surfbot-agent/internal/model"
)

// SubfinderTool discovers subdomains by driving the upstream subfinder SDK
// in-process. Previously this shelled out to a `subfinder` binary on PATH;
// that created a hidden install dependency — users who ran the binary
// without installing the ProjectDiscovery toolchain silently got empty
// discovery results. Embedding the SDK aligns subfinder with how nuclei /
// naabu / httpx / dnsx already run (all Library-kind, no subprocess) and
// removes the install-time surprise.
type SubfinderTool struct{}

func NewSubfinderTool() *SubfinderTool { return &SubfinderTool{} }

func (s *SubfinderTool) Name() string   { return "subfinder" }
func (s *SubfinderTool) Phase() string  { return "discovery" }
func (s *SubfinderTool) Kind() ToolKind { return ToolKindLibrary }

// Available always returns true — the SDK is linked into the binary so
// there's no runtime requirement to check. Kept so the DetectionTool
// interface contract stays uniform across tools.
func (s *SubfinderTool) Available() bool { return true }

func (s *SubfinderTool) Command() string { return "discover" }
func (s *SubfinderTool) Description() string {
	return "Discover subdomains for a target domain using passive sources"
}
func (s *SubfinderTool) InputType() string     { return "domains" }
func (s *SubfinderTool) OutputTypes() []string { return []string{"subdomain"} }

func (s *SubfinderTool) Run(ctx context.Context, inputs []string, opts RunOptions) (*RunResult, error) {
	startedAt := time.Now().UTC()

	if len(inputs) == 0 {
		tr := buildToolRun(s, startedAt, model.ToolRunCompleted, "", 0, 0)
		tr.OutputSummary = "No input domains — skipped."
		attachExecContext(&tr, subfinderCommandLabel(opts), 0, "", inputs)
		return &RunResult{ToolRun: tr}, nil
	}

	options := buildSubfinderOptions(opts)
	runner, err := subfinderRunner.NewRunner(options)
	if err != nil {
		tr := buildToolRun(s, startedAt, model.ToolRunFailed, err.Error(), len(inputs), 0)
		attachExecContext(&tr, subfinderCommandLabel(opts), 1, err.Error(), inputs)
		return &RunResult{ToolRun: tr}, fmt.Errorf("subfinder runner init: %w", err)
	}

	result := &RunResult{}
	var stderrAcc strings.Builder
	// io.Discard for the per-enumeration writer: we read results from the
	// return-value map, not the stdout-style sink. The SDK requires
	// non-nil writers though.
	writers := []io.Writer{io.Discard}

	for _, domain := range inputs {
		enumerated, enumErr := runner.EnumerateSingleDomainWithCtx(ctx, domain, writers)
		if enumErr != nil {
			// Continue on per-domain error — subfinder frequently fails
			// one source while others succeed, and we want whatever we
			// got. Log the error into the tool_run stderr surface.
			fmt.Fprintf(&stderrAcc, "[subfinder] %s: %v\n", domain, enumErr)
		}

		// enumerated is map[subdomain]map[source]struct{}. We only care
		// about the subdomain keys.
		for sub := range enumerated {
			sub = strings.ToLower(strings.TrimSpace(sub))
			if sub == "" {
				continue
			}
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

		// Always include the root domain itself so downstream phases have
		// something to work with even if passive sources returned nothing.
		rootAsset := model.Asset{
			ID:        uuid.New().String(),
			Type:      model.AssetTypeSubdomain,
			Value:     strings.ToLower(domain),
			Status:    model.AssetStatusNew,
			Tags:      []string{},
			Metadata:  map[string]interface{}{},
			FirstSeen: time.Now().UTC(),
			LastSeen:  time.Now().UTC(),
		}
		if _, found := enumerated[rootAsset.Value]; !found {
			result.Assets = append(result.Assets, rootAsset)
		}
	}

	tr := buildToolRun(s, startedAt, model.ToolRunCompleted, "", len(inputs), len(result.Assets))
	tr.OutputSummary = fmt.Sprintf("Discovered %d subdomain(s) across %d input domain(s) via subfinder SDK (passive)",
		len(result.Assets), len(inputs))
	attachExecContext(&tr, subfinderCommandLabel(opts), 0, stderrAcc.String(), inputs)
	tr.Config["threads"] = options.Threads
	tr.Config["timeout_sec"] = options.Timeout
	tr.Config["max_enumeration_time_min"] = options.MaxEnumerationTime
	result.ToolRun = tr
	return result, nil
}

// buildSubfinderOptions translates pipeline RunOptions + ExtraArgs into
// subfinder SDK options. Defaults favor correctness over speed — All=true
// uses every available passive source, which mirrors what the CLI
// `subfinder -all` does. Keys that require external auth (Shodan, GitHub)
// will silently return nothing if the user hasn't configured
// ~/.config/subfinder/provider-config.yaml; that's the upstream behavior
// and we don't pretend otherwise.
func buildSubfinderOptions(opts RunOptions) *subfinderRunner.Options {
	threads := 10
	if v, ok := opts.ExtraArgs["threads"]; ok {
		if n, err := parseInt(v); err == nil && n > 0 {
			threads = n
		}
	}

	timeout := 30
	if opts.Timeout > 0 {
		timeout = opts.Timeout
	}

	maxEnum := 10
	if v, ok := opts.ExtraArgs["max_enumeration_time"]; ok {
		if n, err := parseInt(v); err == nil && n > 0 {
			maxEnum = n
		}
	}

	// Domain field is populated per-call via EnumerateSingleDomainWithCtx,
	// so we don't set it here — that field is only used by RunEnumeration().
	return &subfinderRunner.Options{
		Threads:            threads,
		Timeout:            timeout,
		MaxEnumerationTime: maxEnum,
		All:                true,
		Silent:             true,
		NoColor:            true,
		DisableUpdateCheck: true,
		// Output must be non-nil to avoid a nil-pointer when the SDK
		// writes progress lines. io.Discard drops them.
		Output:         io.Discard,
		Domain:         goflags.StringSlice{},
		Sources:        goflags.StringSlice{},
		ExcludeSources: goflags.StringSlice{},
	}
}

func subfinderCommandLabel(opts RunOptions) string {
	threads := "10"
	if v, ok := opts.ExtraArgs["threads"]; ok && v != "" {
		threads = v
	}
	return fmt.Sprintf("subfinder (SDK in-process, all-sources, threads=%s, timeout=%ds)",
		threads, maxInt(opts.Timeout, 30))
}

func maxInt(v, fallback int) int {
	if v > 0 {
		return v
	}
	return fallback
}

// parseInt is a local int parser used by buildSubfinderOptions to avoid
// pulling strconv just for two call sites.
func parseInt(s string) (int, error) {
	var n int
	_, err := fmt.Sscanf(s, "%d", &n)
	return n, err
}

// ParseSubfinderOutput parses subfinder text output (one subdomain per line).
// Retained for test coverage of the historic text-based parser; no longer
// used by Run() now that results come straight from the SDK map return.
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

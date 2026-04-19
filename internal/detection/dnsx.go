package detection

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/surfbot-io/surfbot-agent/internal/model"
)

// DNSXTool resolves DNS records using the Go standard library.
type DNSXTool struct{}

func NewDNSXTool() *DNSXTool { return &DNSXTool{} }

// dnsxResolver is the narrow surface DNSXTool uses from the system
// resolver. Production calls net.DefaultResolver; tests inject a fake
// to assert params propagation and ctx cancellation.
type dnsxResolver interface {
	LookupHost(ctx context.Context, host string) ([]string, error)
	LookupCNAME(ctx context.Context, host string) (string, error)
}

// dnsxResolverOverride lets tests substitute the resolver. Production
// callers leave it nil and dnsx uses net.DefaultResolver.
var dnsxResolverOverride dnsxResolver

func resolveDnsxResolver() dnsxResolver {
	if dnsxResolverOverride != nil {
		return dnsxResolverOverride
	}
	return net.DefaultResolver
}

// resolveDnsxParams returns the params DNSXTool should run with,
// preferring opts.DnsxParams when supplied and falling back to
// model.DefaultDnsxParams() per-field.
func resolveDnsxParams(opts RunOptions) model.DnsxParams {
	defaults := model.DefaultDnsxParams()
	if opts.DnsxParams == nil {
		return defaults
	}
	resolved := *opts.DnsxParams
	if len(resolved.RecordTypes) == 0 {
		resolved.RecordTypes = defaults.RecordTypes
	}
	if resolved.Retries <= 0 {
		resolved.Retries = defaults.Retries
	}
	return resolved
}

// wantsRecordType is a case-insensitive membership check used to gate
// the per-record-type lookups. Honors params.RecordTypes.
func wantsRecordType(types []string, want string) bool {
	for _, t := range types {
		if strings.EqualFold(t, want) {
			return true
		}
	}
	return false
}

func (d *DNSXTool) Name() string   { return "dnsx" }
func (d *DNSXTool) Phase() string  { return "resolution" }
func (d *DNSXTool) Kind() ToolKind { return ToolKindNative }
func (d *DNSXTool) Available() bool { return true }

func (d *DNSXTool) Command() string       { return "resolve" }
func (d *DNSXTool) Description() string   { return "Resolve domains to IP addresses via DNS lookup" }
func (d *DNSXTool) InputType() string     { return "domains" }
func (d *DNSXTool) OutputTypes() []string { return []string{"ipv4", "ipv6"} }

type dnsResult struct {
	Host  string
	IPs   []string
	CNAME string
}

// lookupHostWithRetries calls resolver.LookupHost up to (1 + retries)
// times, returning the first non-error result. Honors ctx cancellation
// between attempts.
func lookupHostWithRetries(ctx context.Context, resolver dnsxResolver, host string, retries int) ([]string, error) {
	if retries < 0 {
		retries = 0
	}
	var lastErr error
	for attempt := 0; attempt <= retries; attempt++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		ips, err := resolver.LookupHost(ctx, host)
		if err == nil {
			return ips, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func (d *DNSXTool) Run(ctx context.Context, inputs []string, opts RunOptions) (*RunResult, error) {
	startedAt := time.Now().UTC()

	concurrency := opts.RateLimit
	if concurrency <= 0 {
		concurrency = 50
	}
	params := resolveDnsxParams(opts)
	resolver := resolveDnsxResolver()
	wantA := wantsRecordType(params.RecordTypes, "A") || wantsRecordType(params.RecordTypes, "AAAA")
	wantCNAME := wantsRecordType(params.RecordTypes, "CNAME")

	results := make([]dnsResult, 0, len(inputs))
	// errLog accumulates per-host resolution errors so the tool_run record
	// shows the user what went wrong (NXDOMAIN, timeouts, …) rather than
	// silently dropping hosts. dnsx isn't a subprocess so "stderr" here is
	// synthesized.
	var errLog strings.Builder
	var mu sync.Mutex
	resolved := 0
	failed := 0

	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for _, host := range inputs {
		wg.Add(1)
		sem <- struct{}{}
		go func(h string) {
			defer wg.Done()
			defer func() { <-sem }()

			rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()

			dr := dnsResult{Host: h}

			if wantA {
				ips, err := lookupHostWithRetries(rctx, resolver, h, params.Retries)
				if err == nil {
					dr.IPs = ips
				} else {
					mu.Lock()
					fmt.Fprintf(&errLog, "[dnsx] %s: %v\n", h, err)
					mu.Unlock()
				}
			}

			if wantCNAME {
				cname, err := resolver.LookupCNAME(rctx, h)
				if err == nil && cname != "" {
					dr.CNAME = strings.TrimSuffix(cname, ".")
				}
			}

			mu.Lock()
			if len(dr.IPs) > 0 {
				results = append(results, dr)
				resolved++
			} else {
				failed++
			}
			mu.Unlock()
		}(host)
	}

	wg.Wait()

	// Build assets
	runResult := &RunResult{}
	seen := make(map[string]struct{})

	for _, dr := range results {
		for _, ip := range dr.IPs {
			if _, ok := seen[ip]; ok {
				continue
			}
			seen[ip] = struct{}{}

			assetType := model.AssetTypeIPv4
			if strings.Contains(ip, ":") {
				assetType = model.AssetTypeIPv6
			}

			meta := map[string]interface{}{
				"resolved_from": dr.Host,
			}
			if dr.CNAME != "" {
				meta["cname"] = dr.CNAME
			}

			runResult.Assets = append(runResult.Assets, model.Asset{
				ID:        uuid.New().String(),
				Type:      assetType,
				Value:     ip,
				Status:    model.AssetStatusNew,
				Tags:      []string{},
				Metadata:  meta,
				FirstSeen: time.Now().UTC(),
				LastSeen:  time.Now().UTC(),
			})
		}
	}

	tr := buildToolRun(d, startedAt, model.ToolRunCompleted, "", len(inputs), len(runResult.Assets))
	tr.OutputSummary = fmt.Sprintf("Resolved %d of %d hosts to %d unique IP(s) (concurrency=%d)",
		resolved, len(inputs), len(runResult.Assets), concurrency)
	// No external process → exit_code 0, command is a human-readable label.
	attachExecContext(&tr,
		fmt.Sprintf("dnsx (in-process resolver, concurrency=%d)", concurrency),
		0,
		errLog.String(),
		inputs,
	)
	if failed > 0 {
		tr.Config["unresolved_hosts"] = failed
	}
	runResult.ToolRun = tr
	return runResult, nil
}

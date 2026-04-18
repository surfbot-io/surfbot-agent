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

func (d *DNSXTool) Run(ctx context.Context, inputs []string, opts RunOptions) (*RunResult, error) {
	startedAt := time.Now().UTC()

	concurrency := opts.RateLimit
	if concurrency <= 0 {
		concurrency = 50
	}

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

			ips, err := net.DefaultResolver.LookupHost(rctx, h)
			if err == nil {
				dr.IPs = ips
			} else {
				mu.Lock()
				fmt.Fprintf(&errLog, "[dnsx] %s: %v\n", h, err)
				mu.Unlock()
			}

			cname, err := net.DefaultResolver.LookupCNAME(rctx, h)
			if err == nil && cname != "" {
				dr.CNAME = strings.TrimSuffix(cname, ".")
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

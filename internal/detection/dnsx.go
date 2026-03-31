package detection

import (
	"context"
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
	var mu sync.Mutex

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
			}

			cname, err := net.DefaultResolver.LookupCNAME(rctx, h)
			if err == nil && cname != "" {
				dr.CNAME = strings.TrimSuffix(cname, ".")
			}

			if len(dr.IPs) > 0 {
				mu.Lock()
				results = append(results, dr)
				mu.Unlock()
			}
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

	runResult.ToolRun = buildToolRun(d, startedAt, model.ToolRunCompleted, "", len(inputs), len(runResult.Assets))
	return runResult, nil
}

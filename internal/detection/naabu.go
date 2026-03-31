package detection

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/surfbot-io/surfbot-agent/internal/model"
)

// Top100Ports is the default set of ports to scan.
var Top100Ports = []int{
	21, 22, 23, 25, 53, 80, 81, 110, 111, 113, 135, 139, 143, 179, 199,
	443, 445, 465, 514, 515, 548, 554, 587, 646, 993, 995, 1025, 1026,
	1027, 1433, 1720, 1723, 2000, 2001, 3000, 3001, 3128, 3306, 3389,
	4443, 4567, 5000, 5060, 5222, 5432, 5555, 5601, 5900, 5984, 5985,
	6000, 6001, 6379, 6443, 7001, 7002, 7443, 8000, 8008, 8009, 8010,
	8020, 8080, 8081, 8082, 8083, 8084, 8085, 8086, 8087, 8088, 8089,
	8090, 8091, 8181, 8443, 8880, 8888, 9000, 9001, 9090, 9091, 9200,
	9300, 9443, 9999, 10000, 10443, 27017, 27018, 28017, 50000, 50070,
}

// NaabuTool performs TCP connect scanning using the Go standard library.
type NaabuTool struct{}

func NewNaabuTool() *NaabuTool { return &NaabuTool{} }

func (n *NaabuTool) Name() string   { return "naabu" }
func (n *NaabuTool) Phase() string  { return "port_scan" }
func (n *NaabuTool) Kind() ToolKind { return ToolKindNative }
func (n *NaabuTool) Available() bool { return true }

func (n *NaabuTool) Run(ctx context.Context, inputs []string, opts RunOptions) (*RunResult, error) {
	startedAt := time.Now().UTC()

	ports, err := ParsePorts(opts.ExtraArgs["ports"])
	if err != nil {
		return nil, fmt.Errorf("naabu: parsing ports: %w", err)
	}

	dialTimeout := 3 * time.Second
	if t, ok := opts.ExtraArgs["timeout"]; ok {
		if secs, err := strconv.Atoi(t); err == nil && secs > 0 {
			dialTimeout = time.Duration(secs) * time.Second
		}
	}

	concurrency := opts.RateLimit
	if concurrency <= 0 {
		concurrency = 100
	}

	type openPort struct {
		IP   string
		Port int
	}

	var results []openPort
	var mu sync.Mutex

	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for _, ip := range inputs {
		for _, port := range ports {
			if ctx.Err() != nil {
				break
			}
			wg.Add(1)
			sem <- struct{}{}
			go func(ip string, port int) {
				defer wg.Done()
				defer func() { <-sem }()

				addr := fmt.Sprintf("%s:%d", ip, port)
				conn, err := net.DialTimeout("tcp", addr, dialTimeout)
				if err != nil {
					return
				}
				conn.Close()

				mu.Lock()
				results = append(results, openPort{IP: ip, Port: port})
				mu.Unlock()
			}(ip, port)
		}
	}

	wg.Wait()

	runResult := &RunResult{}
	for _, r := range results {
		runResult.Assets = append(runResult.Assets, model.Asset{
			ID:        uuid.New().String(),
			Type:      model.AssetTypePort,
			Value:     fmt.Sprintf("%s:%d/tcp", r.IP, r.Port),
			Status:    model.AssetStatusNew,
			Tags:      []string{},
			Metadata:  map[string]interface{}{"port": r.Port, "protocol": "tcp", "ip": r.IP},
			FirstSeen: time.Now().UTC(),
			LastSeen:  time.Now().UTC(),
		})
	}

	runResult.ToolRun = buildToolRun(n, startedAt, model.ToolRunCompleted, "", len(inputs), len(runResult.Assets))
	return runResult, nil
}

// ParsePorts parses a port specification string into a list of ports.
// Supported formats: "80,443", "1-100", "top100", "" (default top100).
func ParsePorts(spec string) ([]int, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" || spec == "top100" {
		return Top100Ports, nil
	}

	if spec == "top1000" {
		// For now, top1000 just returns top100 — full list would be added later
		return Top100Ports, nil
	}

	var ports []int
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if strings.Contains(part, "-") {
			bounds := strings.SplitN(part, "-", 2)
			start, err := strconv.Atoi(strings.TrimSpace(bounds[0]))
			if err != nil {
				return nil, fmt.Errorf("invalid port range start: %q", bounds[0])
			}
			end, err := strconv.Atoi(strings.TrimSpace(bounds[1]))
			if err != nil {
				return nil, fmt.Errorf("invalid port range end: %q", bounds[1])
			}
			if start > end || start < 1 || end > 65535 {
				return nil, fmt.Errorf("invalid port range: %d-%d", start, end)
			}
			for p := start; p <= end; p++ {
				ports = append(ports, p)
			}
		} else {
			p, err := strconv.Atoi(part)
			if err != nil {
				return nil, fmt.Errorf("invalid port: %q", part)
			}
			if p < 1 || p > 65535 {
				return nil, fmt.Errorf("port out of range: %d", p)
			}
			ports = append(ports, p)
		}
	}

	return ports, nil
}
